package llm

import (
	"context"
	"fmt"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/openaimodel"

	"github.com/dedomorozoff/dmcode/internal/config"
)

// buildLLM creates the client for one provider, picking the wire protocol the
// endpoint actually speaks. Each member of the failover pool gets its own, so a
// responses-wire provider and a chat-wire one can sit in the same session.
func BuildLLM(ctx context.Context, p config.Provider) (model.LLM, error) {
	if p.Wire() == config.APIChat {
		cm := NewChatModel(p.BaseURL, p.APIKey, p.Model)
		cm.setReasoningEffort(p.Reasoning)
		return cm, nil
	}
	om, err := openaimodel.NewModel(ctx, p.Model, &openaimodel.ClientConfig{
		APIKey:  p.APIKey,
		BaseURL: p.BaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create openai-compatible Model: %w", err)
	}
	return om, nil
}
