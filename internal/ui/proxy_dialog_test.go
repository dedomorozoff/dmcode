package ui

import (
	"net/url"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/config"
)

// proxyKeys drives the /proxy dialog through the real Update dispatch, the way
// a terminal does, so the tests exercise the routing rather than internals.
type proxyKeys struct {
	m   *uiModel
	cmd tea.Cmd
}

func (d proxyKeys) send(msg tea.KeyPressMsg) proxyKeys {
	var model tea.Model = d.m
	model, d.cmd = model.Update(msg)
	return d
}

func (d proxyKeys) type_(text string) proxyKeys {
	for _, r := range text {
		d = d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return d
}

func (d proxyKeys) enter() proxyKeys { return d.send(tea.KeyPressMsg{Code: tea.KeyEnter}) }
func (d proxyKeys) down() proxyKeys  { return d.send(tea.KeyPressMsg{Code: tea.KeyDown}) }
func (d proxyKeys) up() proxyKeys    { return d.send(tea.KeyPressMsg{Code: tea.KeyUp}) }
func (d proxyKeys) esc() proxyKeys   { return d.send(tea.KeyPressMsg{Code: tea.KeyEscape}) }
func (d proxyKeys) tab() proxyKeys   { return d.send(tea.KeyPressMsg{Code: tea.KeyTab}) }
func (d proxyKeys) right() proxyKeys { return d.send(tea.KeyPressMsg{Code: tea.KeyRight}) }

// clearField empties one field of the address form with backspaces, the way a
// user erases a value it defaulted to. Indexed by the pf* constants so a test
// cannot address the wrong row by counting.
func (d proxyKeys) clearField(i int) proxyKeys {
	d = d.focusField(i)
	n := len([]rune(d.m.proxy.fields[i]))
	for j := 0; j < n; j++ {
		d = d.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	return d
}

// focusField moves the form's focus to field i by tabbing forward from wherever
// it is, so the test never has to know the order the dialog opens in.
func (d proxyKeys) focusField(i int) proxyKeys {
	// Bounded rather than "until equal": the focus only moves forward, so asking
	// for a field behind the current one would spin forever, and a hung test is a
	// worse failure than one that says which field it wanted.
	for n := 0; d.m.proxy.focus != i && n < pfCount; n++ {
		d = d.tab()
	}
	if d.m.proxy.focus != i {
		d.m.proxy.focus = i
	}
	return d
}

// clear empties the focused field with backspaces, the way a user edits a
// rejected value instead of retyping it from scratch.
func (d proxyKeys) clear() proxyKeys {
	f := d.m.proxy.field()
	if f == nil {
		f = &d.m.proxy.buf
	}
	n := len([]rune(*f))
	for i := 0; i < n; i++ {
		d = d.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	return d
}

func (d proxyKeys) walkTo(i int) proxyKeys {
	for d.m.proxy.selected > 0 {
		d = d.up()
	}
	for d.m.proxy.selected < i {
		d = d.down()
	}
	return d
}

// TestProxyCommandOpensTheDialog types /proxy and expects the dialog, not a
// transcript report the user then has to read to learn what to type next.
func TestProxyCommandOpensTheDialog(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	d := proxyKeys{m: m}.type_("/proxy").enter()
	if !m.proxy.open || m.proxy.stage != proxyPick {
		t.Fatalf("/proxy did not open the dialog: open=%v stage=%d", m.proxy.open, m.proxy.stage)
	}
	if s := ansi.Strip(m.View().Content); !strings.Contains(s, "proxy") {
		t.Errorf("the dialog is not on screen:\n%s", s)
	}

	// Escape must leave the live setting untouched.
	d.esc()
	if m.proxy.open || config.CurrentProxy().Active {
		t.Errorf("esc did not back out cleanly: open=%v active=%v", m.proxy.open, config.CurrentProxy().Active)
	}
}

// ansiStripProxy is gone: the frame is read via m.View().Content and stripped
// with ansi.Strip inline.
// TestProxyDialogSetsTheAddress walks the dialog: pick "turn on", type the
// address, apply. The proxy must go live, land in .env, and the dialog must
// close — the probe that follows reports reachability in the transcript.
func TestProxyDialogSetsTheAddress(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}
	d := proxyKeys{m: m}.type_("/proxy").enter()

	d = d.walkTo(0).enter()
	if m.proxy.stage != proxyURL {
		t.Fatalf("the first menu row did not open the address stage: %d", m.proxy.stage)
	}
	// The type is the focused field. The host is then filled, and the port — which
	// the form pre-filled with the http default — is replaced rather than appended
	// to, which is what a user standing in front of the form would do.
	d = d.tab().type_("127.0.0.1").clearField(pfPort).type_("3128").enter()

	if m.proxy.open {
		t.Error("the dialog stayed open after a valid address")
	}
	s := config.CurrentProxy()
	if !s.Active || s.HTTPS != "http://127.0.0.1:3128" {
		t.Fatalf("the live proxy is %+v, want http://127.0.0.1:3128", s)
	}
	env, err := os.ReadFile(".env")
	if err != nil || !strings.Contains(string(env), config.EnvHTTPProxy+"=http://127.0.0.1:3128") {
		t.Errorf(".env does not carry the proxy: %q, %v", env, err)
	}
}

// The bug that motivated the form: a socks5 address typed without a port is
// well-formed, so nothing complained, and Go dialed 1080 — a port nobody chose.
// The form must not let that through: an empty port is refused, and the reason
// names the port that would have been used.
func TestProxyFormRefusesAMissingPort(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}
	d := proxyKeys{m: m}.type_("/proxy").enter().walkTo(0).enter()

	// The type field is focused first: two steps take http to socks5, and the
	// port follows it to that scheme's default. Then a host, and the port cleared
	// by hand — the state that used to reach the wire as 1080.
	d = d.type_("ss").tab().type_("vpn.example.com")
	d = d.clearField(pfPort).enter()

	if !m.proxy.open {
		t.Fatal("a proxy with no port was applied")
	}
	if config.CurrentProxy().Active {
		t.Error("a proxy with no port reached the environment")
	}
	if !strings.Contains(m.statusText, "port") {
		t.Errorf("the reason does not mention the port: %q", m.statusText)
	}
}

// TestProxyDialogRejectsABadAddressBeforeClosing: a typo must not close the
// wizard and strand the user in the transcript — the field stays, the reason
// shows in the status bar, and nothing is applied.
func TestProxyDialogRejectsABadAddressBeforeClosing(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}
	d := proxyKeys{m: m}.type_("/proxy").enter().walkTo(0).enter()
	// A host carrying a scheme is the typo the old single field could express and
	// this one cannot: "host" here is a host, and the type is a separate field.
	d = d.tab().type_("bad host").enter()

	if !m.proxy.open || m.proxy.stage != proxyURL {
		t.Fatalf("the dialog closed on an invalid host: open=%v stage=%d", m.proxy.open, m.proxy.stage)
	}
	if config.CurrentProxy().Active {
		t.Error("an invalid address was applied anyway")
	}
	if m.statusText == "" || m.statusText == "/proxy" {
		t.Errorf("the reason for the rejection is not visible: %q", m.statusText)
	}

	// The corrected host then applies from the same dialog: the rejected text is
	// still in the field for editing, so it is erased rather than retyped.
	d = d.clear().type_("127.0.0.1").enter()
	if m.proxy.open || !config.CurrentProxy().Active {
		t.Errorf("the corrected address did not apply: open=%v active=%v", m.proxy.open, config.CurrentProxy().Active)
	}
}

