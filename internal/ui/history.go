package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/i18n"
	"github.com/dedomorozoff/dmcode/internal/tools"
)

// maxPromptHistory is how many prompts stay in memory for ↑/↓ recall, and how
// many survive when the on-disk file is trimmed.
const maxPromptHistory = 300

// maxHistoryFileBytes bounds the on-disk file. Appending is cheap, but the file
// is rewritten once it grows past this so months of use cannot balloon it.
const maxHistoryFileBytes = 256 * 1024

// promptHistoryPath returns the file the user's sent prompts persist to,
// ~/.dmcode/history.jsonl, unless DMCODE_HISTORY_PATH points somewhere else.
// The override exists for tests and for users who want the history next to
// their project instead of their home directory.
func promptHistoryPath() (string, error) {
	if p := os.Getenv("DMCODE_HISTORY_PATH"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".dmcode", "history.jsonl"), nil
}

// loadPromptHistory reads the persisted prompts, oldest first. Each line is a
// small JSON record; older plain-text lines from hand-edited files are
// tolerated, and a line that parses to nothing is skipped rather than failing
// the whole history.
func loadPromptHistory() []string {
	path, err := promptHistoryPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := make([]string, 0, 64)
	for _, l := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		text := l
		if strings.HasPrefix(l, "{") {
			var rec struct {
				Text string `json:"text"`
			}
			if json.Unmarshal([]byte(l), &rec) != nil || rec.Text == "" {
				continue
			}
			text = rec.Text
		}
		out = append(out, text)
	}
	if len(out) > maxPromptHistory {
		out = out[len(out)-maxPromptHistory:]
	}
	return out
}

// appendPromptHistory persists one sent prompt. A history failure must never
// touch the turn itself: the prompt has already gone to the model, so the worst
// case here is a lost entry, and that is silently accepted.
func appendPromptHistory(text string) {
	path, err := promptHistoryPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	rec, err := json.Marshal(map[string]any{
		"text": text,
		"ts":   time.Now().Unix(),
	})
	if err != nil {
		return
	}

	info, statErr := os.Stat(path)
	needsTrim := statErr == nil && info.Size() > maxHistoryFileBytes

	if needsTrim {
		old := loadPromptHistory()
		entries := make([]string, 0, len(old))
		for _, p := range old {
			line, _ := json.Marshal(map[string]any{"text": p, "ts": time.Now().Unix()})
			entries = append(entries, string(line))
		}
		entries = append(entries, string(rec))
		if err := os.WriteFile(path, []byte(strings.Join(entries, "\n")+"\n"), 0o600); err != nil {
			return
		}
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(rec, '\n'))
}

// shortenPath trims a path from the left so it fits n cells, marking the cut
// with an ellipsis. The last elements are the ones that identify a project, so
// the tail is what survives — "…\projects\dmcode" reads correctly where
// "C:\Users\…\dmcode-main" would not distinguish two checkouts.
func shortenPath(p string, n int) string {
	if n <= 1 || ansi.StringWidth(p) <= n {
		return p
	}
	// Windows separators are kept alongside the slash so a path stays copyable.
	seps := "/\\"
	runes := []rune(p)
	keep := n - 1
	if keep > len(runes) {
		keep = len(runes)
	}
	tail := string(runes[len(runes)-keep:])
	if cut := len(p) - keep; cut > 0 {
		if strings.ContainsAny(p[:cut], seps) {
			return "…" + tail
		}
	}
	// No separator in the discarded part means the tail alone is ambiguous, so
	// show the whole thing and let the caller's truncate() mark it instead.
	return p
}

// trimLeft cuts a value to n cells from the left, keeping the tail.
//
// It is shortenPath without the separator rule, for the values where the head is
// boilerplate and the tail is the identity: a proxy address, where the scheme and
// the leading host label say little and the port is the half a reader is looking
// for. truncate would keep the head and drop the tail, so "socks5://vpn.example
// .com:21001" in 29 cells would become "socks5://vpn.example.com:…" — a row naming
// the host and hiding the port. shortenPath cannot do this job because its rule
// gives up when the cut part holds no separator, and "socks5://vpn." has none.
func trimLeft(s string, n int) string {
	if n <= 1 || ansi.StringWidth(s) <= n {
		return s
	}
	runes := []rune(s)
	keep := n - 1
	if keep > len(runes) {
		keep = len(runes)
	}
	return "…" + string(runes[len(runes)-keep:])
}

// changeDir moves the session to path and re-fences the tools there.
//
// os.Chdir is what actually moves the agent: the tools address files relative
// to the process directory, so changing the boundary without changing the
// directory — or the reverse — would leave the two disagreeing about which
// project is being edited.
func (m *uiModel) changeDir(path string) tea.Cmd {
	target := strings.TrimSpace(path)
	if target == "" {
		m.history = append(m.history, line{kindSys, i18n.T("current folder: ") + m.workDir})
		m.historyDirty = true
		return nil
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(m.workDir, target)
	}
	if err := tools.SetRoot(target); err != nil {
		m.history = append(m.history, line{kindErr, i18n.T("cannot enter that folder: ") + err.Error()})
		m.historyDirty = true
		return nil
	}
	if err := os.Chdir(target); err != nil {
		m.history = append(m.history, line{kindErr, i18n.T("cannot enter that folder: ") + err.Error()})
		m.historyDirty = true
		return nil
	}
	m.workDir = tools.Root()
	m.workDirShort = filepath.Base(m.workDir)
	m.history = append(m.history, line{kindSys, i18n.T("folder changed to: ") + m.workDir})
	m.historyDirty = true
	// The repository the sidebar's GIT section names is a property of the folder,
	// so it is re-read here. Until the refresh lands the old folder's branch is
	// still drawn — which is why the state is cleared rather than left: a branch
	// that is known to belong somewhere else is worse than no section at all.
	m.gitState = gitState{}
	// The agent is told where it is through its instruction, so a stale one
	// would have it reasoning about the previous directory.
	return tea.Batch(m.rebuildRunner(), m.gitStatusCmd())
}

// savePrompt records a sent prompt for ↑/↓ recall and persists it to disk.
func (m *uiModel) savePrompt(text string) {
	m.promptHistory = append(m.promptHistory, text)
	m.histPos = len(m.promptHistory)
	m.draft = ""
	appendPromptHistory(text)
}

// showRecentPrompts prints the last ten prompts into the transcript.
func (m *uiModel) showRecentPrompts() {
	n := len(m.promptHistory)
	if n == 0 {
		m.history = append(m.history, line{kindSys, i18n.T("prompt history is empty")})
	}
	start := n - 10
	if start < 0 {
		start = 0
	}
	for i := start; i < n; i++ {
		m.history = append(m.history, line{kindSys, fmt.Sprintf("%d. %s", i+1, truncate(m.promptHistory[i], 120))})
	}
	m.historyDirty = true
}
