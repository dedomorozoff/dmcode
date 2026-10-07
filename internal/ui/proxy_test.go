package ui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/config"
	dmtools "github.com/dedomorozoff/dmcode/internal/tools"
)

// tallyModel is a model with a sidebar, sized so the SESSION block is on screen.
func tallyModel(t *testing.T) *uiModel {
	t.Helper()
	m := newSetupModel(t)
	m.width, m.height = 100, 30
	m.layout()
	return m
}

// cleanTally points the tools at a scratch directory and empties the tally, so a
// test never inherits what the one before it recorded.
func cleanTally(t *testing.T) {
	t.Helper()
	if err := dmtools.SetRoot(inTempDir(t)); err != nil {
		t.Fatal(err)
	}
	dmtools.ResetChanges()
	t.Cleanup(dmtools.ResetChanges)
}

// cleanProxyEnv resets the process-global proxy variables for one test, so a
// proxy set here does not leak into the next.
func cleanProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range append(config.ProxyEnvKeys(), config.EnvNoProxy, "no_proxy") {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	t.Cleanup(func() { _ = config.SetProxy("off") })
}

// hasErr reports whether the transcript carries an error line containing sub.
func hasErr(m *uiModel, sub string) bool {
	for _, l := range m.history {
		if l.kind == kindErr && strings.Contains(l.text, sub) {
			return true
		}
	}
	return false
}

// The sidebar's SESSION block reports what the agent actually changed, and stays
// quiet until something has: a column of zeroes on a fresh session is noise, and
// their absence is the information.
func TestSidebarShowsTheChangeTally(t *testing.T) {
	cleanTally(t)
	m := tallyModel(t)

	if s := ansi.Strip(m.sidebarView(20)); strings.Contains(s, "files:") {
		t.Errorf("a fresh session already reports changes:\n%s", s)
	}

	dmtools.RecordChange("new.txt", "", "a\nb\nc\n")
	dmtools.RecordChange("other.txt", "", "x\n")

	s := ansi.Strip(m.sidebarView(20))
	if !strings.Contains(s, "files: 2") {
		t.Errorf("the sidebar does not report two changed files:\n%s", s)
	}
	if !strings.Contains(s, "+4") {
		t.Errorf("the sidebar does not report the added lines:\n%s", s)
	}
	if !strings.Contains(s, "-0") {
		t.Errorf("the sidebar does not report the removed lines:\n%s", s)
	}
}

// A session that writes the same line over and over must not accumulate a number
// per write: the tally is the difference between how the session found the file
// and how it left it.
// A proxy in effect is on the panel, because the panel is where the model and the
// wire are both already reported — and a user whose requests fail behind one has
// no other way to see that they are going through it at all.
func TestSidebarSaysWhenTheConnectionGoesThroughAProxy(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := tallyModel(t)
	if s := ansi.Strip(m.sidebarView(40)); strings.Contains(s, "socks5") {
		t.Errorf("a direct connection is shown as proxied:\n%s", s)
	}

	// A short host, so the assertion is on the whole row rather than on what
	// survived the panel's width. The long-host case is its own test below.
	m.applyProxy("socks5://localhost:21001")
	s := ansi.Strip(m.sidebarView(40))
	if !strings.Contains(s, "socks5 localhost:21001") {
		t.Errorf("the panel does not report the proxy:\n%s", s)
	}

	m.applyProxy("off")
	if s := ansi.Strip(m.sidebarView(40)); strings.Contains(s, "socks5") {
		t.Errorf("the proxy outlived the setting that turned it off:\n%s", s)
	}
}

// The panel is 31 columns. A masked login does not fit alongside a host, so the
// tag drops the credentials outright — and what is left must still end in the
// port, since that is the half a user is there to read.
func TestSidebarProxyRowHasNoCredentialsAndKeepsThePort(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := tallyModel(t)
	m.applyProxy("socks5://dedo:dedodedo@vpn.example.com:21001")

	s := ansi.Strip(m.sidebarView(40))
	if strings.Contains(s, "dedo") || strings.Contains(s, "dedodedo") {
		t.Errorf("the panel prints the proxy login:\n%s", s)
	}
	if !strings.Contains(s, "vpn.example.com:21001") {
		t.Errorf("the proxy row lost the host and port:\n%s", s)
	}
	// The row has to fit the box it is drawn in, or the panel stops lining up
	// with the rest of the frame. The width to compare against is measured off
	// the frame's own border row rather than taken from sidebarBoxWidth: the
	// rendered box carries a column of margin that the constant does not count,
	// and a test that disagrees with the constant about the box's width is a test
	// that fails on the constant rather than on a row being too long.
	rows := strings.Split(s, "\n")
	boxW := ansi.StringWidth(rows[0])
	for _, line := range rows {
		if w := ansi.StringWidth(line); w != boxW {
			t.Errorf("a row is %d wide, the box is %d:\n%q", w, boxW, line)
		}
	}
}

