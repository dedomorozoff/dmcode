package tools

import (
	"strings"
	"testing"
)

// A canned DuckDuckGo HTML page, abbreviated to the parts the parser reads.
// The real page wraps each result the same way: a result__a anchor routed
// through /l/?uddg=, then a result__snippet.
const ddgPage = `<!DOCTYPE html><html><body>
<div class="result">
  <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fdocs&amp;rut=aa">Example <b>Docs</b></a>
  <a class="result__snippet">The <b>docs</b> for   everything.</a>
</div>
<div class="result">
  <a rel="nofollow" class="result__a" href="https://direct.example.org/page">Direct Link</a>
  <a class="result__snippet">Second   result here.</a>
</div>
<div class="result">
  <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fthird.example%2F&amp;rut=cc">Third</a>
</div>
</body></html>`

func TestParseDDGResults(t *testing.T) {
	hits := parseDDGResults(ddgPage, 10)
	if len(hits) != 3 {
		t.Fatalf("got %d hits, want 3: %+v", len(hits), hits)
	}

	first := hits[0]
	if first.URL != "https://example.com/docs" {
		t.Errorf("first hit url = %q, want the decoded uddg target", first.URL)
	}
	if first.Title != "Example Docs" {
		t.Errorf("first hit title = %q, want markup stripped", first.Title)
	}
	if first.Snippet != "The docs for everything." {
		t.Errorf("first hit snippet = %q, want whitespace folded", first.Snippet)
	}

	if hits[1].URL != "https://direct.example.org/page" {
		t.Errorf("second hit url = %q, want a direct href passed through", hits[1].URL)
	}
	if hits[2].Snippet != "" {
		t.Errorf("third hit snippet = %q, want empty when the page has none", hits[2].Snippet)
	}
}

func TestParseDDGResultsRespectsLimit(t *testing.T) {
	hits := parseDDGResults(ddgPage, 2)
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
}

func TestParseDDGResultsOnGarbage(t *testing.T) {
	for _, page := range []string{"", "<html><body>blocked</body></html>"} {
		if hits := parseDDGResults(page, 5); len(hits) != 0 {
			t.Errorf("parseDDGResults(%q) = %+v, want no hits", page, hits)
		}
	}
}

func TestClipSnippetCapsLength(t *testing.T) {
	long := strings.Repeat("word ", 100)
	got := clipSnippet(long)
	if len(got) > maxSnippetChars {
		t.Errorf("snippet is %d chars, want at most %d", len(got), maxSnippetChars)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("snippet = %q, want an ellipsis marking the cut", got)
	}
}
