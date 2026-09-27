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
	"log"

	"dmcode/internal/config"
	"dmcode/internal/discover"
	"dmcode/internal/tools"
	"dmcode/internal/ui"
)

func main() {
	config.LoadDotEnv()
	ctx := context.Background()

	pool, err := discover.DetectProviders()
	if err != nil {
		log.Fatal(err)
	}

	agentTools, err := tools.MakeTools()
	if err != nil {
		log.Fatal(err)
	}

	var toolNames []string
	for _, t := range agentTools {
		toolNames = append(toolNames, t.Name())
	}
	// When the user configured an endpoint the pool holds just that one: the
	// free endpoints join it later, and only if it fails, so a working key
	// never pays for a probe of candidates it does not need.
	if err := ui.RunTUI(ctx, pool[0], pool, agentTools, toolNames); err != nil {
		log.Fatal(err)
	}
}
