package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFetchURLReturnsHTMLAsText: a page comes back as plain text with its title
// pulled out separately, so the model reads prose rather than markup.
func TestFetchURLReturnsHTMLAsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!doctype html><html><head><title>Go on GitHub</title></head>
			<body><h1>Where Go lives</h1><p>This is the repository.</p></body></html>`))
	}))
	defer srv.Close()

	res, err := fetchURL(nil, fetchURLArgs{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Go on GitHub" {
		t.Errorf("Title = %q, want %q", res.Title, "Go on GitHub")
	}
	if !strings.Contains(res.Content, "Where Go lives") || !strings.Contains(res.Content, "This is the repository.") {
		t.Errorf("Content = %q, want the page's text", res.Content)
	}
	if strings.Contains(res.Content, "<h1>") || strings.Contains(res.Content, "<p>") {
		t.Errorf("markup leaked into Content: %q", res.Content)
	}
	if res.Truncated {
		t.Error("a short page reported truncation")
	}
}

// TestFetchURLNonHTMLIsReturnedAsIs: JSON and plain text are answers already,
// so they are not run through the HTML walker.
func TestFetchURLNonHTMLIsReturnedAsIs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"dmcode","lang":"go"}`))
	}))
	defer srv.Close()

	res, err := fetchURL(nil, fetchURLArgs{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != `{"name":"dmcode","lang":"go"}` {
		t.Errorf("Content = %q, want the raw JSON", res.Content)
	}
}

// TestFetchURLTruncatesToMaxChars: a cap that is smaller than the page must cut
// it and say so — a silent clipping would read as the whole answer.
func TestFetchURLTruncatesToMaxChars(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(strings.Repeat("word ", 1000)))
	}))
	defer srv.Close()

	res, err := fetchURL(nil, fetchURLArgs{URL: srv.URL, MaxChars: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Error("an oversized page did not report truncation")
	}
	if len(res.Content) > 50 {
		t.Errorf("Content is %d chars, over the cap of 50", len(res.Content))
	}
	if !strings.HasSuffix(res.Content, "…") {
		t.Errorf("the truncation is not marked: %q", res.Content)
	}
}

// TestFetchURLRefusesBinary: a response with a NUL in it is not text, and
// handing it to a model as prose would be garbage — the tool says so instead.
func TestFetchURLRefusesBinary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte{0x00, 0x01, 0x02, 0xff, 0x00})
	}))
	defer srv.Close()

	res, err := fetchURL(nil, fetchURLArgs{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if res.Note == "" {
		t.Error("binary content produced no note")
	}
	if res.Content != "" {
		t.Errorf("binary content leaked into Content: %q", res.Content)
	}
}

// TestFetchURLRejectsNonHTTPSchemes: file:// and friends must never reach the
// HTTP client — the boundary around the workspace does not apply here, but the
// *kind* of reach does.
func TestFetchURLRejectsNonHTTPSchemes(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/x", "not a url at all"} {
		if _, err := fetchURL(nil, fetchURLArgs{URL: u}); err == nil {
			t.Errorf("fetch_url accepted %q", u)
		}
	}
}

// TestFetchURLSurfacesTheEndpointStatus: a 404 is a fact about the page, not a
// transport failure — the error names the server's answer.
func TestFetchURLSurfacesTheEndpointStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if _, err := fetchURL(nil, fetchURLArgs{URL: srv.URL}); err == nil {
		t.Error("a 404 page returned no error")
	}
}
