package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dedomorozoff/dmcode/internal/editor/editor"
	dmtools "github.com/dedomorozoff/dmcode/internal/tools"
)

// TestEditorToggleKeepsTheWorkspace pins the two-mode contract: ctrl+e (the
// /editor command) starts the workspace in editor mode with the tabs for the
// files the agent changed; the same command returns to the chat, where the
// chrome — tab bar, tree, status bar — stays up around the transcript.
func TestEditorToggleKeepsTheWorkspace(t *testing.T) {
	m := newTurnModel(t)
	m.workDir = editorWorkDir(t)

	_ = m.openEditor()
	if m.ed == nil || m.ed.Chat {
		t.Fatal("first /editor must open the editor mode")
	}
	if !m.ed.Embed {
		t.Fatal("the embedded editor must not be allowed to call tea.Quit")
	}
	// The tree is the user's to open: entering the editor shows the files the
	// agent touched, not a navigator the user did not ask for.
	if _, treeVisible, _ := m.ed.PanelState(); treeVisible {
		t.Fatal("entering the editor must leave the project tree closed")
	}
	// A fresh session has changed nothing, so the editor shows its own chrome
	// and says there is no file open — not a tab bar reading "[untitled]" over
	// an empty gutter, which is a document nobody has.
	v := m.View()
	if !strings.Contains(v.Content, "◫") {
		t.Fatalf("editor mode must show its own chrome, got:\n%.200s", v.Content)
	}
	if !strings.Contains(v.Content, "no file open") {
		t.Fatalf("an editor with no file must say so, got:\n%.200s", v.Content)
	}
	if strings.Contains(v.Content, "[untitled]") {
		t.Fatalf("an empty buffer must not be presented as a file, got:\n%.200s", v.Content)
	}

	// Back to the chat: ctrl+q yields CloseEditorMsg, the panels fold, the
	// screen is the transcript.
	_, quitCmd := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if msg, ok := quitCmd().(editor.CloseEditorMsg); !ok {
		t.Fatalf("quit command yielded %T, want CloseEditorMsg", msg)
	} else {
		if _, _ = m.Update(msg); !m.ed.Chat {
			t.Fatal("CloseEditorMsg must return to chat mode")
		}
	}
	termOpen, treeVisible, termFocus := m.ed.PanelState()
	if termOpen || treeVisible || termFocus {
		t.Fatalf("returning to the chat must fold the panels: termOpen=%v treeVisible=%v termFocus=%v", termOpen, treeVisible, termFocus)
	}
	chat := m.View()
	if !strings.Contains(chat.Content, "waiting for a task") {
		t.Fatalf("chat mode must render the transcript and input, got:\n%.200s", chat.Content)
	}
	if strings.Contains(chat.Content, "no file open") || strings.Contains(chat.Content, "◫") {
		t.Fatal("chat mode must hide the editor's screen")
	}
}

// TestEditorQuitReturnsToChatMode pins the exit path: ctrl+q in the editor
// comes back as CloseEditorMsg instead of tea.Quit, and the chat keeps
// running — closing the editor must not end the program.
func TestEditorQuitReturnsToChatMode(t *testing.T) {
	m := newTurnModel(t)
	m.workDir = editorWorkDir(t)
	_ = m.openEditor() // into editor mode

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+q in the editor must produce an exit command")
	}
	if msg, ok := cmd().(editor.CloseEditorMsg); !ok {
		t.Fatalf("quit command yielded %T, want CloseEditorMsg", msg)
	} else {
		if _, _ = m.Update(msg); !m.ed.Chat {
			t.Fatal("CloseEditorMsg must return the workspace to chat mode")
		}
	}
	// And the chat still renders through the chrome.
	if v := m.View(); !strings.Contains(v.Content, "waiting for a task") {
		t.Fatalf("after quitting the editor the chat must be the main area, got:\n%.200s", v.Content)
	}
}

