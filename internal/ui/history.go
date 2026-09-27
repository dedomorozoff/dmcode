package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dedomorozoff/dmcode/internal/i18n"
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
