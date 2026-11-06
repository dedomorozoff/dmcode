package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dedomorozoff/dmcode/internal/config"
)

// withTempHistory points DMCODE_HISTORY_PATH at a scratch file for the test.
func withTempHistory(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "history.jsonl")
	t.Setenv("DMCODE_HISTORY_PATH", p)
	return p
}

// TestPromptHistoryRoundTrip: sent prompts persist to disk and come back
// oldest first, so a restart can recall everything that was typed before.
func TestPromptHistoryRoundTrip(t *testing.T) {
	p := withTempHistory(t)
	appendPromptHistory("первый промпт")
	appendPromptHistory("второй промпт")

	got := loadPromptHistory()
	if len(got) != 2 || got[0] != "первый промпт" || got[1] != "второй промпт" {
		t.Errorf("loadPromptHistory = %v", got)
	}
	// Windows reports 0666 for every writable file and keeps permissions in an
	// ACL, so there is nothing for a mode check to see there.
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("history file mode = %v (err %v), want 0600", fi, err)
		}
	}
}

// TestRemoveLastPromptHistoryErasesOnlyItsOwnPrompt: a rewind takes the prompt
// out of the on-disk history as well as the recall list, but the guard against
// a second session's newer entry must never eat that entry.
func TestRemoveLastPromptHistoryErasesOnlyItsOwnPrompt(t *testing.T) {
	withTempHistory(t)
	appendPromptHistory("мой промпт")
	appendPromptHistory("чужой промпт")

	// Mismatch: the last line belongs to somebody else, so nothing is erased.
	removeLastPromptHistory("мой промпт")
	got := loadPromptHistory()
	if len(got) != 2 || got[1] != "чужой промпт" {
		t.Fatalf("mismatch guard failed, history = %v", got)
	}

	// Match: the tail is erased, the older entry survives.
	removeLastPromptHistory("чужой промпт")
	got = loadPromptHistory()
	if len(got) != 1 || got[0] != "мой промпт" {
		t.Errorf("after erase history = %v, want [мой промпт]", got)
	}
}

// TestRemoveLastPromptHistoryToleratesLegacyLines: the eraser reads a line the
// way the loader does, so a hand-written plain-text entry can still be undone.
func TestRemoveLastPromptHistoryToleratesLegacyLines(t *testing.T) {
	p := withTempHistory(t)
	if err := os.WriteFile(p, []byte("старая строка\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	removeLastPromptHistory("старая строка")
	if got := loadPromptHistory(); len(got) != 0 {
		t.Errorf("legacy line survived the erase: %v", got)
	}
}

// TestRemoveLastPromptHistoryLeavesARefusedFileAlone: rewriting is only for
// the erase that matched; a refused one must not touch the file at all.
func TestRemoveLastPromptHistoryLeavesARefusedFileAlone(t *testing.T) {
	p := withTempHistory(t)
	if err := os.WriteFile(p, []byte("{\"text\":\"промпт\"}\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	removeLastPromptHistory("другой")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\"text\":\"промпт\"}\n\n" {
		t.Errorf("a refused erase rewrote the file: %q", data)
	}
}

// TestRewindTakesThePromptOffDisk is the half the old behaviour got wrong: the
// recall list forgot the prompt, the file remembered it, and the next start
// offered an undo back.
func TestRewindTakesThePromptOffDisk(t *testing.T) {
	m := newSessionModel(t)
	sendTurn(t, m, "первый")
	sendTurn(t, m, "второй")
	m.rewind()

	got := loadPromptHistory()
	for _, p := range got {
		if p == "второй" {
			t.Errorf("the rewound prompt is still on disk: %v", got)
		}
	}
	if len(got) != 1 || got[0] != "первый" {
		t.Errorf("history = %v, want [первый]", got)
	}
}

// TestPromptHistoryCapsAtLimit: a long history trims to the most recent
// entries, so the recall buffer and the file stay bounded.
func TestPromptHistoryCapsAtLimit(t *testing.T) {
	withTempHistory(t)
	for i := 0; i < maxPromptHistory+20; i++ {
		appendPromptHistory(fmt.Sprintf("prompt-%d", i))
	}
	got := loadPromptHistory()
	if len(got) != maxPromptHistory {
		t.Fatalf("history length = %d, want %d", len(got), maxPromptHistory)
	}
	if got[len(got)-1] != fmt.Sprintf("prompt-%d", maxPromptHistory+19) {
		t.Errorf("newest entry lost: %q", got[len(got)-1])
	}
}

// TestPromptHistoryToleratesLegacyLines: hand-edited plain lines are kept as
// prompts, and a corrupt JSON line is skipped instead of failing the file.
func TestPromptHistoryToleratesLegacyLines(t *testing.T) {
	p := withTempHistory(t)
	content := "plain legacy prompt\n{\"text\":\"json prompt\",\"ts\":123}\n{broken json\n\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadPromptHistory()
	if len(got) != 2 || got[0] != "plain legacy prompt" || got[1] != "json prompt" {
		t.Errorf("loadPromptHistory = %v", got)
	}
}

// TestPromptHistoryRecallKeys drives the real key path: ↑ walks back through
// saved prompts, ↓ walks forward again, and the half-typed draft survives the
// round trip.
func TestPromptHistoryRecallKeys(t *testing.T) {
	withTempHistory(t)
	m := InitialModel(nil, nil, config.Provider{}, nil, nil, nil)
	m.promptHistory = []string{"первый", "второй"}
	m.histPos = len(m.promptHistory)
	m.input.SetValue("набираю черновик")

	var model tea.Model = m
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.input.Value(); got != "второй" {
		t.Errorf("after ↑ input = %q, want %q", got, "второй")
	}
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.input.Value(); got != "первый" {
		t.Errorf("after second ↑ input = %q, want %q", got, "первый")
	}
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.input.Value(); got != "второй" {
		t.Errorf("after ↓ input = %q, want %q", got, "второй")
	}
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.input.Value(); got != "набираю черновик" {
		t.Errorf("draft was not restored: %q", got)
	}
	if m.histPos != len(m.promptHistory) {
		t.Errorf("histPos = %d, want %d", m.histPos, len(m.promptHistory))
	}
}

// TestSavePromptResetsRecallPosition: sending a prompt appends it, points the
// recall cursor back at the live draft, and persists the entry.
func TestSavePromptResetsRecallPosition(t *testing.T) {
	p := withTempHistory(t)
	m := InitialModel(nil, nil, config.Provider{}, nil, nil, nil)
	m.promptHistory = []string{"старый"}
	m.histPos = 1

	m.savePrompt("новый промпт")
	if m.histPos != len(m.promptHistory) || m.promptHistory[len(m.promptHistory)-1] != "новый промпт" {
		t.Errorf("savePrompt state wrong: pos=%d history=%v", m.histPos, m.promptHistory)
	}
	data, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(data), "новый промпт") {
		t.Errorf("prompt not persisted: %v %q", err, data)
	}
}
