package agent

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/dedomorozoff/dmcode/internal/ask"
	"github.com/dedomorozoff/dmcode/internal/config"
)

// named builds a no-op tool with a given name, so a test can describe an
// instrument set without building the real ones.
func named(t *testing.T, name string) tool.Tool {
	t.Helper()
	tl, err := functiontool.New(functiontool.Config{Name: name}, func(agent.Context, struct{}) (string, error) {
		return "", nil
	})
	if err != nil {
		t.Fatalf("build %q: %v", name, err)
	}
	return tl
}

func names(ts []tool.Tool) map[string]bool {
	out := make(map[string]bool, len(ts))
	for _, t := range ts {
		out[t.Name()] = true
	}
	return out
}

// TestSubAgentCannotDelegateFurther: recursion buys nothing here and turns one
// question into an unbounded tree of them, each with its own context window.
func TestSubAgentCannotDelegateFurther(t *testing.T) {
	askTool, err := ask.NewBroker(0).MakeTool()
	if err != nil {
		t.Fatal(err)
	}
	set := []tool.Tool{
		named(t, "read_file"),
		named(t, SubAgentName),
		askTool,
	}
	h := &subRunner{readOnly: subToolSet(set)}

	got := names(h.readOnly)
	if got[SubAgentName] {
		t.Error("a sub-agent was given the delegation tool")
	}
	if got[ask.Name] {
		t.Error("a sub-agent was given the question tool — it would put a prompt on screen whose context the user cannot see")
	}
	if !got["read_file"] {
		t.Error("a sub-agent lost its read-only tools")
	}
}

// TestSubAgentToolIsNamed: the sidebar lists what the agent can reach.
func TestSubAgentToolIsNamed(t *testing.T) {
	tl, err := SubAgentTool(nil, []tool.Tool{named(t, "read_file")}, nil)
	if err != nil {
		t.Fatalf("SubAgentTool: %v", err)
	}
	if tl.Name() != SubAgentName {
		t.Errorf("tool name = %q, want %q", tl.Name(), SubAgentName)
	}
}

// TestSubAgentWithNoProviderReportsRatherThanPanics: main builds this tool
// before the pool is known in a test, and a panic there would take down the
// startup path.
func TestSubAgentWithNoProviderReportsRatherThanPanics(t *testing.T) {
	h := &subRunner{timeout: subDefaultTimeout}
	if _, err := h.buildModel(context.Background()); err == nil {
		t.Error("buildModel reported success with no provider configured")
	}
}

// TestReportIsTrimmedAndMarked: a parent that reads a cut report as a whole one
// will act on a list that stops halfway.
func TestReportIsTrimmedAndMarked(t *testing.T) {
	got := trimReport(strings.Repeat("я", subMaxReportChars+100))
	if len([]rune(got)) <= subMaxReportChars {
		t.Errorf("the report was not cut: %d characters", len([]rune(got)))
	}
	if !strings.Contains(got, "truncated") {
		t.Error("the cut is not marked, so a truncated report reads as a complete one")
	}
}

// TestPreviewIsOneLine: the task is shown in a one-row transcript line, and a
// multi-line task would push the progress rows off the panel.
func TestPreviewIsOneLine(t *testing.T) {
	if got := subPreview("первая строка\nвторая строка"); strings.Contains(got, "\n") {
		t.Errorf("preview %q still has a line break", got)
	}
	long := subPreview(strings.Repeat("я", 200))
	if len([]rune(long)) > subTaskPreview+1 {
		t.Errorf("preview is %d characters, want it cut to %d", len([]rune(long)), subTaskPreview)
	}
}

// TestSortedKeysIsStable: the file list goes back to the model, and map order
// would make two identical delegations look different.
func TestSortedKeysIsStable(t *testing.T) {
	m := map[string]bool{"b.go": true, "a.go": true, "c.go": true}
	for range 20 {
		got := sortedKeys(m)
		if got[0] != "a.go" || got[1] != "b.go" || got[2] != "c.go" {
			t.Fatalf("sortedKeys = %v, want a stable order", got)
		}
	}
}

// TestNotifierIsOptional: the tool is built before the program exists, so a nil
// notifier is the normal case, not a fault.
func TestNotifierIsOptional(t *testing.T) {
	h := &subRunner{timeout: subDefaultTimeout}
	h.say("задача", "что-то") // must not panic
}

var _ = config.Provider{}
