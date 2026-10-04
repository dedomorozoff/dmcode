// dmcode — coding agent built on google/adk-go.
//
// The wiring lives in the internal packages: config owns the endpoint and the
// .env file, discover finds the providers the session can run on, llm speaks
// the OpenAI chat wire and pools endpoints for failover, agent assembles the
// coding agent, tools implements its instruments, and ui is the terminal
// interface. This file only chains them together.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	dmagent "github.com/dedomorozoff/dmcode/internal/agent"
	"github.com/dedomorozoff/dmcode/internal/ask"
	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/discover"
	"github.com/dedomorozoff/dmcode/internal/editor/editor"
	"github.com/dedomorozoff/dmcode/internal/i18n"
	dmmcp "github.com/dedomorozoff/dmcode/internal/mcp"
	"github.com/dedomorozoff/dmcode/internal/memsession"
	"github.com/dedomorozoff/dmcode/internal/tools"
	"github.com/dedomorozoff/dmcode/internal/ui"
	"google.golang.org/adk/v2/tool"
)

// version is stamped at build time via -ldflags "-X main.version=..." (see the
// Makefile). Release tags set it; a plain "go build" leaves it as "dev".
//
// dev is the honest default: it says the binary was not stamped by a release,
// which is what a build someone made to try a change should say about itself.
var version = "dev"

// subSend is how a delegation reaches the event loop. The tool is built before
// the program exists, so the sender is attached afterwards; a turn cannot start
// before that, so the nil is never seen by a running delegation.
var (
	subSendMu sync.RWMutex
	subSend   func(dmagent.SubEvent)
)

// bindSubNotifier records the sender the UI offers once its program is up.
func bindSubNotifier(send func(dmagent.SubEvent)) {
	subSendMu.Lock()
	defer subSendMu.Unlock()
	subSend = send
}

// subNotifier is what the delegation tool calls. It is nil-safe for the same
// reason the other notifiers are.
func subNotifier(ev dmagent.SubEvent) {
	subSendMu.RLock()
	send := subSend
	subSendMu.RUnlock()
	if send != nil {
		send(ev)
	}
}

func main() {
	// "dmcode editor [dir | files...]" is the same door as -e, spelled the way
	// dmed spelled it, so old muscle memory and old scripts keep working.
	if len(os.Args) > 1 && os.Args[1] == "editor" {
		if err := runEditor(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	showVersion := flag.Bool("version", false, "print the dmcode version and exit")
	dir := flag.String("C", "", "work as if dmcode was started in this directory")
	flag.StringVar(dir, "dir", "", "alias for -C")
	edit := flag.Bool("e", false, "open the file editor instead of the agent session")
	// -s takes an *optional* value, which Go's flag package cannot express: a
	// string flag demands an argument and a bool flag refuses one. So it is taken
	// out of the arguments here and handed to run, the same door "dmcode editor"
	// gets.
	rest, want := takeResumeFlag(os.Args[1:])
	os.Args = append([]string{os.Args[0]}, rest...)
	flag.Parse()
	if *showVersion {
		fmt.Println("dmcode", version)
		return
	}
	if *edit {
		if err := runEditor(flag.Args()); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(*dir, want); err != nil {
		log.Fatal(err)
	}
}

// resumeFlag is what -s asked for. Asked and ID are separate because the two
// cases are different requests: "this session" names one, and a bare -s means
// "wherever I was", which only becomes an id once the store has been read.
type resumeFlag struct {
	Asked bool
	ID    string
}

// takeResumeFlag removes -s / --session from args and reports what it wanted.
//
// Bare, it asks for the newest session; given an id, that one. A following
// argument that looks like a flag is left alone, so "dmcode -s -C ~/project"
// resumes the newest session *and* changes directory rather than reading "-C" as
// a session id.
func takeResumeFlag(args []string) (rest []string, want resumeFlag) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-s" || a == "--session":
			want.Asked = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				want.ID = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--session="):
			want.Asked, want.ID = true, strings.TrimPrefix(a, "--session=")
		case strings.HasPrefix(a, "-s="):
			want.Asked, want.ID = true, strings.TrimPrefix(a, "-s=")
		default:
			rest = append(rest, a)
		}
	}
	return rest, want
}

// runEditor starts the merged dmed editor on the given paths. The editor is
// a full Bubble Tea program of its own, so it never shares a process with the
// agent TUI: one runs, the other does not exist.
func runEditor(args []string) error {
	model := editor.New(args...)
	model.ApplyTerminalCompat()
	p := tea.NewProgram(model)
	_, err := p.Run()
	return err
}

