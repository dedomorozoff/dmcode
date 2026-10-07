// fetchurl.go — the fetch_url instrument.
//
// web_search returns titles, URLs and snippets; fetch_url is the second half of
// that answer: it opens a URL the search found (or one the agent was given) and
// hands back the page as text. It is read-only, like web_search, and it is the
// one other instrument that deliberately reaches past the workspace boundary —
// the alternative is a model that has a link it cannot follow, which is what
// plan mode used to offer: search, then nothing.
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

// Bounds on one fetch. A page is not an answer either: an unbounded body is a
// context window with a hole in it, which is the same reasoning behind the file
// tools' caps. The byte bound keeps the network read sane; the char bound keeps
// what actually reaches the model sane, and the two are different questions.
const (
	fetchTimeout      = 20 * time.Second
	defaultFetchChars = 20000
	maxFetchChars     = 100000
	maxFetchBytes     = 4 << 20
)

type fetchURLArgs struct {
	URL string `json:"url"`
	// MaxChars caps the returned text. Twenty thousand characters is already a
	// long page; a model rarely needs all of one.
	MaxChars int `json:"max_chars,omitempty"`
}

type fetchURLResult struct {
	URL         string `json:"url"`
	Content     string `json:"content"`
	Title       string `json:"title,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	// Truncated reports that Content was cut at the char cap, so the model
	// knows the rest of the page exists rather than reading a clipping as a
	// complete answer.
	Truncated bool   `json:"truncated"`
	Note      string `json:"note,omitempty"`
}

func fetchURL(ctx agent.Context, in fetchURLArgs) (fetchURLResult, error) {
	raw := strings.TrimSpace(in.URL)
	if raw == "" {
		return fetchURLResult{}, fmt.Errorf("the url is empty — say what to fetch")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fetchURLResult{}, fmt.Errorf("the url does not parse: %w", err)
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fetchURLResult{}, fmt.Errorf("only http(s) URLs can be fetched, got %q", raw)
	}
	cap := in.MaxChars
	if cap <= 0 {
		cap = defaultFetchChars
	}
	if cap > maxFetchChars {
		cap = maxFetchChars
	}

	// The same context dance web_search keeps: the turn's context carries
	// cancellation, but the tool may be called with none (a test), so there is
	// a fallback.
	parentCtx := context.Background()
	if ctx != nil {
		parentCtx = ctx
	}
	reqCtx, cancel := context.WithTimeout(parentCtx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fetchURLResult{}, err
	}
	req.Header.Set("User-Agent", searchUserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fetchURLResult{}, fmt.Errorf("the request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fetchURLResult{}, fmt.Errorf("%s answered %s", u.Host, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return fetchURLResult{}, fmt.Errorf("could not read the page: %w", err)
	}

	ct := resp.Header.Get("Content-Type")
	content, title, truncated, note := pageText(body, ct, cap)
	return fetchURLResult{
		URL:         u.String(),
		Content:     content,
		Title:       title,
		ContentType: ct,
		Truncated:   truncated,
		Note:        note,
	}, nil
}

// pageText turns a response body into the text a model can read.
//
// HTML becomes plain text, with the title pulled out separately; everything
// else — JSON, XML, a README served raw — is handed back as it is. Binary
// content is refused with a note rather than decoded into a string of garbage:
// a NUL in the first bytes is the same tell grep uses for "not a text file".
func pageText(body []byte, ct string, cap int) (content, title string, truncated bool, note string) {
	if strings.ContainsRune(string(body[:min(len(body), 4096)]), 0) {
		return "", "", false, "the response is binary and was not read"
	}
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml") {
		text, ttl := htmlToText(string(body))
		if strings.TrimSpace(text) == "" {
			return "", ttl, false, "the page has no readable text"
		}
		out, truncated := clipText(text, cap)
		return out, ttl, truncated, ""
	}
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return "", "", false, "the response has no readable text"
	}
	out, truncated := clipText(raw, cap)
	return out, "", truncated, ""
}

// htmlToText walks a page the way a browser reads it and keeps the words, the
// block boundaries and the title. Block elements emit a newline before and
// after, which collapseWS folds into paragraph breaks; script, style and the
// template body never reach the output, and the title is pulled out of the
// head rather than printed with it.
func htmlToText(page string) (text, title string) {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return "", ""
	}
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "template":
				return
			case "title":
				if title == "" {
					title = strings.TrimSpace(nodeText(n))
				}
				return
			case "br", "hr":
				b.WriteByte('\n')
				return
			}
			if blockTags[n.Data] {
				b.WriteByte('\n')
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && blockTags[n.Data] {
			b.WriteByte('\n')
		}
	}
	walk(doc)
	return collapseWS(b.String()), title
}

// blockTags are the elements whose end is a paragraph boundary in prose.
var blockTags = map[string]bool{
	"p": true, "div": true, "li": true, "ul": true, "ol": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"pre": true, "blockquote": true, "section": true, "article": true,
	"header": true, "footer": true, "table": true, "tr": true, "td": true, "th": true,
}

// collapseWS folds every run of whitespace — the markup's own line breaks and
// indentation included — into single spaces, so the text reads as paragraphs
// rather than as the page's source layout.
func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clipText cuts s at cap chars, marking the cut with an ellipsis that counts
// inside the cap, and reports whether a cut happened.
func clipText(s string, cap int) (string, bool) {
	if len(s) <= cap {
		return s, false
	}
	cut := cap - len("…")
	if cut < 1 {
		cut = 1
	}
	return s[:cut] + "…", true
}

// MakeFetchURLTool builds the fetch_url instrument.
func MakeFetchURLTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "fetch_url",
		Description: "Fetches an http(s) URL and returns the page as plain text (HTML tags stripped), " +
			"with the title when there is one. Use it to open a page web_search found, an API endpoint, " +
			"or documentation. max_chars caps the returned text (default 20000, max 100000). " +
			"It reads, never writes.",
	}, fetchURL)
}
