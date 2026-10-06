package config

import "testing"

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

func TestProviderWireDefaultsToResponses(t *testing.T) {
	if got := (Provider{}).Wire(); got != APIResponses {
		t.Errorf("empty provider should default to responses, got %q", got)
	}
	if got := (Provider{API: APIChat}).Wire(); got != APIChat {
		t.Errorf("explicit chat wire lost, got %q", got)
	}
}
