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
	"sync"

	dmagent "github.com/dedomorozoff/dmcode/internal/agent"
	"github.com/dedomorozoff/dmcode/internal/ask"
	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/discover"
	"github.com/dedomorozoff/dmcode/internal/i18n"
	"github.com/dedomorozoff/dmcode/internal/tools"
	"github.com/dedomorozoff/dmcode/internal/ui"
)

// version is stamped at build time via -ldflags "-X main.version=..." (see the
// Makefile). Release tags set it; a plain "go build" leaves it as "dev".
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
	showVersion := flag.Bool("version", false, "print the dmcode version and exit")
	dir := flag.String("C", "", "work as if dmcode was started in this directory")
	flag.StringVar(dir, "dir", "", "alias for -C")
	flag.Parse()
	if *showVersion {
		fmt.Println("dmcode", version)
		return
	}
	if err := run(*dir); err != nil {
		log.Fatal(err)
	}
}

// run starts the session, optionally relocated to dir.
//
// The move has to happen before LoadDotEnv: the provider lives in the .env of
// the directory the user pointed us at, and reading the old one first would
// configure the session against the wrong project. Chdir also keeps the tools —
// which address files relative to the process directory — in step with what the
// user asked for, instead of leaving the two disagreeing.
func run(dir string) error {
	if dir != "" {
		if err := os.Chdir(dir); err != nil {
			return fmt.Errorf("cannot start in %s: %w", dir, err)
		}
	}
	// Confines every tool path to the session's directory. A failure here is not
	// fatal: the agent still works, it simply is not fenced in.
	if err := tools.SetRoot(mustGetwd()); err != nil {
		fmt.Fprintln(os.Stderr, "dmcode: "+err.Error())
	}
	config.LoadDotEnv()
	// Before anything renders, so even the setup wizard speaks the saved language.
	i18n.Init()
	ctx := context.Background()

	pool, err := discover.DetectProviders()
	if err != nil {
		return err
	}

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

	toolNames := tools.ToolNames(agentTools)
	// When the user configured an endpoint the pool holds just that one: the
	// free endpoints join it later, and only if it fails, so a working key
	// never pays for a probe of candidates it does not need.
	return ui.RunTUI(ctx, pool[0], pool, agentTools, readOnlyTools, toolNames, broker, bindSubNotifier)
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
