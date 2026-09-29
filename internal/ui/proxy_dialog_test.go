package ui

import (
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

// clear empties the focused field with backspaces, the way a user edits a
// rejected value instead of retyping it from scratch.
func (d proxyKeys) clear() proxyKeys {
	n := len([]rune(d.m.proxy.buf))
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
	d = d.type_("127.0.0.1:3128").enter()

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

// TestProxyDialogRejectsABadAddressBeforeClosing: a typo must not close the
// wizard and strand the user in the transcript — the field stays, the reason
// shows in the status bar, and nothing is applied.
func TestProxyDialogRejectsABadAddressBeforeClosing(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}
	d := proxyKeys{m: m}.type_("/proxy").enter().walkTo(0).enter()
	d = d.type_("http://%zz").enter()

	if !m.proxy.open || m.proxy.stage != proxyURL {
		t.Fatalf("the dialog closed on an invalid address: open=%v stage=%d", m.proxy.open, m.proxy.stage)
	}
	if config.CurrentProxy().Active {
		t.Error("an invalid address was applied anyway")
	}
	if m.statusText == "" || m.statusText == "/proxy" {
		t.Errorf("the reason for the rejection is not visible: %q", m.statusText)
	}

	// The corrected address then applies from the same dialog: the rejected
	// text is still in the field for editing, so it is erased rather than
	// retyped before the fix goes in.
	d = d.clear().type_("127.0.0.1:3128").enter()
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