// A bypass list changes nothing about the proxy, so it earns its row only when it
// has something in it — an empty list would be a label over an absence.
func TestSidebarShowsTheBypassListOnlyWhenItHasEntries(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := tallyModel(t)
	m.applyProxy("socks5://vpn.example.com:21001")
	if s := ansi.Strip(m.sidebarView(40)); strings.Contains(s, "bypass") {
		t.Errorf("an empty bypass list is printed:\n%s", s)
	}

	m.applyProxy("no localhost")
	if s := ansi.Strip(m.sidebarView(40)); !strings.Contains(s, "bypass: localhost") {
		t.Errorf("a set bypass list is not on the panel:\n%s", s)
	}
}

func TestSidebarTallyIsTheSessionDiff(t *testing.T) {
	cleanTally(t)
	// Each write reports what was on disk before it, exactly as writeFileAtomic
	// does. The first write finds an empty file, so it is wholly additive; the
	// later ones replace a line each. All three are diffed against the session's
	// baseline, so what the sidebar shows is the end state, not three steps.
	prev := ""
	for _, c := range []string{"a\nb\n", "a\nB\n", "a\nB!\n"} {
		dmtools.RecordChange("f.txt", prev, c)
		prev = c
	}

	s := ansi.Strip(tallyModel(t).sidebarView(20))
	// The file did not exist when the session found it, so the baseline is empty
	// and the end state is wholly additive — two lines that were not there
	// before. What is being checked is that it is two and not six: the two later
	// writes each replaced a line, and counting them separately would show +6.
	if !strings.Contains(s, "files: 1") || !strings.Contains(s, "+2") || !strings.Contains(s, "-0") {
		t.Errorf("three writes to one file reported as:\n%s", s)
	}
}

// /proxy with a URL sets it for this session and writes it to .env, so the next
// start does not silently go direct. With "off" it removes the variables from
// both: MergeDotEnv only adds and replaces, so a proxy left behind in the file
// would be read back and "off" would not mean anything.
func TestProxyCommandSetsAndClears(t *testing.T) {
	dir := inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}

	m.applyProxy("127.0.0.1:3128")
	if s := config.CurrentProxy(); !s.Active || s.HTTPS != "http://127.0.0.1:3128" {
		t.Fatalf("the live proxy is %+v, want the one just set", s)
	}
	env, err := os.ReadFile(dir + "/.env")
	if err != nil {
		t.Fatalf("the proxy was not written to .env: %v", err)
	}
	if !strings.Contains(string(env), config.EnvHTTPProxy+"=http://127.0.0.1:3128") {
		t.Errorf(".env does not carry the proxy:\n%s", env)
	}

	// A value that is not a URL must be refused and must not disturb what works.
	m.applyProxy("http://%zz")
	if s := config.CurrentProxy(); s.HTTPS != "http://127.0.0.1:3128" {
		t.Errorf("a bad URL disturbed the working proxy: %+v", s)
	}
	if !hasErr(m, "not a valid proxy URL") {
		t.Error("a bad URL was not reported to the user")
	}

	m.applyProxy("off")
	if config.CurrentProxy().Active {
		t.Error("the proxy is still active after /proxy off")
	}
	env, _ = os.ReadFile(dir + "/.env")
	for _, k := range config.ProxyEnvKeys() {
		if strings.Contains(string(env), k+"=") {
			t.Errorf("%s survived /proxy off in .env:\n%s", k, env)
		}
	}
}

// /proxy must not eat the user's other settings when it rewrites .env: the file
// is merged, and the proxy keys are the only thing it removes.
func TestProxyCommandKeepsTheRestOfDotEnv(t *testing.T) {
	dir := inTempDir(t)
	cleanProxyEnv(t)

	if err := os.WriteFile(dir+"/.env", []byte("# my note\nOPENAI_API_KEY=sk-real\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}
	m.applyProxy("http://127.0.0.1:8080")

	env, _ := os.ReadFile(dir + "/.env")
	for _, want := range []string{"# my note", "OPENAI_API_KEY=sk-real", config.EnvHTTPProxy + "="} {
		if !strings.Contains(string(env), want) {
			t.Errorf(".env lost %q:\n%s", want, env)
		}
	}
}

// The bypass list is set without touching the proxy itself, so a user behind a
// corporate proxy can exempt their own services without turning it off.
func TestProxyBypassListIsIndependent(t *testing.T) {
	inTempDir(t)
	cleanProxyEnv(t)

	m := newSetupModel(t)
	m.prov = config.Provider{BaseURL: "http://provider.invalid/v1", Model: "m"}
	m.applyProxy("http://127.0.0.1:8080")
	m.applyProxy("no localhost,127.0.0.1")

	s := config.CurrentProxy()
	if !strings.Contains(s.NoProxy, "localhost") {
		t.Errorf("the bypass list is %q, want the entries given", s.NoProxy)
	}
	if !s.Active {
		t.Error("setting a bypass list cleared the proxy")
	}
}

// one's: reporting them would claim changes against files never opened.
func TestNewSessionClearsTheChangeTally(t *testing.T) {
	cleanTally(t)
	dmtools.RecordChange("x.txt", "", "a\n")
	if dmtools.Changes().Files != 1 {
		t.Fatal("the change did not register")
	}

	m := newSetupModel(t)
	m.input.SetValue("/new")
	var model tea.Model = m
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := dmtools.Changes(); got != (dmtools.ChangeStats{}) {
		t.Errorf("/new left the tally at %+v", got)
	}
}
