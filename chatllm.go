package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// chatModel implements model.LLM on top of the classic OpenAI
// /chat/completions endpoint. google/adk-go's openaimodel package speaks only
// the Responses API (/v1/responses), which the providers that are actually
// free and keyless do not expose: Ollama, LM Studio, llama.cpp and Pollinations
// all answer on /chat/completions only. This adapter is what makes them
// usable without any key or configuration.
// genaiSchemaToJSON converts a genai.Schema into the plain JSON Schema map the
// chat/completions tool format expects. genai encodes the type as an uppercase
// enum name ("OBJECT"), while JSON Schema wants lowercase ("object"), and
// omits a schema with no type at all, so the type is normalised by hand rather
// than by marshalling the struct.
func genaiSchemaToJSON(s *genai.Schema) map[string]any {
	if s == nil {
		return map[string]any{"type": "object"}
	}
	out := map[string]any{}
	if t := strings.ToLower(strings.TrimSpace(string(s.Type))); t != "" && t != "type_unspecified" {
		out["type"] = t
	}
	if s.Description != "" {
		out["description"] = s.Description
	}
	if len(s.Enum) > 0 {
		out["enum"] = s.Enum
	}
	if len(s.Properties) > 0 {
		props := make(map[string]any, len(s.Properties))
		for name, ps := range s.Properties {
			props[name] = genaiSchemaToJSON(ps)
		}
		out["properties"] = props
	}
	if len(s.Required) > 0 {
		out["required"] = s.Required
	}
	if s.Items != nil {
		out["items"] = genaiSchemaToJSON(s.Items)
	}
	return out
}

func genaiToolsToChat(tools []*genai.Tool) []chatTool {
	var out []chatTool
	for _, t := range tools {
		if t == nil {
			continue
		}
		for _, fd := range t.FunctionDeclarations {
			if fd == nil || fd.Name == "" {
				continue
			}
			var ct chatTool
			ct.Type = "function"
			ct.Function.Name = fd.Name
			ct.Function.Description = fd.Description
			ct.Function.Parameters = genaiSchemaToJSON(fd.Parameters)
			out = append(out, ct)
		}
	}
	return out
}

// contentsToChat flattens genai contents into chat messages. Function calls and
// function responses arrive as separate model/user contents and become
// tool_calls / tool messages respectively, which is how the chat API expects
// the tool loop to be spelled.
func contentsToChat(contents []*genai.Content) []chatMessage {
	var msgs []chatMessage
	for _, c := range contents {
		if c == nil {
			continue
		}
		role := "user"
		if c.Role == genai.RoleModel {
			role = "assistant"
		}
		var text strings.Builder
		var calls []chatToolCall
		for _, p := range c.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.FunctionCall != nil:
				args := "{}"
				if p.FunctionCall.Args != nil {
					if b, err := json.Marshal(p.FunctionCall.Args); err == nil {
						args = string(b)
					}
				}
				tc := chatToolCall{Type: "function"}
				tc.ID = p.FunctionCall.ID
				tc.Function.Name = p.FunctionCall.Name
				tc.Function.Arguments = args
				calls = append(calls, tc)
			case p.FunctionResponse != nil:
				// A tool result is its own message in the chat format, emitted
				// before any remaining text of this content.
				body := "{}"
				if p.FunctionResponse.Response != nil {
					if b, err := json.Marshal(p.FunctionResponse.Response); err == nil {
						body = string(b)
					}
				}
				msgs = append(msgs, chatMessage{
					Role:       "tool",
					Name:       p.FunctionResponse.Name,
					ToolCallID: p.FunctionResponse.ID,
					Content:    body,
				})
			case p.Text != "":
				text.WriteString(p.Text)
			}
		}
		if text.Len() > 0 || len(calls) > 0 {
			msgs = append(msgs, chatMessage{
				Role:      role,
				Content:   text.String(),
				ToolCalls: calls,
			})
		}
	}
	return msgs
}

func usageToGenai(u *chatUsage) *genai.GenerateContentResponseUsageMetadata {
	if u == nil {
		return nil
	}
	return &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:     int32(u.PromptTokens),
		CandidatesTokenCount: int32(u.CompletionTokens),
		TotalTokenCount:      int32(u.TotalTokens),
	}
}

func chatFinishReason(s string) genai.FinishReason {
	switch s {
	case "length":
		return genai.FinishReasonMaxTokens
	case "content_filter":
		return genai.FinishReasonSafety
	case "tool_calls", "function_call", "stop", "":
		return genai.FinishReasonStop
	default:
		return genai.FinishReasonStop
	}
}

func singleErrorSequence(err error) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(nil, err)
	}
}

