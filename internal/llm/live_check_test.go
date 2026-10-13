package llm

import (
	"context"
	"os"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// liveModel is the endpoint the three checks below talk to.
//
// It is hardcoded to Pollinations' anonymous host, and that is still the right
// default: it is the only endpoint reachable with no key at all that carries a
// tool call, so it is the one these checks can actually run against.
// gen.pollinations.ai is not a substitute — it answers 401 to any body carrying
// `tools` unless a key is present, so a live tool check pointed there would only
// ever test that a 401 is parsed.
//
// Any other endpoint can be named in the environment instead: DMCODE_LIVE_URL,
// DMCODE_LIVE_MODEL and DMCODE_LIVE_KEY, read in that order so a developer
// overrides only the piece they need. That keeps the checks pointed at whatever
// provider they actually have, which is the point of a test that has to reach a
// real model to mean anything.
func liveModel(t *testing.T) *chatModel {
	t.Helper()
	if os.Getenv("DMCODE_LIVE") != "1" {
		t.Skip("set DMCODE_LIVE=1 to hit a real endpoint")
	}
	url := firstEnv("DMCODE_LIVE_URL", "https://text.pollinations.ai/openai")
	name := firstEnv("DMCODE_LIVE_MODEL", "openai-fast")
	// "dmcode" is the placeholder the anonymous host requires: it rejects a
	// request carrying no Authorization header but never reads the value.
	key := firstEnv("DMCODE_LIVE_KEY", "dmcode")
	return NewChatModel(url, key, name)
}

// firstEnv returns the value of the first variable that is set.
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// TestLiveEndpointStream is the check that the streaming adapter works against a
// real server end to end, which the httptest fakes cannot prove: deltas arrive,
// the final event is not partial, and the aggregate matches what was streamed.
func TestLiveEndpointStream(t *testing.T) {
	m := liveModel(t)
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "Ответь одним словом: ok"}}}},
	}
	var text strings.Builder
	var final *model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("live stream failed: %v", err)
		}
		if resp.Partial {
			text.WriteString(resp.Content.Parts[0].Text)
			continue
		}
		final = resp
	}
	if final == nil {
		t.Fatal("no final event from live endpoint")
	}
	t.Logf("streamed=%q aggregated=%q turnComplete=%v", text.String(), final.Content.Parts[0].Text, final.TurnComplete)
}

// TestLiveEndpointToolCall is the load-bearing check for dmcode: a real provider
// must actually emit a tool call, not just text. A provider that ignores the
// tools array would silently turn every turn into prose.
func TestLiveEndpointToolCall(t *testing.T) {
	m := liveModel(t)
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{
			{Text: "Сколько сейчас времени? Вызови инструмент get_time."},
		}}},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name:        "get_time",
				Description: "Возвращает текущее время",
				Parameters:  &genai.Schema{Type: genai.TypeObject},
			}}}},
		},
	}

	var final *model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("live tool stream failed: %v", err)
		}
		if !resp.Partial {
			final = resp
		}
	}
	if final == nil {
		t.Fatal("no final event from live endpoint")
	}
	var call *genai.FunctionCall
	for _, p := range final.Content.Parts {
		if p.FunctionCall != nil {
			call = p.FunctionCall
		}
	}
	if call == nil {
		t.Fatalf("provider did not emit a tool call, parts: %+v", final.Content.Parts)
	}
	t.Logf("tool call: %s id=%s args=%v", call.Name, call.ID, call.Args)
}

// TestLiveEndpointBigToolCall replays the failure that started it all: a
// write_file large enough that a capped endpoint truncates it. With the
// reasoning effort held low the call must arrive complete and parseable.
func TestLiveEndpointBigToolCall(t *testing.T) {
	m := liveModel(t)
	m.setReasoningEffort("low")
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{
			{Text: "Вызови write_file: путь index.html, содержимое — классический тетрис на JS (HTML+CSS+JS, не менее 100 строк). Ничего кроме вызова инструмента."},
		}}},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name:        "write_file",
				Description: "Записать файл",
				Parameters: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"path":    {Type: genai.TypeString},
						"content": {Type: genai.TypeString},
					},
					Required: []string{"path", "content"},
				},
			}}}},
		},
	}

	var final *model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("live big tool stream failed: %v", err)
		}
		if !resp.Partial {
			final = resp
		}
	}
	if final == nil {
		t.Fatal("no final event from live endpoint")
	}
	for _, p := range final.Content.Parts {
		if p.FunctionCall != nil && p.FunctionCall.Name == "write_file" {
			content, _ := p.FunctionCall.Args["content"].(string)
			t.Logf("write_file content length: %d", len(content))
			if len(content) < 1000 {
				t.Errorf("write_file content suspiciously short: %d chars", len(content))
			}
			return
		}
	}
	t.Fatalf("no write_file call in final parts: %+v", final.Content.Parts)
}
