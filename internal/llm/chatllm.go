package llm

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

	"github.com/dedomorozoff/dmcode/internal/config"
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
			ct.Function.Parameters = declParams(fd)
			out = append(out, ct)
		}
	}
	return out
}

// declParams renders a tool's argument schema for the chat format.
//
// It has to look at ParametersJsonSchema as well as Parameters, because
// functiontool fills in the former and leaves the latter nil — the two fields
// are mutually exclusive in genai. Reading only Parameters sent every tool as
// {"type":"object"}, telling the model the instruments take no arguments at
// all. A model that cannot see a tool's contract cannot call it correctly: it
// invents argument names, a tool silently ignores the ones it does not know,
// the answer comes back unchanged, and the model tries the next guess — the
// same list_dir call over and over until the endpoint gives up.
func declParams(fd *genai.FunctionDeclaration) map[string]any {
	if fd.Parameters != nil {
		return genaiSchemaToJSON(fd.Parameters)
	}
	if fd.ParametersJsonSchema != nil {
		if m, ok := jsonSchemaToJSON(fd.ParametersJsonSchema); ok {
			return m
		}
	}
	// A tool with no declared arguments is still sent an object schema: the
	// chat format expects one, and an absent "parameters" is a rejected
	// request rather than a permissive one.
	return map[string]any{"type": "object"}
}

// jsonSchemaToJSON normalises an arbitrary JSON Schema value into the map the
// chat format wants: the type is lowercased, and the keys the format cares
// about are pulled out of a schema that arrived as untyped JSON.
//
// The value reaches us as `any` — jsonschema-go and hand-written declarations
// both land there — so it is walked rather than type-asserted. Anything that is
// not a schema object is reported as unusable and the caller falls back.
func jsonSchemaToJSON(v any) (map[string]any, bool) {
	switch s := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(s))
		for k, val := range s {
			switch k {
			case "type":
				if t, ok := val.(string); ok {
					out["type"] = normalizeSchemaType(t)
					continue
				}
				out["type"] = val
			case "properties":
				if props, ok := val.(map[string]any); ok {
					conv := make(map[string]any, len(props))
					for name, pv := range props {
						if pm, ok := jsonSchemaToJSON(pv); ok {
							conv[name] = pm
						} else {
							conv[name] = map[string]any{}
						}
					}
					out["properties"] = conv
					continue
				}
			case "items", "additionalProperties":
				if m, ok := jsonSchemaToJSON(val); ok {
					out[k] = m
					continue
				}
			case "required":
				// A required list is []string on the typed path and []any on
				// the untyped one; the wire wants []any either way.
				out[k] = stringSliceToAny(val)
				continue
			}
			out[k] = val
		}
		if _, ok := out["type"]; !ok {
			out["type"] = "object"
		}
		return out, true
	case *genai.Schema:
		if s == nil {
			return nil, false
		}
		return genaiSchemaToJSON(s), true
	default:
		// functiontool hands over a *jsonschema.Schema, which is a typed struct
		// rather than a map. Round-tripping it through JSON is the honest way
		// to read it: the struct already marshals to exactly the JSON Schema
		// the wire wants, and handling its fields by hand here would be a
		// second, drifting copy of that type's marshaller.
		return marshalSchema(v)
	}
}

// marshalSchema converts an arbitrary JSON-marshallable schema into the map the
// chat format wants. A value that will not marshal as a schema object is
// reported as unusable so the caller can fall back rather than send garbage.
func marshalSchema(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}
	if b, ok := v.([]byte); ok {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, false
		}
		return jsonSchemaToJSON(m)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false
	}
	return jsonSchemaToJSON(m)
}

// normalizeSchemaType lowercases a schema type and drops the placeholder genai
// uses for "no type". An empty type is left off the wire entirely rather than
// sent as "" or "type_unspecified", both of which endpoints reject.
func normalizeSchemaType(t string) string {
	v := strings.ToLower(strings.TrimSpace(t))
	if v == "type_unspecified" {
		return ""
	}
	return v
}

// stringSliceToAny renders any string-slice shape as []any, which is what
// json.Marshal needs and what the schema type check wants.
func stringSliceToAny(v any) any {
	switch s := v.(type) {
	case []string:
		out := make([]any, len(s))
		for i, e := range s {
			out[i] = e
		}
		return out
	case []any:
		return s
	default:
		return v
	}
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
// When the model ran out of output tokens the arguments are not malformed but
// cut short, and the error has to say that: "unexpected end of JSON input"
// alone sends the user hunting for a bug in dmcode that is not there. That case
// also carries a type, because it is the one malformed-looking failure the
// client may ask about again.
func toolCallsToParts(calls []chatToolCall, finish string, spent int) ([]*genai.Part, error) {
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
			if finish == "length" {
				return nil, &truncatedCallError{tool: tc.Function.Name, spent: spent, err: err}
			}
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
	BaseURL string
	APIKey  string
	name    string
	client  *http.Client
	// reasoningEffort is sent as the OpenAI `reasoning_effort` field when set.
	// Reasoning models burn the output budget on their analysis channel before
	// the tool call starts, which on a capped endpoint truncates the call's
	// arguments mid-JSON; "low" keeps that channel short.
	reasoningEffort string
}

func NewChatModel(BaseURL, APIKey, name string) *chatModel {
	return &chatModel{
		BaseURL: strings.TrimRight(BaseURL, "/"),
		APIKey:  APIKey,
		name:    name,
		client:  config.Client(0),
	}
}

// setReasoningEffort pins the reasoning level for this endpoint. Servers that
// do not know the field drop it silently, so it is only ever set where it has
// been seen to work.
func (m *chatModel) setReasoningEffort(e string) {
	m.reasoningEffort = e
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
	out := chatRequest{Model: name, Messages: msgs, Stream: stream, ReasoningEffort: m.reasoningEffort}
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
	url := m.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Some keyless endpoints (Pollinations) ignore the header but reject a
	// request without one, so a placeholder is always sent.
	if m.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.APIKey)
	} else {
		req.Header.Set("Authorization", "Bearer dmcode")
	}
	return m.client.Do(req)
}