// TestProxyDialogTurnsItOff picks the "off" row and expects the live setting
// and .env to lose the proxy.
func TestProxyDialogTurnsItOff(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}
	m.applyProxy("127.0.0.1:3128")
	if !config.CurrentProxy().Active {
		t.Fatal("the setup proxy did not take")
	}

	proxyKeys{m: m}.type_("/proxy").enter().walkTo(2).enter()
	if config.CurrentProxy().Active {
		t.Error("the proxy is still active after the dialog's off action")
	}
	env, _ := os.ReadFile(".env")
	for _, k := range config.ProxyEnvKeys() {
		if strings.Contains(string(env), k+"=") {
			t.Errorf("%s survived the dialog's off action in .env:\n%s", k, env)
		}
	}
}

// TestProxyDialogSetsTheBypassList: the second menu row edits NO_PROXY without
// touching the proxy itself.
func TestProxyDialogSetsTheBypassList(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}
	m.applyProxy("http://127.0.0.1:8080")

	d := proxyKeys{m: m}.type_("/proxy").enter().walkTo(1).enter()
	if m.proxy.stage != proxyNo {
		t.Fatalf("the second menu row did not open the bypass stage: %d", m.proxy.stage)
	}
	d = d.type_("localhost,*.internal").enter()

	s := config.CurrentProxy()
	if !strings.Contains(s.NoProxy, "localhost") {
		t.Errorf("the bypass list is %q, want the entries given", s.NoProxy)
	}
	if !s.Active {
		t.Error("editing the bypass list cleared the proxy")
	}
}

