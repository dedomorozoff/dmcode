// Package mcp connects dmcode to external MCP (Model Context Protocol)
// servers. A server is configured in a JSON file — either the user's
// ~/.dmcode/mcp.json or a workspace .mcp.json — and its tools join the
// agent's instrument set for the session.
//
// The transport is one of two kinds, following the format the other agent
// CLIs use, so an existing mcpServers block can be pasted as-is:
//
//	{
//	  "mcpServers": {
//	    "filesystem": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]},
//	    "remote": {"url": "https://example.com/mcp"}
//	  }
//	}
//
// A "command" server runs as a child process speaking MCP over stdio; a "url"
// server is reached over streamable HTTP. The connections are lazy — a server
// is not started until the first turn asks for its tools — and reconnect on
// their own when one dies between turns.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/mcptoolset"
)

// Server is one configured MCP server. A Server with neither a command nor a
// URL is skipped, not an error: one broken entry should not take the others
// down.
type Server struct {
	// Command runs a stdio server: the executable, its arguments, and any
	// environment to add on top of the session's own.
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// URL reaches a streamable HTTP server.
	URL string `json:"url,omitempty"`
}

// listTimeout bounds how long one server may take to answer a tools list.
// A server that cannot say what it offers in half a minute is not going to
// answer a tool call either, and the session must start regardless.
const listTimeout = 30 * time.Second

// fileFormat is the on-disk shape: the mcpServers key wraps the map so the
// file can grow other keys without breaking old readers.
type fileFormat struct {
	MCPServers map[string]Server `json:"mcpServers"`
}

// Load reads every config file that exists and merges them, the workspace
// file winning on a server name collision: the project is what the session is
// working on, so its servers are the ones the names mean. Missing files are
// not errors — most sessions have neither file.
//
// workspace is the directory to read .mcp.json from; pass "" to read only the
// home config. The returned notes are one line per file that exists but
// cannot be used, for the session to show the user.
func Load(workspace string) (map[string]Server, []string) {
	servers := map[string]Server{}
	var notes []string

	if home, err := os.UserHomeDir(); err == nil {
		s, errs := loadFile(filepath.Join(home, ".dmcode", "mcp.json"))
		notes = append(notes, errs...)
		for name, srv := range s {
			servers[name] = srv
		}
	}
	if workspace != "" {
		s, errs := loadFile(filepath.Join(workspace, ".mcp.json"))
		notes = append(notes, errs...)
		for name, srv := range s {
			servers[name] = srv
		}
	}

	for name, srv := range servers {
		if !usable(srv) {
			delete(servers, name)
			notes = append(notes, fmt.Sprintf("mcp: server %q has neither a command nor a url; skipping", name))
		}
	}
	return servers, notes
}

// loadFile reads one config file. A file that exists but does not parse is
// reported, not fatal: the other file may still be fine.
func loadFile(path string) (map[string]Server, []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, []string{fmt.Sprintf("mcp: could not read %s: %v", path, err)}
		}
		return nil, nil
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, []string{fmt.Sprintf("mcp: %s is not valid JSON: %v", path, err)}
	}
	return f.MCPServers, nil
}

// usable reports whether a Server names a transport at all.
func usable(s Server) bool {
	return s.Command != "" || s.URL != ""
}

// Toolsets builds one ADK toolset per configured server. The connection to a
// server is opened on the first turn that asks for its tools, so a slow or
// missing server costs nothing until it is used — the toolsets are expanded
// into the request by the ADK flow, the same path the built-in tools travel.
//
// A stdio server is started with the session's environment plus whatever the
// config adds: a server that needs a token reads it from env, not from the
// transcript.
func Toolsets(servers map[string]Server) []tool.Toolset {
	if len(servers) == 0 {
		return nil
	}
	out := make([]tool.Toolset, 0, len(servers))
	for _, name := range sortedNames(servers) {
		srv := servers[name]
		// Load already drops the unusable entries, but Toolsets is exported:
		// a caller that skipped Load gets a skip here too, not a toolset
		// pointed at nothing.
		if !usable(srv) {
			continue
		}
		ts, err := mcptoolset.New(mcptoolset.Config{Transport: transportFor(srv)})
		if err != nil {
			continue
		}
		out = append(out, ts)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ServerState is what one configured server turned out to be.
//
// The three fields are not three answers to one question, and which of them is
// empty is the answer: Tools empty with Err set is a server that could not be
// asked, and Tools empty with Err nil is a server that answered and offers
// nothing. A caller that only had a tool count could not tell those apart, and
// the difference is the whole reason for asking.
type ServerState struct {
	// Name is the key the server is configured under.
	Name string
	// Tools are the names it offered, in the order it listed them.
	Tools []string
	// Err is why it could not be asked, or nil.
	Err error
	// CloseErr is a failure to close the listing session. It does not stop the
	// tools being reported: the answer arrived, and a session left open behind
	// it is this function's problem rather than the server's.
	CloseErr error
}

// States connects to every server, asks it what it offers, and closes. It is
// the eager half of Toolsets: the sidebar wants to show what came of each
// server, while the toolsets themselves open their real connection lazily, so
// nothing keeps a server alive merely because the session started.
//
// Every configured server appears in the result, in name order, whether it
// answered or not. A server that failed here still keeps its toolset — it may
// be up by the time a turn runs — but the user is told, because a silent gap
// in the sidebar would read as "this server has no tools", which is a claim.
func States(ctx context.Context, servers map[string]Server) []ServerState {
	out := make([]ServerState, 0, len(servers))
	for _, name := range sortedNames(servers) {
		st := ServerState{Name: name}
		listCtx, cancel := context.WithTimeout(ctx, listTimeout)
		client := mcp.NewClient(&mcp.Implementation{Name: "dmcode", Version: "1"}, nil)
		session, err := client.Connect(listCtx, transportFor(servers[name]), nil)
		if err != nil {
			cancel()
			st.Err = err
			out = append(out, st)
			continue
		}
		res, err := session.ListTools(listCtx, nil)
		st.CloseErr = session.Close()
		cancel()
		if err != nil {
			st.Err = err
			out = append(out, st)
			continue
		}
		for _, t := range res.Tools {
			st.Tools = append(st.Tools, t.Name)
		}
		out = append(out, st)
	}
	return out
}

// transportFor builds the transport one server speaks over. Both the lazy
// toolsets and the eager listing go through it, so the two can never disagree
// about how a server is reached.
func transportFor(srv Server) mcp.Transport {
	if srv.Command != "" {
		cmd := exec.Command(srv.Command, srv.Args...)
		if len(srv.Env) > 0 {
			cmd.Env = append(os.Environ(), envPairs(srv.Env)...)
		}
		return &mcp.CommandTransport{Command: cmd}
	}
	return &mcp.StreamableClientTransport{Endpoint: srv.URL}
}

// envPairs flattens an env map the way os.Environ writes it.
func envPairs(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// sortedNames returns the server names in a stable order: the config is
// user-authored, and the order should not depend on map iteration.
func sortedNames(servers map[string]Server) []string {
	out := make([]string, 0, len(servers))
	for name := range servers {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