// toolCallsToParts renders accumulated tool calls as genai parts. Arguments
// that are not valid JSON objects are reported rather than silently dropped, so
// a malformed call surfaces as a turn error instead of an empty function call.
func toolCallsToParts(calls []chatToolCall) ([]*genai.Part, error) {
	var parts []*genai.Part
	for _, tc := range calls {
		if tc.Function.Name == "" {
			continue
		}
		raw := strings.TrimSpace(tc.Function.Arguments)
		if raw == "" {
			raw = "{}"
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return nil, fmt.Errorf("dmcode: не удалось разобрать аргументы вызова %s: %w", tc.Function.Name, err)
		}
		if args == nil {
			args = map[string]any{}
		}
		parts = append(parts, &genai.Part{
			FunctionCall: &genai.FunctionCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: args,
			},
		})
	}
	return parts, nil
}

type chatModel struct {
	baseURL string
	apiKey  string
	name    string
	client  *http.Client
}

func newChatModel(baseURL, apiKey, name string) *chatModel {
	return &chatModel{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		name:    name,
		client:  &http.Client{Timeout: 0},
	}
}

func (m *chatModel) Name() string { return m.name }

// buildChatRequest assembles the wire request from an ADK LLMRequest.
func (m *chatModel) buildChatRequest(req *model.LLMRequest, stream bool) (chatRequest, error) {
	if req == nil {
		return chatRequest{}, fmt.Errorf("dmcode: пустой запрос к модели")
	}
	name := m.name
	if req.Model != "" {
		name = req.Model
	}
	var msgs []chatMessage
	if req.Config != nil && req.Config.SystemInstruction != nil {
		var sys strings.Builder
		for _, p := range req.Config.SystemInstruction.Parts {
			if p != nil && p.Text != "" {
				sys.WriteString(p.Text)
			}
		}
		if sys.Len() > 0 {
			msgs = append(msgs, chatMessage{Role: "system", Content: sys.String()})
		}
	}
	msgs = append(msgs, contentsToChat(req.Contents)...)
	if len(msgs) == 0 {
		return chatRequest{}, fmt.Errorf("dmcode: в запросе нет ни одного сообщения")
	}
	out := chatRequest{Model: name, Messages: msgs, Stream: stream}
	if cfg := req.Config; cfg != nil {
		if cfg.Temperature != nil {
			t := float32(*cfg.Temperature)
			out.Temperature = &t
		}
		if cfg.MaxOutputTokens > 0 {
			out.MaxOutputTokens = int(cfg.MaxOutputTokens)
		}
		out.Tools = genaiToolsToChat(cfg.Tools)
	}
	return out, nil
}

func (m *chatModel) doRequest(ctx context.Context, body chatRequest) (*http.Response, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("dmcode: не удалось собрать тело запроса: %w", err)
	}
	url := m.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Some keyless endpoints (Pollinations) ignore the header but reject a
	// request without one, so a placeholder is always sent.
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	} else {
		req.Header.Set("Authorization", "Bearer dmcode")
	}
	return m.client.Do(req)
}

// httpError renders a non-2xx reply, including the server's own JSON message,
// because the bare status ("500 Internal Server Error") rarely says anything
// actionable.
func httpError(op string, resp *http.Response) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
	msg := strings.TrimSpace(string(snippet))
	if msg != "" {
		var e chatResponse
		if json.Unmarshal(snippet, &e) == nil && e.Error != nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		return fmt.Errorf("%s: %s: %s", op, resp.Status, truncate(msg, 200))
	}
	return fmt.Errorf("%s: %s", op, resp.Status)
}

// toolCallBuffer accumulates streamed tool calls. The chat API splits one call
// across chunks: the first carries the id and name, later ones carry only
// argument fragments keyed by the call's `index`.
type toolCallBuffer struct {
	order []int
	calls map[int]*chatToolCall
	args  map[int]*strings.Builder
}

func newToolCallBuffer() *toolCallBuffer {
	return &toolCallBuffer{calls: map[int]*chatToolCall{}, args: map[int]*strings.Builder{}}
}

func (b *toolCallBuffer) add(idx int, tc chatToolCall) {
	cur, ok := b.calls[idx]
	if !ok {
		cur = &chatToolCall{Type: "function"}
		if tc.ID != "" {
			cur.ID = tc.ID
		}
		if tc.Function.Name != "" {
			cur.Function.Name = tc.Function.Name
		}
		b.calls[idx] = cur
		b.order = append(b.order, idx)
	} else {
		if tc.ID != "" && cur.ID == "" {
			cur.ID = tc.ID
		}
		if tc.Function.Name != "" && cur.Function.Name == "" {
			cur.Function.Name = tc.Function.Name
		}
	}
	if tc.Function.Arguments != "" {
		buf, ok := b.args[idx]
		if !ok {
			buf = &strings.Builder{}
			b.args[idx] = buf
		}
		buf.WriteString(tc.Function.Arguments)
	}
}

// snapshot returns the accumulated calls in the order they first appeared.
func (b *toolCallBuffer) snapshot() []chatToolCall {
	out := make([]chatToolCall, 0, len(b.order))
	for _, idx := range b.order {
		cur := b.calls[idx]
		tc := *cur
		if buf, ok := b.args[idx]; ok {
			tc.Function.Arguments = buf.String()
		}
		out = append(out, tc)
	}
	return out
}