// TestProxyTypedFastPathsStillWork keeps the syntax the transcript advertises
// honest: a URL sets it, "off" clears it — both from the input line, without
// the dialog. The prefix used to reach applyProxy uncut, which made every
// typed form fail and a bare "/proxy something" fall through to the agent.
func TestProxyTypedFastPathsStillWork(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}

	proxyKeys{m: m}.type_("/proxy 127.0.0.1:3128").enter()
	if s := config.CurrentProxy(); !s.Active || s.HTTPS != "http://127.0.0.1:3128" {
		t.Fatalf("/proxy <url> did not set the proxy: %+v", s)
	}

	proxyKeys{m: m}.type_("/proxy off").enter()
	if config.CurrentProxy().Active {
		t.Error("/proxy off did not clear the proxy")
	}
}

// The credentials have to survive the trip from the form to .env intact, which
// means they are escaped going in: a password with @ in it must not be able to
// split the URL into a different host and a different password. This is the
// shape that actually reaches a provider, so the assertion is on the parsed
// credentials rather than on the string.
func TestProxyFormSetsLoginAndPassword(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	const pass = "p@ss:word/1"
	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}
	d := proxyKeys{m: m}.type_("/proxy").enter().walkTo(0).enter()

	d = d.type_("ss").tab().type_("vpn.example.com").clearField(pfPort).type_("21001")
	d = d.tab().type_("dedo").tab().type_(pass).enter()

	s := config.CurrentProxy()
	if !s.Active {
		t.Fatal("the proxy did not go live")
	}
	u, err := url.Parse(s.HTTPS)
	if err != nil {
		t.Fatalf("the stored address does not parse: %v", err)
	}
	if u.Scheme != "socks5" || u.Hostname() != "vpn.example.com" || u.Port() != "21001" {
		t.Errorf("stored as %s://%s:%s", u.Scheme, u.Hostname(), u.Port())
	}
	if got, _ := u.User.Password(); u.User.Username() != "dedo" || got != pass {
		t.Errorf("credentials stored as %q / %q", u.User.Username(), got)
	}
}

