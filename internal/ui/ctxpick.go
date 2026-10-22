package ui

// ctxpick.go — the restart offer after a local model's context window could
// not hold a turn.
//
// The failure itself — "request (5701 tokens) exceeds the available context
// size (4096 tokens)" — already names both numbers; what it does not name is
// that the fix is one llama-server restart away. Until this dialog the only
// answer was a trip to `.env` to hand-write DMCODE_LLAMA_ARGS, which is the
// kind of usability hole a user closes by switching to a cloud provider.

import (
	"errors"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dedomorozoff/dmcode/internal/discover"
	"github.com/dedomorozoff/dmcode/internal/i18n"
	"github.com/dedomorozoff/dmcode/internal/llm"
)

// ctxPickState offers to restart llama-server with a larger context. It lives
// on the model the way every other overlay's state does, so the highlight
// survives redraws.
type ctxPickState struct {
	open  bool
	offer *llm.ContextExceededError
	sizes []int // the two context sizes on offer, tokens
	list  listView
}

// offerCtxRestart decides whether a failed turn deserves the restart dialog:
// only for a failure typed as a full context window, and only against a
// provider this build launched around a .gguf file — a cloud provider cannot
// be restarted from here, and offering to be would be advertising a trap.
func (m *uiModel) offerCtxRestart(err error) {
	var e *llm.ContextExceededError
	if !errors.As(err, &e) || !discover.IsGGUF(m.prov) {
		return
	}
	// The offer is announced in the transcript, right under the error it
	// answers: a dialog alone leaves nothing readable behind after esc, and
	// the transcript is where the numbers already are.
	m.history = append(m.history,
		line{kindSys, i18n.T("this is a local llama.cpp model — the turn can be retried on a restarted server with a larger context")})
	m.historyDirty = true
	base := e.SuggestedContext()
	m.ctxPick = ctxPickState{
		open:  true,
		offer: e,
		// A second row at twice the first, because the failure came from a
		// window that was already too small once — the doubling is the one
		// guess that cannot be *less* than enough, and both rows are explicit
		// numbers on screen rather than anything silent.
		sizes: []int{base, base * 2},
	}
}

// ctxPickKey drives the restart dialog. enter restarts with the highlighted
// size, esc closes the box and leaves the window as it is.
func (m *uiModel) ctxPickKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.ctxPick
	switch msg.String() {
	case "esc":
		p.open = false
		return m, nil
	case "up":
		if p.list.sel > 0 {
			p.list.sel--
		}
		return m, nil
	case "down":
		if p.list.sel < len(p.sizes)-1 {
			p.list.sel++
		}
		return m, nil
	case "enter":
		if p.list.sel >= len(p.sizes) {
			return m, nil
		}
		n := p.sizes[p.list.sel]
		p.open = false
		return m, m.restartGGUFWithCtx(n)
	}
	return m, nil
}

// restartGGUFWithCtx takes the current llama-server down and starts a new one
// with the context size forced to n, then hands the load to the machinery a
// /setup GGUF pick already drives.
func (m *uiModel) restartGGUFWithCtx(n int) tea.Cmd {
	// Started first, before anything else is printed: the old server dies
	// here, and StartGGUF would answer with the shared state if it were still
	// standing — a restart has to mean a new launch, not a share of the dead
	// one.
	stopGGUF()

	// os.Setenv rather than the settings seam: the size has to govern this
	// process's next launch, and the line below tells the user how to keep it
	// after dmcode itself restarts. Editing their settings out of a restart
	// dialog would write a decision made in nine seconds over one they may
	// have tuned in .env on purpose.
	os.Setenv("DMCODE_LLAMA_ARGS", discover.SetContextSize(os.Getenv("DMCODE_LLAMA_ARGS"), n))
	args := os.Getenv("DMCODE_LLAMA_ARGS")

	m.history = append(m.history,
		line{kindSys, fmt.Sprintf(i18n.T("restarting llama-server with -c %d"), n)},
		line{kindSys, fmt.Sprintf(i18n.T("to keep the size after dmcode restarts, set .env's DMCODE_LLAMA_ARGS to %s"), args)},
	)
	m.historyDirty = true

	l, err := startGGUF()
	if err != nil {
		// The fast failures — DMCODE_GGUF unset, the binary gone — are the
		// same as a first launch's, and reported the same way.
		m.statusText = i18n.T("failed")
		m.history = append(m.history,
			line{kindErr, i18n.T("llama-server did not start: ") + err.Error()},
			line{kindSys, i18n.T(".env keeps DMCODE_GGUF — fix DMCODE_LLAMA_SERVER or the path, then restart dmcode.")})
		m.historyDirty = true
		m.followVP()
		return nil
	}

	m.beginGGUFLoad(l, []line{
		{kindSys, i18n.T("starting llama-server on ") + os.Getenv("DMCODE_GGUF")},
		{kindSys, i18n.T("this can take a while — the model loads before the first reply")},
	})
	return m.ggufLoadCmd()
}

// ctxPickBox renders the restart offer: the two sizes, the first marked as
// recommended and already the cursor's row.
func (m *uiModel) ctxPickBox() string {
	p := m.ctxPick
	inner := m.floatingWidth() - panelBorder
	var entries [][]string
	for i, n := range p.sizes {
		label := fmt.Sprintf(i18n.T("restart llama-server with -c %d"), n)
		if i == 0 {
			label += i18n.T(" (recommended)")
		}
		marker, style := "   ", lipgloss.NewStyle()
		switch {
		case i == p.list.sel:
			marker = " ▸ "
		case i == 0:
			style = styleTool
		default:
			style = styleHint
		}
		rows := wrapIndent(label, inner, marker, "     ")
		if i != p.list.sel {
			for j := range rows {
				rows[j] = style.Render(rows[j])
			}
		}
		entries = append(entries, rows)
	}
	return m.floatingPanel(
		i18n.T("the turn outgrew the context — enter restarts llama-server, esc stays"),
		i18n.T("enter — restart with the size above"),
		entries, &p.list)
}
