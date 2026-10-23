package ui

// The version commands: /version says what is running, /update brings the
// newest release from GitHub, and /changelog says what changed in it.
//
// They are one file because they answer one family of questions — "what am I
// running", "is there a newer one", "what is in it" — and share one client.
// Keeping them together is also what keeps them honest: the version /update
// compares against is the one /version prints, and not a second reading of the
// same build stamps.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/i18n"
	"github.com/dedomorozoff/dmcode/internal/update"
)

// updateTimeout bounds a check and a download together.
//
// It is generous on purpose: a release binary weighs tens of megabytes, and a
// slow link is not a failure. Esc ends the wait early; this is the backstop
// for a connection that neither answers nor fails.
const updateTimeout = 10 * time.Minute

// newUpdateClient builds the client the commands use.
//
// It is a variable so a test can point it at a local server: a command whose
// only behaviour is a network call cannot be tested at all, and what is worth
// testing here is the wording and the flow, not GitHub.
var newUpdateClient = update.New

// versionLabel is a version as it is shown: "dev" for a build nobody tagged,
// "v0.1.1" for a release.
//
// The sidebar's brand block and /version both ask this, so the two cannot
// disagree about how many v's a version has.
func versionLabel(v string) string {
	if v == "" || v == "dev" {
		return "dev"
	}
	return "v" + v
}

// showVersion prints what is running: the version, the branch and commit it
// was built from, and where the binary sits.
//
// The branch is the reason this is more than the version in the sidebar: a
// release and a checkout can carry the same version string, and only one of
// them is the code the user is editing.
func (m *uiModel) showVersion() {
	rows := []string{"dmcode " + versionLabel(config.Version)}
	add := func(label, value string) {
		if value == "" || value == "unknown" {
			return
		}
		rows = append(rows, fmt.Sprintf("  %-8s%s", label+":", value))
	}
	add("branch", config.Branch)
	add("commit", config.Commit)
	if exe, err := os.Executable(); err == nil {
		add("binary", exe)
	}
	add("source", "github.com/"+update.Repo)
	m.history = append(m.history, line{kindSys, strings.Join(rows, "\n")})
	m.historyDirty = true
	m.followVP()
}

// updateCheckMsg is what the check against GitHub found, before anything was
// changed: the newest release, and whether it is newer than this build.
type updateCheckMsg struct {
	rel      update.Release
	latest   string
	upToDate bool
	newer    bool
	// only says the user asked to look and not to install, so the handler
	// stops after reporting.
	only bool
	err  error
}

// updateDoneMsg is what an install did: the release that went in, and where.
type updateDoneMsg struct {
	res update.Result
	err error
}

// changelogMsg is the newest release, fetched for its notes.
type changelogMsg struct {
	rel update.Release
	err error
}

// updateCmd asks GitHub for the newest release and, unless only is set,
// installs it.
//
// Both halves run on a goroutine: the check is a network round trip, the
// install is a download of a binary, and the interface has to keep answering
// keys meanwhile. The two are separate messages so the status line can say
// "downloading" between them — the difference between a wait the user can see
// and one that looks hung.
func (m *uiModel) updateCmd(only bool) tea.Cmd {
	if m.cancelUpdate != nil {
		return nil
	}
	base := m.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, updateTimeout)
	m.cancelUpdate = cancel
	m.statusText = i18n.T("checking for updates…")
	return func() tea.Msg {
		rel, err := newUpdateClient().Latest(ctx)
		if err != nil {
			return updateCheckMsg{err: err}
		}
		latest := rel.Version()
		msg := updateCheckMsg{rel: rel, latest: latest, only: only}
		switch {
		case latest == config.Version:
			msg.upToDate = true
		case update.Compare(latest, config.Version) < 0:
			msg.newer = true
		}
		return msg
	}
}

// installCmd downloads the release and puts it where the running binary is.
func (m *uiModel) installCmd(rel update.Release) tea.Cmd {
	exe, err := os.Executable()
	if err != nil {
		m.statusText = i18n.T("error")
		m.history = append(m.history, line{kindErr, i18n.T("update failed: ") +
			i18n.T("cannot find the running binary: ") + err.Error()})
		m.historyDirty = true
		m.followVP()
		return nil
	}
	base := m.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, updateTimeout)
	m.cancelUpdate = cancel
	c := newUpdateClient()
	return func() tea.Msg {
		res, err := update.Install(ctx, c, rel, exe)
		cancel()
		return updateDoneMsg{res: res, err: err}
	}
}

// isReleaseVersion says the version is a number the release workflow stamped,
// rather than "dev" from a plain `make build` or empty from a bare `go build`.
//
// It decides whether /update may install at all, so it is deliberately narrow:
// only a leading decimal number counts, and anything else is a local build.
func isReleaseVersion(v string) bool {
	first, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	_, err := strconv.Atoi(first)
	return err == nil
}