func (m *chatModel) generateStream(ctx context.Context, body chatRequest) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		resp, err := m.doRequest(ctx, body)
		if err != nil {
			yield(nil, fmt.Errorf("dmcode: %s: %w", m.baseURL, err))
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			yield(nil, httpError(m.baseURL, resp))
			return
		}

		calls := newToolCallBuffer()
		var text strings.Builder
		var usage *chatUsage
		var modelVer, finish string

		// The turn is closed with one aggregated non-partial event, which is
		// what the ADK runner requires to persist the turn and dispatch tools.
		defer func() {
			parts, err := toolCallsToParts(calls.snapshot())
			if err != nil {
				yield(nil, err)
				return
			}
			if text.Len() > 0 {
				parts = append([]*genai.Part{{Text: text.String()}}, parts...)
			}
			yield(&model.LLMResponse{
				Content:       &genai.Content{Role: genai.RoleModel, Parts: parts},
				FinishReason:  chatFinishReason(finish),
				UsageMetadata: usageToGenai(usage),
				ModelVersion:  modelVer,
				TurnComplete:  true,
			}, nil)
		}()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "[DONE]" {
				return
			}
			var chunk chatResponse
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				// A malformed frame is skipped rather than failing the turn:
				// some gateways interleave keep-alive or non-JSON comments.
				continue
			}
			if chunk.Error != nil && chunk.Error.Message != "" {
				yield(nil, fmt.Errorf("%s: %s", m.baseURL, chunk.Error.Message))
				return
			}
			if chunk.Model != "" {
				modelVer = chunk.Model
			}
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			ch := chunk.Choices[0]
			if ch.FinishReason != "" {
				finish = ch.FinishReason
			}
			// Reasoning traces are not surfaced: the TUI has no place for
			// them and they would drown the actual answer.
			for i, tc := range ch.Delta.ToolCalls {
				idx := i
				if tc.Index != nil {
					idx = *tc.Index
				}
				calls.add(idx, tc)
			}
			if ch.Delta.Content != "" {
				text.WriteString(ch.Delta.Content)
				if !yield(&model.LLMResponse{
					Content: &genai.Content{
						Role:  genai.RoleModel,
						Parts: []*genai.Part{{Text: ch.Delta.Content}},
					},
					Partial: true,
				}, nil) {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			if ctx.Err() != nil {
				return
			}
			yield(nil, fmt.Errorf("dmcode: обрыв потока от %s: %w", m.baseURL, err))
		}
	}
}

func (m *chatModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if req == nil {
		return singleErrorSequence(fmt.Errorf("dmcode: пустой запрос к модели"))
	}
	body, err := m.buildChatRequest(req, stream)
	if err != nil {
		return singleErrorSequence(err)
	}
	if stream {
		return m.generateStream(ctx, body)
	}
	return m.generate(ctx, body)
}

func (m *chatModel) generate(ctx context.Context, body chatRequest) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		resp, err := m.doRequest(ctx, body)
		if err != nil {
			yield(nil, fmt.Errorf("dmcode: %s: %w", m.baseURL, err))
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			yield(nil, httpError(m.baseURL, resp))
			return
		}
		var parsed chatResponse
		if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
			yield(nil, fmt.Errorf("dmcode: не удалось разобрать ответ %s: %w", m.baseURL, err))
			return
		}
		if parsed.Error != nil && parsed.Error.Message != "" {
			yield(nil, fmt.Errorf("%s: %s", m.baseURL, parsed.Error.Message))
			return
		}
		if len(parsed.Choices) == 0 {
			yield(nil, fmt.Errorf("%s: провайдер вернул пустой ответ", m.baseURL))
			return
		}
		ch := parsed.Choices[0]
		parts, err := toolCallsToParts(ch.Message.ToolCalls)
		if err != nil {
			yield(nil, err)
			return
		}
		if ch.Message.Content != "" {
			parts = append([]*genai.Part{{Text: ch.Message.Content}}, parts...)
		}
		if len(parts) == 0 {
			// A model that answered only with reasoning still completed a turn.
			yield(&model.LLMResponse{
				Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{}},
				FinishReason: genai.FinishReasonStop,
				ModelVersion: parsed.Model,
			}, nil)
			return
		}
		yield(&model.LLMResponse{
			Content:       &genai.Content{Role: genai.RoleModel, Parts: parts},
			FinishReason:  chatFinishReason(ch.FinishReason),
			UsageMetadata: usageToGenai(parsed.Usage),
			ModelVersion:  parsed.Model,
		}, nil)
	}
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

type chatToolCall struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Index    *int   `json:"index,omitempty"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Parameters  any    `json:"parameters,omitempty"`
	} `json:"function"`
}

type chatRequest struct {
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	Tools           []chatTool    `json:"tools,omitempty"`
	Stream          bool          `json:"stream,omitempty"`
	Temperature     *float32      `json:"temperature,omitempty"`
	MaxOutputTokens int           `json:"max_tokens,omitempty"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string         `json:"role"`
			Content   string         `json:"content"`
			Reasoning string         `json:"reasoning"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"message"`
		Delta struct {
			Content   string         `json:"content"`
			Reasoning string         `json:"reasoning"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}
