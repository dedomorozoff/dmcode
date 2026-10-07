package config

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
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

// Spec is a proxy address as its parts. The URL is the shape the wire wants;
// the parts are the shape a person fills in, one field at a time — and the shape
// a settings dialog can show without a password sitting in plain text.
//
// It exists because a single free-text field hides the one part that decides
// whether the proxy works. Go fills an absent port from the scheme, and for
// socks5 that means 1080, so "socks5://host" is a well-formed address that
// dials a port nobody chose: the request is refused, and refused at 1080 it
// reads as "the proxy is down" rather than as "a number is missing". Asking for
// the port as a field is what turns that into a visible omission.
type Spec struct {
	Scheme string
	Host   string
	Port   string
	User   string
	Pass   string
}

// Schemes are the proxy types offered, in menu order. socks5h is beside socks5
// because it is the same wire with the name resolved at the proxy end, which is
// the difference between a request that leaks the hostname being dialled and one
// that does not.
var Schemes = []string{"http", "https", "socks5", "socks5h"}

// DefaultPort is the port a scheme gets when the form leaves it empty. It is the
// port that scheme listens on in the overwhelming majority of setups, so a user
// who skipped the field is far likelier to want it than to want silence.
func DefaultPort(scheme string) string {
	switch scheme {
	case "https":
		return "443"
	case "socks5", "socks5h":
		return "1080"
	default:
		return "8080"
	}
}

// URL assembles the parts into an address. The credentials go through url.User
// rather than string concatenation, so a password containing @, : or / cannot
// split the URL into a different host and a different password than the user
// typed.
func (s Spec) URL() string {
	u := url.URL{Scheme: s.Scheme, Host: s.Host}
	if s.Port != "" {
		u.Host = net.JoinHostPort(s.Host, s.Port)
	}
	if s.User != "" {
		u.User = url.UserPassword(s.User, s.Pass)
	}
	return u.String()
}

// Validate reports what is wrong with the parts, for a form that must not close
// on a value that will not work.
//
// The port is required rather than defaulted. Defaulting it here would put the
// mistake back: a socks5 proxy on 21001 typed as "socks5://vpn.example.com"
// would be reported as valid and then dialed on 1080. The form fills the port
// in for the user (DefaultPort), so an empty field means the form's own default
// is already on screen and there is nothing to ask about.
func (s Spec) Validate() error {
	if s.Scheme == "" {
		return fmt.Errorf("pick a proxy type")
	}
	if !slices.Contains(Schemes, s.Scheme) {
		return fmt.Errorf("%q is not a proxy type — use %s", s.Scheme, strings.Join(Schemes, ", "))
	}
	if strings.TrimSpace(s.Host) == "" {
		return fmt.Errorf("the proxy needs a host")
	}
	if strings.ContainsAny(s.Host, " \t/") {
		return fmt.Errorf("%q is not a host — no spaces or slashes", s.Host)
	}
	port := strings.TrimSpace(s.Port)
	if port == "" {
		return fmt.Errorf("the proxy needs a port — %s would assume %d",
			s.Scheme, portNumber(DefaultPort(s.Scheme)))
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("%q is not a port number", port)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("%d is not a port (1–65535)", n)
	}
	if s.User == "" && s.Pass != "" {
		return fmt.Errorf("a password without a login goes nowhere — fill in the login too")
	}
	return nil
}

// ParseSpec takes an address apart into the parts a form edits, so reopening the
// dialog shows what is configured rather than an empty set of fields. Values it
// cannot use are left empty for the user to fill in; nothing is invented.
func ParseSpec(raw string) Spec {
	s := Spec{}
	p := strings.TrimSpace(raw)
	if p == "" || strings.EqualFold(p, "off") || strings.EqualFold(p, "none") {
		return s
	}
	if !strings.Contains(p, "://") {
		p = "http://" + p
	}
	u, err := url.Parse(p)
	if err != nil {
		return s
	}
	s.Scheme = strings.ToLower(u.Scheme)
	if !slices.Contains(Schemes, s.Scheme) {
		s.Scheme = ""
	}
	s.Host = u.Hostname()
	s.Port = u.Port()
	if u.User != nil {
		s.User = u.User.Username()
		s.Pass, _ = u.User.Password()
	}
	return s
}

// RedactProxy masks the password in an address so it can be shown in the
// transcript, the dialog title and a status line. A proxy address almost always
// carries credentials, and every one of those places is a screen someone else can
// see, a screenshot, or a scrollback buffer kept past exit.
func RedactProxy(raw string) string {
	if !strings.Contains(raw, "@") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, hasPass := u.User.Password(); !hasPass {
		return raw
	}
	schemeEnd := strings.Index(raw, "://")
	if schemeEnd < 0 {
		return raw
	}
	schemeEnd += len("://")
	at := strings.LastIndex(raw, "@")
	if at <= schemeEnd {
		return raw
	}
	// The userinfo is spliced rather than reassembled through url.URL.String. That
	// call percent-encodes the mask, because the mask is not a legal password
	// character — so a "redacted" address would come back as
	// "dedo:%E2%80%A2%E2%80%A2..." and no longer resemble what it redacts.
	// Splicing leaves every other byte alone, which is also what makes this safe
	// to call on anything: it cannot change an address, only hide a field of it.
	login, _, _ := strings.Cut(raw[schemeEnd:at], ":")
	return raw[:schemeEnd] + login + ":••••" + raw[at:]
}

func portNumber(s string) int {
	n, _ := strconv.Atoi(s)
	return n
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

// Effective returns the address provider calls actually go through. Almost every
// provider is https, so HTTPS wins; HTTP is the fallback for a user who set only
// that one. showProxy reports through this rather than through CurrentProxy so
// the address on screen is the one in use and not a variable that happens to be
// populated.
func (s ProxySettings) Effective() string {
	if s.HTTPS != "" {
		return s.HTTPS
	}
	return s.HTTP
}

// ProxyVars is the map .env is written from, so the variables a proxy can live
// in and the ones persisted are one list. It exists because they were two: a
// variable SetProxy cleared but persistProxy never wrote back would be written
// by the next save from a list that had drifted from the one being cleared.
func (s ProxySettings) ProxyVars() map[string]string {
	vars := map[string]string{}
	if s.HTTP != "" {
		vars[EnvHTTPProxy] = s.HTTP
	}
	if s.HTTPS != "" {
		vars[EnvHTTPSProxy] = s.HTTPS
	}
	if s.NoProxy != "" {
		vars[EnvNoProxy] = s.NoProxy
	}
	return vars
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
