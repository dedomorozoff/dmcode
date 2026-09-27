package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// TestLiveKeylessEndpoint talks to a real keyless provider, so it is opt-in:
// set DMCODE_LIVE=1 to run it. It is the check that the zero-config path
// end-to-end still works, which the httptest fakes cannot prove.
func TestLiveKeylessEndpoint(t *testing.T) {
	if os.Getenv("DMCODE_LIVE") != "1" {
		t.Skip("set DMCODE_LIVE=1 to hit a real endpoint")
	}
	m := newChatModel("https://text.pollinations.ai/openai", "dmcode", "openai-fast")
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

// TestLiveKeylessToolCall is the load-bearing check for dmcode: a free keyless
// provider must actually emit a tool call, not just text. A provider that
// ignores the tools array would silently turn every turn into prose.
func TestLiveKeylessToolCall(t *testing.T) {
	if os.Getenv("DMCODE_LIVE") != "1" {
		t.Skip("set DMCODE_LIVE=1 to hit a real endpoint")
	}
	m := newChatModel("https://text.pollinations.ai/openai", "dmcode", "openai-fast")
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
		t.Fatalf("free provider did not emit a tool call, parts: %+v", final.Content.Parts)
	}
	t.Logf("tool call: %s id=%s args=%v", call.Name, call.ID, call.Args)
}
