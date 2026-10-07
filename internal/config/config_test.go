package config

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The window is what the context meter and the compaction threshold are both
// fractions of, so both of its edges matter: a known model and an unknown one.
func TestContextWindow(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  int
	}{
		{"gpt-4o", 128_000},
		{"gpt-4o-mini", 128_000},
		{"claude-sonnet-4", 200_000},
		{"llama3.1:8b", 32_768},
		// dmcode's anonymous hosted tier answers with GPT-OSS, which has a
		// known 128k window — recognising it turns on compaction that the guess
		// (and the "~" the meter would otherwise show) denied it.
		{"gpt-oss-20b", 128_000},
		// Not recognised: zero says so, and every caller shows a bare token
		// count rather than dividing by a number it made up.
		{"some-finetune-abc", 0},
		{"", 0},
	} {
		if got := ContextWindow(tc.model); got != tc.want {
			t.Errorf("ContextWindow(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}

	// The override beats the table: it exists for a model the heuristic has
	// wrong, so a correct guess must not be able to overrule the user.
	t.Setenv("DMCODE_CONTEXT", "4096")
	if got := ContextWindow("gpt-4o"); got != 4096 {
		t.Errorf("DMCODE_CONTEXT was ignored: got %d", got)
	}
	// A malformed override is no override — the table still answers, rather
	// than the model reporting a window of zero.
	t.Setenv("DMCODE_CONTEXT", "not a number")
	if got := ContextWindow("gpt-4o"); got != 128_000 {
		t.Errorf("a malformed override fell through to %d instead of the table", got)
	}
}

// The endpoint-reported window wins over the table: it is the server's own
// answer, which knows its actual cap where a name-to-tokens guess cannot.
func TestWindowForPrefersTheEndpoint(t *testing.T) {
	if got := WindowFor(Provider{Model: "gpt-4o"}); got != 128_000 {
		t.Errorf("table fallback gave %d, want 128000", got)
	}
	if got := WindowFor(Provider{Model: "gpt-4o", Context: 4096}); got != 4096 {
		t.Errorf("endpoint figure lost to the table: got %d", got)
	}
	// An endpoint number for a model the table does not know is still trusted:
	// it did not come from a guess.
	if got := WindowFor(Provider{Model: "madeup/whatever", Context: 131_072}); got != 131_072 {
		t.Errorf("endpoint figure for an unknown model lost: got %d", got)
	}
}

func TestWindowForDisplayIsExactWhenTheEndpointSpeaks(t *testing.T) {
	win, approx := WindowForDisplay(Provider{Model: "gpt-4o", Context: 131_072})
	if win != 131_072 || approx {
		t.Errorf("WindowForDisplay = (%d, %v), want (131072, false)", win, approx)
	}
	win, approx = WindowForDisplay(Provider{Model: "madeup/whatever"})
	if win != DefaultContextWindow || !approx {
		t.Errorf("WindowForDisplay for an unknown silent model = (%d, %v), want (%d, true)", win, approx, DefaultContextWindow)
	}
}

// ModelContext asks the server that enforces the limit. A server that reports
// max_model_len on /v1/models is answered; one that does not yields zero.
func TestModelContextReadsTheServersOwnFigure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"big-model","max_model_len":262144},{"id":"plain"}]}`)
	}))
	defer srv.Close()

	if got := ModelContext(srv.URL, "", "big-model"); got != 262_144 {
		t.Errorf("ModelContext(big-model) = %d, want 262144", got)
	}
	if got := ModelContext(srv.URL, "", "plain"); got != 0 {
		t.Errorf("ModelContext(plain) = %d, want 0 (no window stated)", got)
	}
	if got := ModelContext(srv.URL, "", "missing"); got != 0 {
		t.Errorf("ModelContext(missing) = %d, want 0", got)
	}
}

func TestProviderWireDefaultsToResponses(t *testing.T) {
	if got := (Provider{}).Wire(); got != APIResponses {
		t.Errorf("empty provider should default to responses, got %q", got)
	}
	if got := (Provider{API: APIChat}).Wire(); got != APIChat {
		t.Errorf("explicit chat wire lost, got %q", got)
	}
}