// run starts the session, optionally relocated to dir.
//
// The move has to happen before LoadDotEnv: the provider lives in the .env of
// the directory the user pointed us at, and reading the old one first would
// configure the session against the wrong project. Chdir also keeps the tools —
// which address files relative to the process directory — in step with what the
// user asked for, instead of leaving the two disagreeing.
func run(dir string, want resumeFlag) error {
	if dir != "" {
		if err := os.Chdir(dir); err != nil {
			return fmt.Errorf("cannot start in %s: %w", dir, err)
		}
	}
	if want.Asked && want.ID == "" {
		// Settled here rather than inside the UI, because the runner is built
		// around a session id and a session opened after that would be pointed at
		// the wrong one. A store with nothing in it is not a failure to open a
		// fresh conversation, so an empty answer just means the start is a new
		// session.
		want.ID = memsession.NewestSessionID(config.SessionsDir())
	}
	// Confines every tool path to the session's directory. A failure here is not
	// fatal: the agent still works, it simply is not fenced in.
	if err := tools.SetRoot(mustGetwd()); err != nil {
		fmt.Fprintln(os.Stderr, "dmcode: "+err.Error())
	}
	config.LoadDotEnv()
	// The sidebar's brand block shows the version; it lives in config because
	// the UI cannot reach into package main, and main is the only place the
	// linker-stamped value exists.
	config.Version = version
	// Before anything renders, so even the setup wizard speaks the saved language.
	i18n.Init()
	ctx := context.Background()

	pool, err := discover.DetectProviders()
	if err != nil {
		return err
	}
	// A GGUF session runs its own llama-server; it must not outlive the TUI.
	defer discover.StopGGUF()

	agentTools, err := tools.MakeTools()
	if err != nil {
		return err
	}
	// The read-only set is built once up front so switching into plan mode is
	// instant and cannot fail halfway through a turn.
	readOnlyTools, err := tools.MakeReadOnlyTools()
	if err != nil {
		return err
	}

	// The question tool is built here and in both sets: a decision is not an
	// edit, so plan mode may ask as well. The broker is handed to the UI, which
	// registers the side that shows the overlay.
	broker := ask.NewBroker(config.AskTimeout())
	askTool, err := broker.MakeTool()
	if err != nil {
		return err
	}
	agentTools = append(agentTools, askTool)
	readOnlyTools = append(readOnlyTools, askTool)

	// The delegation tool is built against the read-only set: a sub-agent that
	// could write would be a second agent editing the workspace while the user
	// watches one turn. It goes in both sets because delegating research is
	// exactly what plan mode needs.
	subTool, err := dmagent.SubAgentTool(pool, readOnlyTools, subNotifier)
	if err != nil {
		return err
	}
	agentTools = append(agentTools, subTool)
	readOnlyTools = append(readOnlyTools, subTool)

	// MCP servers join both sets the way the delegation tool does: they are
	// configured explicitly, in the user's or the project's own file, and a
	// server's tools are resolved lazily per turn rather than frozen in here.
	// The listing is eager only to put names in the sidebar and to be able to
	// tell the user which server did not come up.
	// Yolo is act's reach with the question tool taken away, so a long job is
	// never punctuated by a question. It is built here rather than filtered in
	// the UI because main is the only place that still knows which tool in the
	// set is the question tool — the same reason the delegation tool is built
	// here. It is a separate slice rather than a view onto agentTools so that a
	// later append to one cannot quietly hand the question back to yolo.
	yoloTools := make([]tool.Tool, 0, len(agentTools))
	for _, t := range agentTools {
		if t.Name() == ask.Name {
			continue
		}
		yoloTools = append(yoloTools, t)
	}

	mcpServers, mcpNotes := dmmcp.Load(mustGetwd())
	mcpToolsets := dmmcp.Toolsets(mcpServers)
	var mcpNames []string
	if len(mcpServers) > 0 {
		names, moreNotes := dmmcp.List(ctx, mcpServers)
		mcpNames = names
		mcpNotes = append(mcpNotes, moreNotes...)
	}

	toolNames := tools.ToolNames(agentTools)
	toolNames = append(toolNames, mcpNames...)
	// When the user configured an endpoint the pool holds just that one: the
	// free endpoints join it later, and only if it fails, so a working key
	// never pays for a probe of candidates it does not need.
	return ui.RunTUI(ctx, pool[0], pool, agentTools, readOnlyTools, toolNames, mcpToolsets, mcpNotes, broker, bindSubNotifier, yoloTools, ui.Resume{Asked: want.Asked, ID: want.ID})
}

// mustGetwd returns the current directory, or "." when the platform refuses to
// say — SetRoot then resolves it against the same directory the tools use.
func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return "."
	}
	return wd
}
