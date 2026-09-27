package discover

import (
	"context"

	"dmcode/internal/llm"
)

// FreeBackups is the lazy half of the failover pool: the endpoints to fall back
// on once every configured one has failed. It runs the probe inside the failing
// turn rather than at startup, because a user whose own key works must not wait
// for a scan of candidates they will never need.
func FreeBackups(ctx context.Context) ([]llm.PoolMember, error) {
	provs := DiscoverFreeProviders()
	members := make([]llm.PoolMember, 0, len(provs))
	for _, p := range provs {
		client, err := llm.BuildLLM(ctx, p)
		if err != nil {
			// One unusable endpoint is not a reason to give up on the others.
			continue
		}
		members = append(members, llm.PoolMember{Prov: p, LLM: client})
	}
	return members, nil
}
