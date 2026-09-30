package config

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// HTTP proxy support.
//
// The variables are the conventional HTTP_PROXY, HTTPS_PROXY and NO_PROXY, so a
// proxy configured for any other tool on the machine is picked up with no extra
// setup. What they do not do on their own is survive a change: Go's
// http.ProxyFromEnvironment reads the environment once, on the first request
// through the default transport, and caches it. Setting a proxy from inside the
// running program would therefore appear to do nothing until a restart — which
// is exactly the kind of setting a user wants to change and see the effect of
// immediately. ProxyFor below is called per request instead, so /proxy applies
// the moment it is set.

// proxyEnvNames are the variables consulted, in the order a value is taken from
// for a plain http request. The lowercase spellings are checked too because
// Go refuses to read HTTP_PROXY from the environment on a CGI request, where a
// client-supplied header would be a way to make the server talk to anything.
// The proxy variables, exported because the UI persists them to .env by name.
const (
	EnvHTTPProxy  = "HTTP_PROXY"
	EnvHTTPSProxy = "HTTPS_PROXY"
	EnvNoProxy    = "NO_PROXY"
)

// ProxyEnvKeys are every variable a proxy can live in, uppercase and lowercase.
// Both spellings are cleared on "off" and both are written on a set: which one
// a shell or an existing .env already uses is not something dmcode gets to
// decide, and leaving a stale lowercase copy behind would let the two disagree.
func ProxyEnvKeys() []string {
	return []string{EnvHTTPProxy, EnvHTTPSProxy, "http_proxy", "https_proxy"}
}

// ProxySettings is the proxy as configured right now, for /proxy to display.
type ProxySettings struct {
	// HTTP is the proxy for plain http requests, HTTPS the one for https, and
	// NoProxy the bypass list. Each is empty when unset.
	HTTP, HTTPS, NoProxy string
	// Active reports whether a proxy is in effect for ordinary provider calls,
	// which are almost always https.
	Active bool
}

// CurrentProxy reports the proxy in effect.
func CurrentProxy() ProxySettings {
	s := ProxySettings{
		HTTP:    firstEnv(EnvHTTPProxy, "http_proxy"),
		HTTPS:   firstEnv(EnvHTTPSProxy, "https_proxy"),
		NoProxy: firstEnv(EnvNoProxy, "no_proxy"),
	}
	s.Active = s.HTTPS != "" || s.HTTP != ""
	return s
}

// ValidateProxy checks that proxy would be accepted as a proxy address without
// changing anything. It is what the /proxy dialog runs while the field is still
// on screen, so a typo is rejected before the wizard closes — the same rules
// SetProxy applies, kept in one place so the two can never disagree.
func ValidateProxy(proxy string) error {
	p := strings.TrimSpace(proxy)
	if p == "" || strings.EqualFold(p, "off") || strings.EqualFold(p, "none") {
		return nil
	}
	if !strings.Contains(p, "://") {
		p = "http://" + p
	}
	u, err := url.Parse(p)
	if err != nil {
		return fmt.Errorf("%s is not a valid proxy URL: %w", proxy, err)
	}
	if u.Host == "" {
		return fmt.Errorf("%s has no host", proxy)
	}
	return nil
}

// SetProxy points the process at proxy, or clears the setting when proxy is
// empty, "off" or "none".
//
// A value with no scheme is assumed to be http, since that is what a bare
// "host:port" almost always means and refusing it would be pedantry. The URL is
// parsed before anything is set: a typo must not leave the program half
// configured, pointing at a proxy that does not exist.
func SetProxy(proxy string) error {
	if err := ValidateProxy(proxy); err != nil {
		return err
	}
	p := strings.TrimSpace(proxy)
	if p == "" || strings.EqualFold(p, "off") || strings.EqualFold(p, "none") {
		for _, k := range []string{EnvHTTPProxy, EnvHTTPSProxy, "http_proxy", "https_proxy"} {
			os.Unsetenv(k)
		}
		return nil
	}
	if !strings.Contains(p, "://") {
		p = "http://" + p
	}
	u, _ := url.Parse(p)
	for _, k := range []string{EnvHTTPProxy, EnvHTTPSProxy, "http_proxy", "https_proxy"} {
		if err := os.Setenv(k, u.String()); err != nil {
			return err
		}
	}
	return nil
}

// SetNoProxy replaces the bypass list.
func SetNoProxy(list string) error {
	list = strings.TrimSpace(list)
	if list == "" || strings.EqualFold(list, "off") || strings.EqualFold(list, "none") {
		return os.Unsetenv(EnvNoProxy)
	}
	return os.Setenv(EnvNoProxy, list)
}

// ProxyFor is the per-request proxy decision, handed to an http.Transport. It
// reads the environment each time rather than once, so a /proxy change takes
// effect on the next request rather than the next start.
func ProxyFor(req *http.Request) (*url.URL, error) {
	s := CurrentProxy()
	raw := s.HTTP
	if req != nil && req.URL != nil && req.URL.Scheme == "https" && s.HTTPS != "" {
		raw = s.HTTPS
	}
	if raw == "" {
		return nil, nil
	}
	if bypassed(req, s.NoProxy) {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		// A proxy that cannot be parsed is a configuration error, and returning
		// it makes the request fail with something readable instead of silently
		// going direct — which would send the user's traffic somewhere they did
		// not ask for.
		return nil, fmt.Errorf("proxy %q is not a valid URL: %w", raw, err)
	}
	return u, nil
}

// bypassed reports whether req should skip the proxy.
//
// The list is comma separated. An entry matches the request host when it is
// equal to it, when it is a parent domain ("example.com" covers
// "api.example.com"), or when it names the port. "*" bypasses everything, and
// localhost is never proxied: a proxy that cannot reach the machine's own
// services is a common enough setup that following it would break local
// providers that work perfectly well.
func bypassed(req *http.Request, list string) bool {
	if req == nil || req.URL == nil {
		return false
	}
	host := req.URL.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	for _, entry := range strings.Split(list, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if entry == "*" {
			return true
		}
		entry = strings.TrimPrefix(entry, ".")
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

// Transport is the http.Transport every outbound request uses. It is a clone of
// the default with the proxy decision replaced by ProxyFor, so timeouts, idle
// connection pooling and HTTP/2 negotiation are all inherited rather than
// reimplemented.
func Transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = ProxyFor
	return t
}

// Client is an http.Client that honours the proxy. timeout of zero means no
// limit, which is what a streaming model call wants.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: Transport()}
}

// firstEnv returns the first non-empty value among names.
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