// TestEditorCtrlEReturnsToChatMode pins the toggle: the same ctrl+e that
// opened the editor sends it back, no ctrl+q needed — the editor asks with
// ToggleEditorMsg and the host flips the mode and folds the panels.
func TestEditorCtrlEReturnsToChatMode(t *testing.T) {
	m := newTurnModel(t)
	m.workDir = editorWorkDir(t)
	_ = m.openEditor() // into editor mode

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+e in the editor must ask the host to toggle")
	}
	if msg, ok := cmd().(editor.ToggleEditorMsg); !ok {
		t.Fatalf("ctrl+e yielded %T, want ToggleEditorMsg", msg)
	} else {
		if _, _ = m.Update(msg); !m.ed.Chat {
			t.Fatal("ToggleEditorMsg must return the workspace to chat mode")
		}
	}
	termOpen, treeVisible, termFocus := m.ed.PanelState()
	if termOpen || treeVisible || termFocus {
		t.Fatalf("returning to the chat must fold the panels: termOpen=%v treeVisible=%v termFocus=%v", termOpen, treeVisible, termFocus)
	}
	if v := m.View(); !strings.Contains(v.Content, "waiting for a task") {
		t.Fatalf("after ctrl+e the chat must be the main area, got:\n%.200s", v.Content)
	}
}

// TestCtrlQQuitsFromTheChat pins the exit key: ctrl+q in the chat ends the
// program — the sidebar has advertised it all along — while inside the
// editor the same key still only returns to the chat.
func TestCtrlQQuitsFromTheChat(t *testing.T) {
	m := newTurnModel(t)
	m.workDir = t.TempDir()

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+q in the chat must produce a quit command")
	}
	if msg, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("ctrl+q in the chat yielded %T, want QuitMsg", cmd())
	} else {
		_ = msg
	}

	// In the editor the key is the chat toggle, not an exit.
	_ = m.openEditor()
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if msg, ok := cmd().(editor.CloseEditorMsg); !ok {
		t.Fatalf("ctrl+q in the editor yielded %T, want CloseEditorMsg", cmd())
	} else {
		if _, _ = m.Update(msg); !m.ed.Chat {
			t.Fatal("ctrl+q in the editor must return to the chat")
		}
	}
}

// TestNewSessionDropsTheEditorsTabs pins what /new does to the workspace:
// the tabs the previous session's edits opened do not come back on the next
// ctrl+e — a new conversation does not start from the old one's files.
func TestNewSessionDropsTheEditorsTabs(t *testing.T) {
	m := newSessionModel(t)
	m.workDir = t.TempDir()

	// The agent changed a file; entering the editor opens a tab for it.
	dmtools.RecordChange(filepath.Join(m.workDir, "a.txt"), "x\n", "y\n")
	t.Cleanup(dmtools.ResetChanges)
	_ = m.openEditor()
	if v := m.View(); !strings.Contains(v.Content, "a.txt") {
		t.Fatalf("the changed file must have a tab, got:\n%.200s", v.Content)
	}

	// A new session resets the tally and drops the tabs with it.
	m.newSession("")
	_ = m.openEditor()
	if v := m.View(); strings.Contains(v.Content, "a.txt") {
		t.Fatalf("the previous session's tabs must not survive /new, got:\n%.200s", v.Content)
	}
}

// TestWorkspaceIsUpFromStart pins the startup promise: the workspace exists
// from Init — the tree, the git panel and the terminal are one key or one
// status-bar icon away — but every panel starts closed and the first screen
// is the chat.
func TestWorkspaceIsUpFromStart(t *testing.T) {
	m := newTurnModel(t)
	m.workDir = t.TempDir()
	// The shell starts only when the terminal panel opens; nothing to leak —
	// but keep the guard in case a test opens the panel later.
	t.Cleanup(func() {
		if m.ed != nil {
			m.ed.Shutdown()
		}
	})

	_ = m.Init() // the returned listeners are channel waits; not run here
	if m.ed == nil {
		t.Fatal("Init must bring the workspace up")
	}
	if !m.ed.Chat {
		t.Fatal("the workspace starts in chat mode")
	}
	termOpen, treeVisible, termFocus := m.ed.PanelState()
	if termOpen || treeVisible || termFocus {
		t.Fatalf("panels must start closed: termOpen=%v treeVisible=%v termFocus=%v", termOpen, treeVisible, termFocus)
	}
	if v := m.View(); !strings.Contains(v.Content, "waiting for a task") {
		t.Fatalf("chat must be the main area at startup, got:\n%.200s", v.Content)
	}
}