// handleUpdateCheck reports what the check found and, when an update was asked
// for and there is one, starts the download.
func (m *uiModel) handleUpdateCheck(msg updateCheckMsg) tea.Cmd {
	m.forgetUpdate()
	if msg.err != nil {
		m.reportUpdateError(i18n.T("update failed: "), msg.err)
		return nil
	}
	current, latest := versionLabel(config.Version), versionLabel(msg.latest)
	// A checkout is the user's own code, and no self-updater can put it back:
	// replacing it with a release would throw away work nothing else holds a
	// copy of. So a build nobody tagged is told what the release is and left
	// alone — including by a bare /update, which would otherwise install.
	if !isReleaseVersion(config.Version) {
		m.statusText = i18n.T("ready")
		m.note(fmt.Sprintf(i18n.T("this is a local build (%s) — the newest release is %s; /update leaves it alone"), current, latest), kindSys)
		return nil
	}
	switch {
	case msg.upToDate:
		m.statusText = i18n.T("ready")
		m.note(fmt.Sprintf(i18n.T("dmcode %s is the latest release"), latest), kindSys)
	case msg.newer:
		m.statusText = i18n.T("ready")
		m.note(fmt.Sprintf(i18n.T("this build (%s) is newer than the latest release (%s)"), current, latest), kindSys)
	case msg.only:
		m.statusText = i18n.T("ready")
		m.note(fmt.Sprintf(i18n.T("version %s is available — you run %s"), latest, current), kindSys)
	default:
		m.note(fmt.Sprintf(i18n.T("version %s is available — you run %s"), latest, current), kindSys)
		m.statusText = i18n.T("downloading update…")
		return m.installCmd(msg.rel)
	}
	return nil
}

// handleUpdateDone reports the install, in the words of what actually
// happened: a download that was not checked against a checksum says so rather
// than borrowing the confidence of one that was.
func (m *uiModel) handleUpdateDone(msg updateDoneMsg) {
	m.forgetUpdate()
	if msg.err != nil {
		m.reportUpdateError(i18n.T("update failed: "), msg.err)
		return
	}
	m.statusText = i18n.T("ready")
	m.note(fmt.Sprintf(i18n.T("installed %s at %s — restart dmcode to use it"),
		versionLabel(msg.res.Version), msg.res.Path), kindSys)
	if !msg.res.Verified {
		m.note(i18n.T("the release publishes no checksum file, so the download was not verified"), kindSys)
	}
	if msg.res.Deferred {
		m.note(i18n.T("the previous binary is still on disk and will be removed at the next start"), kindSys)
	}
}

// changelogCmd asks GitHub for the newest release and prints its notes.
func (m *uiModel) changelogCmd() tea.Cmd {
	if m.cancelUpdate != nil {
		return nil
	}
	base := m.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, updateTimeout)
	m.cancelUpdate = cancel
	m.statusText = i18n.T("fetching the release notes…")
	return func() tea.Msg {
		rel, err := newUpdateClient().Latest(ctx)
		cancel()
		return changelogMsg{rel: rel, err: err}
	}
}

// handleChangelog prints the release notes. They are rendered as a markdown
// document rather than as a line of prose: a release notes file is headings
// and bullets, and the transcript has one renderer that is good at those.
func (m *uiModel) handleChangelog(msg changelogMsg) {
	m.forgetUpdate()
	if msg.err != nil {
		m.reportUpdateError(i18n.T("changelog: "), msg.err)
		return
	}
	m.statusText = i18n.T("ready")
	m.note(fmt.Sprintf(i18n.T("release %s — notes"), versionLabel(msg.rel.Version())), kindSys)
	if notes := strings.TrimSpace(msg.rel.Notes); notes != "" {
		m.history = append(m.history, line{kindAgent, notes})
	} else {
		m.note(i18n.T("this release has no notes"), kindSys)
	}
	m.historyDirty = true
	m.followVP()
}

// forgetUpdate drops the cancel function of a finished update, so the next one
// may start and Esc has nothing stale to cancel.
func (m *uiModel) forgetUpdate() {
	if m.cancelUpdate != nil {
		m.cancelUpdate()
		m.cancelUpdate = nil
	}
}

// note appends one line to the transcript and keeps it visible.
func (m *uiModel) note(text string, kind lineKind) {
	m.history = append(m.history, line{kind, text})
	m.historyDirty = true
	m.followVP()
}

// reportUpdateError puts a failed check, download or fetch into the transcript
// in the words of what happened.
//
// A cancellation is the user's own doing, so it is a plain line and a plain
// status rather than an error: "update failed: context canceled" is the tool
// arguing with the person who pressed Esc, and a red bar for something they
// stopped themselves is a lie about the state of the program.
func (m *uiModel) reportUpdateError(prefix string, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		m.statusText = i18n.T("stopped")
		m.note(i18n.T("stopped"), kindSys)
	case errors.Is(err, context.DeadlineExceeded):
		m.statusText = i18n.T("error")
		m.note(prefix+i18n.T("timed out"), kindErr)
	default:
		m.statusText = i18n.T("error")
		m.note(prefix+err.Error(), kindErr)
	}
}
