package tools

import (
	"os"
	"testing"
)

// TestLiveWebSearch hits the real search endpoint, so it is skipped unless
// DMCODE_LIVE_SEARCH is set: a unit suite must not depend on the network.
//
//	go test ./internal/tools -run TestLiveWebSearch -v
func TestLiveWebSearch(t *testing.T) {
	if os.Getenv("DMCODE_LIVE_SEARCH") == "" {
		t.Skip("set DMCODE_LIVE_SEARCH=1 to run against the real search engine")
	}
	res, err := webSearch(nil, searchArgs{Query: "golang error wrapping", MaxResults: 3})
	if err != nil {
		t.Fatalf("webSearch: %v", err)
	}
	if res.Note != "" {
		t.Logf("note: %s", res.Note)
	}
	if len(res.Hits) == 0 {
		t.Fatal("no hits returned")
	}
	for _, h := range res.Hits {
		t.Logf("- %s\n  %s\n  %s", h.Title, h.URL, h.Snippet)
	}
	// A hit without a URL is not a hit: the agent cannot read it.
	for i, h := range res.Hits {
		if h.URL == "" || h.Title == "" {
			t.Errorf("hit %d has no url or title: %+v", i, h)
		}
	}
}
