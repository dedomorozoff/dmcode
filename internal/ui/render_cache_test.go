package ui

import (
	"strings"
	"testing"
)

// freshRender rebuilds the transcript from scratch, the way an uncached model
// would, so an incremental render can be compared against it.
func freshRender(m *uiModel) string {
	c := *m
	c.cachedRows = nil
	c.cachedHistory = ""
	c.cachedWidth = 0
	c.historyDirty = true
	return c.renderHistory()
}

// TestRenderHistoryIncrementalMatchesFullRebuild is the guarantee the per-line
// cache has to make: reusing cached rows for unchanged lines must produce
// exactly the transcript a from-scratch render would, whether the last line
// grew by a streamed delta or was replaced wholesale.
func TestRenderHistoryIncrementalMatchesFullRebuild(t *testing.T) {
	m := newTurnModel(t)
	m.history = append(m.history,
		line{kindUser, "сделай таблицу из пяти строк и объясни"},
		line{kindAgent, "## План\n\n1. прочитать\n2. изменить\n3. проверить\n"},
		line{kindTool, "read_file main.go"},
		line{kindToolRes, "     1 package main"},
		line{kindAgent, "Черновик."},
	)
	if got, want := m.renderHistory(), freshRender(m); got != want {
		t.Fatalf("first render diverged:\n%q\nvs\n%q", got, want)
	}

	// Streaming appends to the last agent line; every earlier line must come
	// out of the cache unchanged.
	m.history[len(m.history)-1].text += " Всё сошлось."
	m.historyDirty = true
	if got, want := m.renderHistory(), freshRender(m); got != want {
		t.Fatalf("after a delta:\n%q\nvs\n%q", got, want)
	}

	// A replacement rewrites the last agent line and drops the agent lines
	// behind it, shrinking the history the cache was built against.
	m.applyAgentText(spoken{text: "## Итог\n\nготово\n", replace: true})
	if got, want := m.renderHistory(), freshRender(m); got != want {
		t.Fatalf("after a replacement:\n%q\nvs\n%q", got, want)
	}
}

// TestRenderHistoryCacheSurvivesClearAndWidthChange covers the two events that
// must not leave the cache describing a transcript that no longer exists.
func TestRenderHistoryCacheSurvivesClearAndWidthChange(t *testing.T) {
	m := newTurnModel(t)
	m.history = append(m.history, line{kindUser, strings.Repeat("слово ", 40)})
	m.renderHistory()

	m.history = nil
	m.historyDirty = true
	if got := m.renderHistory(); got != "" {
		t.Fatalf("after /clear the transcript is %q, want it empty", got)
	}

	m.history = append(m.history, line{kindUser, "длинная строка, которая переносится по ширине панели"})
	m.historyDirty = true
	m.renderHistory()
	m.width = 60
	m.layout()
	m.historyDirty = true
	if got, want := m.renderHistory(), freshRender(m); got != want {
		t.Fatalf("after a resize:\n%q\nvs\n%q", got, want)
	}
}
