package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/update"
)

// withUpdateClient points the version commands at a local server for the
// duration of one test. The seam is the only reason these commands can be
// tested without reaching GitHub.
func withUpdateClient(t *testing.T, api string) {
	t.Helper()
	old := newUpdateClient
	newUpdateClient = func() *update.Client { return &update.Client{API: api} }
	t.Cleanup(func() { newUpdateClient = old })
}

// recentTranscript is the last few lines of the transcript, unstyled: the
// report a command wrote, without the escapes of the renderer.
func recentTranscript(m *uiModel) string {
	tail := m.history[max(len(m.history)-12, 0):]
	rows := make([]string, 0, len(tail))
	for _, l := range tail {
		rows = append(rows, l.text)
	}
	return strings.Join(rows, "\n")
}

// TestVersionPrintsWhatTheBuildIs: the version alone cannot say whether this
// is the release or the checkout, so /version prints all three stamps.
func TestVersionPrintsWhatTheBuildIs(t *testing.T) {
	oldV, oldB, oldC := config.Version, config.Branch, config.Commit
	t.Cleanup(func() { config.Version, config.Branch, config.Commit = oldV, oldB, oldC })
	config.Version, config.Branch, config.Commit = "0.1.1", "main", "abc1234"

	m := suggestModel(t)
	m.showVersion()
	got := recentTranscript(m)
	for _, want := range []string{
		"dmcode v0.1.1", "branch:", "main", "commit:", "abc1234",
		"source:", "github.com/dedomorozoff/dmcode",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("/version did not print %q:\n%s", want, got)
		}
	}
}

// TestVersionLeavesUnknownStampsOut: a build made outside a git checkout has
// no branch and no commit, and a row of "unknown" says less than no row at
// all.
func TestVersionLeavesUnknownStampsOut(t *testing.T) {
	oldV, oldB, oldC := config.Version, config.Branch, config.Commit
	t.Cleanup(func() { config.Version, config.Branch, config.Commit = oldV, oldB, oldC })
	config.Version, config.Branch, config.Commit = "dev", "unknown", "unknown"

	m := suggestModel(t)
	m.showVersion()
	got := recentTranscript(m)
	if !strings.Contains(got, "dmcode dev") {
		t.Errorf("/version did not print the version:\n%s", got)
	}
	if strings.Contains(got, "branch:") || strings.Contains(got, "commit:") {
		t.Errorf("/version printed a stamp it does not have:\n%s", got)
	}
}

