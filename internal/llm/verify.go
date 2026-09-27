package llm

import (
	"context"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// ToolProbeTimeout bounds the tool-calling check. The anonymous Pollinations
// backend cold-starts its model, and the first request after an idle spell has
// been observed taking ~18s, so this has to be generous. It is only ever paid
// once per run, in the background, and the UI stays usable meanwhile.
const ToolProbeTimeout = 45 * time.Second

// VerifyTools asks a candidate to call a dummy tool and reports whether it
// complied. Answering /models proves nothing about tool support, and a host
// that ignores the tools array turns every single turn into prose — the user
// only finds out after typing a real task, when dmcode is already useless.
//
// conclusive is false when the check could not reach a verdict (transport
// failure, timeout, truncated stream). Callers must not treat that as a
// failure: a slow endpoint says nothing about its tool support.
func VerifyTools(BaseURL, APIKey, modelName string, timeout time.Duration) (ok, conclusive bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{
			{Text: "Вызови инструмент probe_ok, ничего больше не нужно."},
		}}},
		Config: &genai.GenerateContentConfig{
			MaxOutputTokens: 64,
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name:        "probe_ok",
				Description: "Проверочный инструмент. Вызови его.",
				Parameters:  &genai.Schema{Type: genai.TypeObject},
			}}}},
		},
	}

	// The real client is used on purpose, not a hand-rolled request: keyless
	// hosts (Pollinations) answer 404 to a non-streaming call, so a probe that
	// skipped the adapter would reject a Provider that works perfectly.
	var final *model.LLMResponse
	for resp, err := range NewChatModel(BaseURL, APIKey, modelName).GenerateContent(ctx, req, true) {
		if err != nil {
			return false, false
		}
		if !resp.Partial {
			final = resp
		}
	}
	if final == nil {
		return false, false
	}
	for _, p := range final.Content.Parts {
		if p != nil && p.FunctionCall != nil && p.FunctionCall.Name == "probe_ok" {
			return true, true
		}
	}
	// A complete answer that contains no call is a real "no".
	return false, true
}
