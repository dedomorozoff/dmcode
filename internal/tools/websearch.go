// websearch.go — the web_search instrument. The agent works on a local
// workspace, but answers often live outside it: an error message from a
// library, an API that changed, a version bump. One read-only HTTP tool keeps
// that reach without giving the model a browser.
package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// Bounds on one search. The web returns pages, not answers, and an unbounded
// fetch is a context window with a hole in it — the same reason the file tools
// cap their reads.
const (
	searchTimeout   = 20 * time.Second
	searchUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) dmcode/1.0"
	defaultResults  = 5
	maxResults      = 10
	maxSnippetChars = 300
)

type searchArgs struct {
	Query string `json:"query"`
	// MaxResults is how many hits to return. Five is usually enough; more than
	// ten and the page stops being an answer.
	MaxResults int `json:"max_results,omitempty"`
}

type searchHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type searchResult struct {
	Query string      `json:"query"`
	Hits  []searchHit `json:"hits"`
	// Note is why there is nothing to read, when there is not. An empty hits
	// list with an empty note would read as "the web has no opinion", which is
	// a claim.
	Note string `json:"note,omitempty"`
}

// searchEndpoint is DuckDuckGo's HTML endpoint: no key, no JavaScript, and a
// result list stable enough to parse. It is a var so a test can point it at a
// canned page.
var searchEndpoint = "https://html.duckduckgo.com/html/?q="

func webSearch(ctx agent.Context, in searchArgs) (searchResult, error) {
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return searchResult{}, fmt.Errorf("the query is empty — say what to search for")
	}
	want := in.MaxResults
	if want <= 0 {
		want = defaultResults
	}
	if want > maxResults {
		want = maxResults
	}

	// The turn's context carries cancellation, but the tool may also be
	// called with none — the live test does — so there is a fallback, the
	// same one run_command keeps.
	parentCtx := context.Background()
	if ctx != nil {
		parentCtx = ctx
	}
	reqCtx, cancel := context.WithTimeout(parentCtx, searchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet,
		searchEndpoint+url.QueryEscape(query), nil)
	if err != nil {
		return searchResult{}, err
	}
	req.Header.Set("User-Agent", searchUserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return searchResult{Query: query}, fmt.Errorf("the search request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return searchResult{Query: query}, fmt.Errorf("the search engine answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return searchResult{Query: query}, fmt.Errorf("could not read the search results: %w", err)
	}

	hits := parseDDGResults(string(body), want)
	res := searchResult{Query: query, Hits: hits}
	if len(hits) == 0 {
		res.Note = "the search engine returned no usable results — try different wording, or check whether the network is reachable"
	}
	return res, nil
}

// parseDDGResults walks the HTML page the way a browser would and keeps the
// result anchors. The markup is DuckDuckGo's, not a contract, so every step
// tolerates the element being missing: a page that changed shape returns no
// hits and the note, not a parse error.
func parseDDGResults(page string, want int) []searchHit {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return nil
	}
	var hits []searchHit
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Data == "a" && n.Type == html.ElementNode {
			class := attrOr(n, "class")
			switch {
			case strings.Contains(class, "result__a"):
				link := ddgLink(attrOr(n, "href"))
				title := nodeText(n)
				if link != "" && title != "" {
					hits = append(hits, searchHit{Title: title, URL: link})
					if len(hits) >= want {
						return
					}
				}
			case strings.Contains(class, "result__snippet"):
				// The snippet belongs to the last result whose link came
				// first; the page renders one per result.
				if len(hits) > 0 && hits[len(hits)-1].Snippet == "" {
					hits[len(hits)-1].Snippet = clipSnippet(nodeText(n))
				}
			}
		}
		if len(hits) >= want {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
			if len(hits) >= want {
				return
			}
		}
	}
	walk(doc)
	return hits
}

// ddgLink unwraps a result href. The HTML endpoint routes clicks through
// /l/?uddg=<encoded-url>, so the real address is a parameter of a duckduckgo
// one; a direct href passes through untouched.
func ddgLink(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if u, err := url.Parse(href); err == nil && (u.Path == "/l" || u.Path == "/l/") {
		if target := u.Query().Get("uddg"); target != "" {
			return target
		}
	}
	return href
}

// clipSnippet folds the snippet text to one line and caps it. Whitespace in
// the markup is arbitrary; in a result list it is noise. The cap counts the
// ellipsis in: a snippet longer than the cap would be its own problem.
func clipSnippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxSnippetChars {
		s = s[:maxSnippetChars-len("…")] + "…"
	}
	return s
}

// attrOr reads an attribute off an element node, or "" when it has none.
func attrOr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// nodeText collects an element's text, ignoring the markup inside it.
func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// MakeWebSearchTool builds the web_search instrument.
func MakeWebSearchTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "web_search",
		Description: "Searches the web and returns titles, URLs and snippets. Use it for " +
			"things the local workspace cannot answer: library error messages, current API " +
			"documentation, release notes, version compatibility. It reads, never writes. " +
			"max_results defaults to 5 and is capped at 10.",
	}, webSearch)
}
