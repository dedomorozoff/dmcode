package config

import "testing"

func TestProviderWireDefaultsToResponses(t *testing.T) {
	if got := (Provider{}).Wire(); got != APIResponses {
		t.Errorf("empty provider should default to responses, got %q", got)
	}
	if got := (Provider{API: APIChat}).Wire(); got != APIChat {
		t.Errorf("explicit chat wire lost, got %q", got)
	}
}
