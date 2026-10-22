package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/adk/v2/session"

	"github.com/dedomorozoff/dmcode/internal/memsession"
)

// maxSessionTurns is how much of one conversation is indexed. A session that
// ran for days is still represented by its most recent exchanges; indexing
// every turn of every session is a rebuild nobody waits for.
const maxSessionTurns = 1000

// loadSessions turns saved conversations into chunks. It reads through the
// same store the UI uses, so a session that is visible in /sessions is the one
// the index sees.
func loadSessions(ctx context.Context, dir string, remaining int) ([]Chunk, int, error) {
	if dir == "" || remaining <= 0 {
		return nil, 0, nil
	}
	svc := memsession.NewPersistent(dir)
	svc.Discover(ctx, "dmcode", "user")
	resp, err := svc.List(ctx, &session.ListRequest{AppName: "dmcode", UserID: "user"})
	if err != nil {
		return nil, 0, err
	}
	sessions := append([]session.Session(nil), resp.Sessions...)
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].LastUpdateTime().After(sessions[j].LastUpdateTime())
	})

	var out []Chunk
	count := 0
	for _, sess := range sessions {
		if len(out) >= remaining {
			break
		}
		tr, err := svc.TranscriptOf("dmcode", "user", sess.ID(), maxSessionTurns)
		if err != nil {
			continue
		}
		text := sessionText(tr)
		if strings.TrimSpace(text) == "" {
			continue
		}
		chunks := chunkText("session:"+sess.ID(), text, 80, 10)
		if len(chunks) == 0 {
			continue
		}
		out = append(out, chunks...)
		count++
	}
	if len(out) > remaining {
		out = out[:remaining]
	}
	return out, count, nil
}

func sessionText(tr memsession.Transcript) string {
	var b strings.Builder
	title := strings.TrimSpace(tr.Title)
	if title == "" {
		title = "untitled"
	}
	fmt.Fprintf(&b, "Session: %s (%s)\n", title, tr.ID)
	for _, turn := range tr.Turns {
		if strings.TrimSpace(turn.User) != "" {
			fmt.Fprintf(&b, "\nUser: %s\n", turn.User)
		}
		for _, e := range turn.Entries {
			switch e.Kind {
			case memsession.EntryAgent:
				if strings.TrimSpace(e.Text) != "" {
					fmt.Fprintf(&b, "\nAssistant: %s\n", e.Text)
				}
			case memsession.EntryTool:
				if e.Call != nil {
					args, _ := json.Marshal(e.Call.Args)
					fmt.Fprintf(&b, "\nTool call: %s %s\n", e.Call.Name, clip(string(args), 2000))
				}
			case memsession.EntryToolResult:
				if e.Result != nil {
					resp, _ := json.Marshal(e.Result.Response)
					fmt.Fprintf(&b, "\nTool result: %s\n", clip(string(resp), 2000))
				}
			}
		}
	}
	return b.String()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