// TestUpdateCheckReportsANewerRelease drives the check through the messages
// the command actually sends: the server's tag comes back as a version, and
// the transcript says it in the words of what it means.
func TestUpdateCheckReportsANewerRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"tag_name":"v9.9.9","body":"notes","html_url":"u","assets":[]}`))
	}))
	defer srv.Close()
	withUpdateClient(t, srv.URL)
	oldV := config.Version
	t.Cleanup(func() { config.Version = oldV })
	config.Version = "0.1.1"

	m := suggestModel(t)
	cmd := m.updateCmd(true)
	if cmd == nil {
		t.Fatal("the check did not start")
	}
	_, _ = m.Update(cmd())
	if got := recentTranscript(m); !strings.Contains(got, "version v9.9.9 is available") {
		t.Errorf("the check reported:\n%s", got)
	}
	if m.cancelUpdate != nil {
		t.Error("the cancel function outlived the check")
	}
}

// TestUpdateDoneSaysWhatItDid: the install report names the version and the
// path, and does not claim a checksum it never saw. Both halves are checked
// because a report that always says "verified" passes the happy case.
func TestUpdateDoneSaysWhatItDid(t *testing.T) {
	m := suggestModel(t)
	m.handleUpdateDone(updateDoneMsg{res: update.Result{
		Version: "0.2.0", Path: "/usr/local/bin/dmcode", Verified: true,
	}})
	got := recentTranscript(m)
	if !strings.Contains(got, "installed v0.2.0 at /usr/local/bin/dmcode") {
		t.Errorf("the report is:\n%s", got)
	}
	if strings.Contains(got, "not verified") {
		t.Errorf("a verified download was reported as unverified:\n%s", got)
	}

	unverified := suggestModel(t)
	unverified.handleUpdateDone(updateDoneMsg{res: update.Result{Version: "0.2.0", Path: "/x/dmcode"}})
	if got := recentTranscript(unverified); !strings.Contains(got, "was not verified") {
		t.Errorf("an unverified download said nothing about it:\n%s", got)
	}
}

// TestAStoppedUpdateIsNotAnError: Esc is the user's own doing, and a red
// "update failed: context canceled" is the tool arguing with them.
func TestAStoppedUpdateIsNotAnError(t *testing.T) {
	m := suggestModel(t)
	cmd := m.updateCmd(true)
	m.cancelUpdate() // what Esc does
	_, _ = m.Update(cmd())
	got := recentTranscript(m)
	if strings.Contains(got, "failed") {
		t.Errorf("a cancelled update was reported as a failure:\n%s", got)
	}
	if !strings.Contains(got, "stopped") {
		t.Errorf("a cancelled update was not reported at all:\n%s", got)
	}
}

// TestChangelogPrintsTheNotes: the release body is the project's account of
// what changed, and /changelog is how it is read without leaving the chat.
func TestChangelogPrintsTheNotes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v9.9.9","body":"## Fixed\n\n- a thing","assets":[]}`))
	}))
	defer srv.Close()
	withUpdateClient(t, srv.URL)

	m := suggestModel(t)
	cmd := m.changelogCmd()
	if cmd == nil {
		t.Fatal("the changelog did not start")
	}
	_, _ = m.Update(cmd())
	got := recentTranscript(m)
	if !strings.Contains(got, "release v9.9.9") || !strings.Contains(got, "a thing") {
		t.Errorf("the changelog printed:\n%s", got)
	}
}

// TestTheVersionCommandRunsFromThePrompt is the reachability half: the command
// is in the list, but the list is not the only door — a user types it.
func TestTheVersionCommandRunsFromThePrompt(t *testing.T) {
	m := suggestModel(t)
	m.input.SetValue("/version")
	enterKey(m)
	if got := recentTranscript(m); !strings.Contains(got, "dmcode ") {
		t.Errorf("/version printed nothing:\n%s", got)
	}
}

// TestUpdateCheckFromThePromptStartsACheck drives the "/update check" prefix
// branch: bare "/update" installs, and the one word has to reach the check
// rather than the install.
// TestUpdateLeavesALocalBuildAlone: a checkout is the user's own code, and no
// self-updater can put it back — so a build nobody tagged is told about the
// release instead of being replaced by it.
func TestUpdateLeavesALocalBuildAlone(t *testing.T) {
	oldV := config.Version
	t.Cleanup(func() { config.Version = oldV })
	config.Version = "dev"

	m := suggestModel(t)
	cmd := m.handleUpdateCheck(updateCheckMsg{latest: "0.1.1"})
	if cmd != nil {
		t.Fatal("a local build started an install")
	}
	if m.cancelUpdate != nil {
		t.Error("a local build left a download running")
	}
	if got := recentTranscript(m); !strings.Contains(got, "local build") {
		t.Errorf("/update said:\n%s", got)
	}
}

func TestUpdateCheckFromThePromptStartsACheck(t *testing.T) {
	withUpdateClient(t, "http://127.0.0.1:1")
	m := suggestModel(t)
	m.input.SetValue("/update check")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("/update check did nothing")
	}
	if m.cancelUpdate == nil {
		t.Fatal("/update check did not start a check")
	}
	m.forgetUpdate()
}

// TestUpdateRefusesAnUnknownArgument: the difference between "tell me" and
// "do it" is one word, so a typo is refused rather than quietly installing
// something.
func TestUpdateRefusesAnUnknownArgument(t *testing.T) {
	m := suggestModel(t)
	m.input.SetValue("/update now")
	enterKey(m)
	if m.cancelUpdate != nil {
		t.Error("/update now started an update")
	}
	if !strings.Contains(m.statusText, "usage: /update") {
		t.Errorf("the status line says %q", m.statusText)
	}
}
