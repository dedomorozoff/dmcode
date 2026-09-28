package config

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"
)

// clearProxyEnv puts the environment back to "no proxy" for one test. The
// variables are process-global, so a test that left one set would silently give
// the next one a proxy it never asked for.
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range append(ProxyEnvKeys(), EnvNoProxy, "no_proxy") {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func TestSetProxyAcceptsABareHostAndPort(t *testing.T) {
	clearProxyEnv(t)
	// "host:port" is what everyone actually types, and refusing it as a URL would
	// be pedantry rather than safety.
	if err := SetProxy("127.0.0.1:8080"); err != nil {
		t.Fatalf("SetProxy: %v", err)
	}
	s := CurrentProxy()
	if s.HTTPS != "http://127.0.0.1:8080" || s.HTTP != "http://127.0.0.1:8080" {
		t.Errorf("proxy = %+v, want the bare host given an http scheme", s)
	}
	if !s.Active {
		t.Error("a set proxy reports itself inactive")
	}
}

func TestSetProxyRejectsRubbishWithoutTouchingTheEnvironment(t *testing.T) {
	clearProxyEnv(t)
	if err := SetProxy("http://127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
	// A typo must not leave the session half configured: the working proxy stays
	// until something valid replaces it.
	if err := SetProxy("http://%zz"); err == nil {
		t.Fatal("a malformed URL was accepted")
	}
	if s := CurrentProxy(); s.HTTPS != "http://127.0.0.1:8080" {
		t.Errorf("a rejected URL disturbed the working proxy: %q", s.HTTPS)
	}
}

func TestSetProxyOffClearsBothSpellings(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("http_proxy", "http://stale:1")
	if err := SetProxy("http://127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
	if err := SetProxy("off"); err != nil {
		t.Fatal(err)
	}
	for _, k := range ProxyEnvKeys() {
		if v := os.Getenv(k); v != "" {
			t.Errorf("%s survived /proxy off with %q", k, v)
		}
	}
	if CurrentProxy().Active {
		t.Error("the proxy still reports itself active after off")
	}
}

func TestNoProxyBypasses(t *testing.T) {
	clearProxyEnv(t)
	req := func(host string) *http.Request {
		u, _ := url.Parse("https://" + host + "/models")
		r, _ := http.NewRequest(http.MethodGet, u.String(), nil)
		return r
	}
	for _, tc := range []struct {
		list string
		host string
		want bool
		why  string
	}{
		{"", "api.example.com", false, "no list means nothing is bypassed"},
		{"example.com", "api.example.com", true, "a parent domain covers its subdomains"},
		{"api.example.com", "api.example.com", true, "an exact match"},
		{"other.com", "api.example.com", false, "an unrelated entry"},
		{"*", "api.example.com", true, "a star bypasses everything"},
		{"", "localhost", true, "localhost is never proxied"},
		{"", "127.0.0.1", true, "the loopback address is never proxied"},
		{" example.com , foo.io ", "api.example.com", true, "spaces around entries are ignored"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			if got := bypassed(req(tc.host), tc.list); got != tc.want {
				t.Errorf("bypassed(%q, %q) = %v, want %v", tc.host, tc.list, got, tc.want)
			}
		})
	}
}

// The end-to-end one: a request really goes through the proxy, and really stops
// going through it once the proxy is cleared. A test that only inspected the
// returned *url.URL would pass even if the transport never used it.
//
// The target is a plain http URL on purpose. An https request through a proxy is
// tunnelled with CONNECT and needs a proxy that speaks TLS, which is a much
// larger thing to stand up in a test; the transport's proxy hook is the same
// either way, and the https path is the one the bypass list is unit-tested for.
func TestClientRoutesThroughTheProxy(t *testing.T) {
	clearProxyEnv(t)

	var proxied int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied++
		w.Write([]byte(`{"data":[{"id":"via-proxy"}]}`))
	}))
	defer proxy.Close()

	// A host that cannot resolve: anything that answers came back through the
	// proxy, and a failure is proof the request was sent directly.
	const target = "http://provider.invalid/v1"

	if err := SetProxy(proxy.URL); err != nil {
		t.Fatal(err)
	}
	models, err := ListModels(Provider{BaseURL: target})
	if err != nil {
		t.Fatalf("ListModels through the proxy: %v", err)
	}
	if len(models) != 1 || models[0] != "via-proxy" {
		t.Errorf("models = %v, want the proxy's answer", models)
	}
	if proxied == 0 {
		t.Error("the request did not reach the proxy")
	}

	// The same client, built once, used across a proxy change: the shape of a
	// running session, where /proxy changes the setting under a live client.
	client := Client(2 * time.Second)
	if err := SetProxy("off"); err != nil {
		t.Fatal(err)
	}
	// Now the request must try to reach the host itself. It will fail — the host
	// does not exist — and that failure is the point: it is what a direct
	// connection looks like, as against a request the proxy answered.
	if _, err := client.Get(target + "/models"); err == nil {
		t.Error("after /proxy off the request still reached something through a proxy")
	}
	if proxied != 1 {
		t.Errorf("the proxy saw %d requests, want only the one from before the change", proxied)
	}
}