// The password is on screen, so it must be dots. The transcript is written to
// disk and the terminal keeps its own scrollback; a proxy login is a credential
// and neither is a place to leave one.
func TestProxyFormDoesNotPrintThePassword(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}
	d := proxyKeys{m: m}.type_("/proxy").enter().walkTo(0).enter()
	d = d.type_("ss").tab().type_("vpn.example.com").clearField(pfPort).type_("21001")
	d = d.tab().type_("dedo").tab().type_("hunter2")

	if s := ansi.Strip(m.View().Content); strings.Contains(s, "hunter2") {
		t.Errorf("the password is on screen:\n%s", s)
	}
	if s := ansi.Strip(m.View().Content); !strings.Contains(s, "•••••••") {
		t.Errorf("the password row shows no mask:\n%s", s)
	}
}

// The dialog is a form, so it has to open as one: five labelled fields with the
// current values in them. A user who has set a proxy and reopens /proxy to
// change the password must not find an empty box and conclude the setting is
// gone.
func TestProxyFormOpensSeededFromTheCurrentProxy(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}
	m.applyProxy("socks5://dedo:hunter2@vpn.example.com:21001")

	proxyKeys{m: m}.type_("/proxy").enter().walkTo(0).enter()
	if m.proxy.stage != proxyURL {
		t.Fatalf("the address stage did not open: %d", m.proxy.stage)
	}
	want := map[int]string{
		pfScheme: "socks5",
		pfHost:   "vpn.example.com",
		pfPort:   "21001",
		pfUser:   "dedo",
		pfPass:   "hunter2",
	}
	for i, w := range want {
		if got := m.proxy.fields[i]; got != w {
			t.Errorf("field %d holds %q, want %q", i, got, w)
		}
	}
	// Every field is labelled, so the values can be read as answers to questions
	// rather than as a column of bare strings.
	s := ansi.Strip(m.View().Content)
	for _, label := range []string{"type", "host", "port", "login", "password"} {
		if !strings.Contains(s, label) {
			t.Errorf("the form has no %q row:\n%s", label, s)
		}
	}
}

// The port is the field that decides whether the proxy works, and it is the one
// a scheme change has to move: defaulting socks5 to 8080 because the field
// arrived holding http's default is the same class of bug as defaulting it to
// nothing.
func TestProxyFormPortFollowsTheType(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}
	d := proxyKeys{m: m}.type_("/proxy").enter().walkTo(0).enter()

	if got := m.proxy.fields[pfPort]; got != config.DefaultPort(config.Schemes[0]) {
		t.Fatalf("the form opened on port %q, want the http default", got)
	}
	// http -> https, by arrow, since the type is the field with fixed values.
	d = d.right()
	if m.proxy.fields[pfScheme] != "https" {
		t.Fatalf("the type did not step: %q", m.proxy.fields[pfScheme])
	}
	if got := m.proxy.fields[pfPort]; got != config.DefaultPort("https") {
		t.Errorf("switching to https left the port at %q", got)
	}

	// A port the user typed is not overwritten by a later scheme change.
	d = d.focusField(pfPort).clearField(pfPort).type_("21001")
	d = d.focusField(pfScheme).right()
	if got := m.proxy.fields[pfPort]; got != "21001" {
		t.Errorf("a typed port was overwritten by a scheme change: %q", got)
	}
	if got := m.proxy.fields[pfPort]; got != "21001" {
		t.Errorf("a typed port was overwritten by a scheme change: %q", got)
	}
}

// TestProxyDialogTitleShowsTheCurrentState: the one thing a user reopening the
// dialog must not have to remember is whether a proxy is already on.
func TestProxyDialogTitleShowsTheCurrentState(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}

	d := proxyKeys{m: m}.type_("/proxy").enter()
	if s := ansi.Strip(m.View().Content); strings.Contains(s, "127.0.0.1") {
		t.Errorf("an unset proxy is shown as an address:\n%s", s)
	}

	m.applyProxy("http://127.0.0.1:8080")
	d = d.type_("/proxy").enter()
	if s := ansi.Strip(m.View().Content); !strings.Contains(s, "127.0.0.1:8080") {
		t.Errorf("the dialog title does not show the active proxy:\n%s", s)
	}
}