// Why a call failed, in the terms the failover layer needs. The rendered
// message alone cannot tell a rate limit from a malformed request, and the two
// demand opposite reactions: the first is worth retrying on another host, the
// second would fail identically everywhere.
const (
	failTransport = "transport" // never reached the server
	failHTTP      = "http"      // the server answered with a non-2xx status
	failStream    = "stream"    // an error frame arrived mid-response
)

// providerError is a failed call to one endpoint, tagged with enough structure
// to tell a transient failure from a request the provider will never accept.
// The message is exactly what the previous plain error produced, so nothing
// user-visible changes; the type only adds what the classifier needs.
type providerError struct {
	err    error
	status int    // HTTP status, 0 when the failure was not a status reply
	reason string // failTransport, failHTTP or failStream
}

func (e *providerError) Error() string { return e.err.Error() }
func (e *providerError) Unwrap() error { return e.err }

// retryable reports whether another endpoint in the pool stands a chance of
// serving the same request.
//
// The conservative default is yes. Anything that is not clearly about the
// request itself — a 429, a 5xx, a 404 for a model this host does not carry, a
// dropped connection — is a property of the endpoint, not of the conversation,
// and the pool exists precisely to route around it.
//
// The exceptions are the statuses that mean the payload itself is wrong: a
// rejected tool schema or an oversized prompt produces them on every host, so
// walking the pool would only multiply the latency of a failure that is already
// decided.
func (e *providerError) retryable() bool {
	if e.reason == failHTTP {
		switch e.status {
		case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
			return false
		}
	}
	return true
}

// transportError reports an endpoint that could not be reached at all.
func transportError(BaseURL string, err error) error {
	return &providerError{
		err:    fmt.Errorf("dmcode: %s: %w", BaseURL, err),
		reason: failTransport,
	}
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
		return &providerError{
			err:    fmt.Errorf("%s: %s: %s", op, resp.Status, truncate(msg, 200)),
			status: resp.StatusCode,
			reason: failHTTP,
		}
	}
	return &providerError{
		err:    fmt.Errorf("%s: %s", op, resp.Status),
		status: resp.StatusCode,
		reason: failHTTP,
	}
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
			yield(nil, transportError(m.BaseURL, err))
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			yield(nil, httpError(m.BaseURL, resp))
			return
		}

		calls := newToolCallBuffer()
		var text strings.Builder
		var usage *chatUsage
		var modelVer, finish string
		// stopped records that the consumer walked away from the stream. A
		// yield that returns false means "no more", and yielding into it again
		// is not a no-op: a range-over-func panics when the body returns false
		// and the iterator keeps going. The deferred close below is exactly such
		// a later yield, so it has to know the stream was already abandoned.
		stopped := false

		// The turn is closed with one aggregated non-partial event, which is
		// what the ADK runner requires to persist the turn and dispatch tools.
		defer func() {
			if stopped {
				return
			}
			parts, err := toolCallsToParts(calls.snapshot(), finish, completionTokens(usage))
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
				stopped = !yield(nil, &providerError{
					err:    fmt.Errorf("%s: %s", m.BaseURL, chunk.Error.Message),
					reason: failStream,
				})
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
					stopped = true
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			if ctx.Err() != nil {
				return
			}
			yield(nil, transportError(m.BaseURL, fmt.Errorf("обрыв потока: %w", err)))
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
	var call chatCall = m.generate
	if stream {
		call = m.generateStream
	}
	return withReAsk(ctx, body, call)
}

func (m *chatModel) generate(ctx context.Context, body chatRequest) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		resp, err := m.doRequest(ctx, body)
		if err != nil {
			yield(nil, transportError(m.BaseURL, err))
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			yield(nil, httpError(m.BaseURL, resp))
			return
		}
		var parsed chatResponse
		if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
			yield(nil, fmt.Errorf("dmcode: не удалось разобрать ответ %s: %w", m.BaseURL, err))
			return
		}
		if parsed.Error != nil && parsed.Error.Message != "" {
			yield(nil, fmt.Errorf("%s: %s", m.BaseURL, parsed.Error.Message))
			return
		}
		if len(parsed.Choices) == 0 {
			yield(nil, fmt.Errorf("%s: провайдер вернул пустой ответ", m.BaseURL))
			return
		}
		ch := parsed.Choices[0]
		chunks, err := toolCallsToParts(ch.Message.ToolCalls, ch.FinishReason, completionTokens(parsed.Usage))
		if err != nil {
			yield(nil, err)
			return
		}
		if ch.Message.Content != "" {
			chunks = append([]*genai.Part{{Text: ch.Message.Content}}, chunks...)
		}
		if len(chunks) == 0 {
			// A model that answered only with reasoning still completed a turn.
			yield(&model.LLMResponse{
				Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{}},
				FinishReason: genai.FinishReasonStop,
				ModelVersion: parsed.Model,
			}, nil)
			return
		}
		yield(&model.LLMResponse{
			Content:       &genai.Content{Role: genai.RoleModel, Parts: chunks},
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
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
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

// truncate shortens s to at most n runes, marking the cut with an ellipsis.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
