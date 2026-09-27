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

	"github.com/dedomorozoff/dmcode/internal/config"
	"github.com/dedomorozoff/dmcode/internal/discover"
	"github.com/dedomorozoff/dmcode/internal/tools"
	"github.com/dedomorozoff/dmcode/internal/ui"
)

// version is stamped at build time via -ldflags "-X main.version=..." (see the
// Makefile). Release tags set it; a plain "go build" leaves it as "dev".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the dmcode version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("dmcode", version)
		return
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	config.LoadDotEnv()
	ctx := context.Background()

	pool, err := discover.DetectProviders()
	if err != nil {
		return err
	}

	agentTools, err := tools.MakeTools()
	if err != nil {
		return err
	}

	var toolNames []string
	for _, t := range agentTools {
		toolNames = append(toolNames, t.Name())
	}
	// When the user configured an endpoint the pool holds just that one: the
	// free endpoints join it later, and only if it fails, so a working key
	// never pays for a probe of candidates it does not need.
	return ui.RunTUI(ctx, pool[0], pool, agentTools, toolNames)
}
