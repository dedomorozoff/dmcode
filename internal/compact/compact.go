// Package compact is the manual half of context compaction.
//
// ADK compacts automatically when a prompt crosses the model's threshold. That
// is the right default and the wrong only answer: a user who knows the next
// question needs the whole window should be able to pay for a summary now,
// rather than wait for the threshold or start a new session. This package
// builds the same kind of summary event ADK's tail-retention path builds, using
// the same public summarizer, and appends it to the store.
package compact

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/compaction"
	"google.golang.org/genai"
)

// ErrNothing means there was not enough history to summarize, or the
// summarizer declined. It is not a failure: the caller says so and leaves the
// conversation alone.
var ErrNothing = errors.New("nothing to compact")

// Result describes one successful compaction.
type Result struct {
	Covered  int
	Retained int
	Summary  string
	EventID  string
}

// Now summarizes all but the newest retention events and appends the summary
// to the session. m must be the model the session is actually running on; a
// summary written by a different model is a different conversation's notes.
func Now(ctx context.Context, svc session.Service, appName, userID, sessionID string, m model.LLM, retention int) (Result, error) {
	if svc == nil {
		return Result{}, fmt.Errorf("session service is required")
	}
	if m == nil {
		return Result{}, fmt.Errorf("model is required")
	}
	if retention <= 0 {
		retention = 8
	}
	resp, err := svc.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		return Result{}, err
	}
	if resp == nil || resp.Session == nil {
		return Result{}, fmt.Errorf("session %q was not found", sessionID)
	}
	events := collect(resp.Session.Events())
	if len(events) <= retention+1 {
		return Result{}, ErrNothing
	}
	window := events[:len(events)-retention]
	if len(window) == 0 {
		return Result{}, ErrNothing
	}

	summarizer, err := compaction.NewLLMSummarizer(compaction.LLMSummarizerConfig{
		Model:   m,
		Timeout: 90 * time.Second,
	})
	if err != nil {
		return Result{}, err
	}
	out, err := summarizer.SummarizeEvents(ctx, window)
	if err != nil {
		return Result{}, err
	}
	if out.Content == nil {
		return Result{}, ErrNothing
	}

	content := proseOnly(out.Content)
	if content == nil {
		return Result{}, ErrNothing
	}
	start, end := span(window)
	ev := &session.Event{
		Author:         "user",
		Branch:         window[0].Branch,
		IsolationScope: window[0].IsolationScope,
		InvocationID:   fmt.Sprintf("manual-compact-%d", time.Now().UnixNano()),
		Actions: session.EventActions{Compaction: &session.EventCompaction{
			StartTimestamp:   start,
			EndTimestamp:     end,
			CompactedContent: content,
			ExcludedEvents:   excludedRefs(window, events, start, end),
		}},
		LLMResponse: model.LLMResponse{UsageMetadata: out.Usage},
	}
	if err := svc.AppendEvent(ctx, resp.Session, ev); err != nil {
		return Result{}, err
	}
	return Result{
		Covered:  len(window),
		Retained: retention,
		Summary:  textOf(content),
		EventID:  ev.ID,
	}, nil
}

func collect(events session.Events) []*session.Event {
	if events == nil {
		return nil
	}
	out := make([]*session.Event, 0, events.Len())
	for ev := range events.All() {
		if ev != nil {
			out = append(out, ev)
		}
	}
	return out
}

func span(events []*session.Event) (time.Time, time.Time) {
	start, end := events[0].Timestamp, events[0].Timestamp
	for _, ev := range events[1:] {
		if ev.Timestamp.Before(start) {
			start = ev.Timestamp
		}
		if ev.Timestamp.After(end) {
			end = ev.Timestamp
		}
	}
	return start, end
}

// excludedRefs names the events inside the summary's timestamp range that the
// summary does not stand in for. The window is a prefix of the append order,
// not a timestamp slice, so a concurrently appended event can have a timestamp
// inside the span while sitting outside the window; without this list the
// summary would claim it and the event would vanish from later prompts.
func excludedRefs(window, all []*session.Event, start, end time.Time) []session.EventRef {
	inWindow := make(map[*session.Event]bool, len(window))
	for _, ev := range window {
		inWindow[ev] = true
	}
	var out []session.EventRef
	seen := map[string]bool{}
	for _, ev := range all {
		if ev == nil || inWindow[ev] || ev.Actions.Compaction != nil {
			continue
		}
		if ev.Timestamp.Before(start) || ev.Timestamp.After(end) {
			continue
		}
		k := ev.InvocationID + "|" + ev.Timestamp.String()
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, session.EventRef{InvocationID: ev.InvocationID, Timestamp: ev.Timestamp})
	}
	return out
}

// proseOnly mirrors what ADK stores: a summary is prose, never a tool call or
// a thought, because the stored content is replayed into later prompts as
// though the framework had produced it.
func proseOnly(c *genai.Content) *genai.Content {
	if c == nil {
		return nil
	}
	out := &genai.Content{Role: "model"}
	for _, p := range c.Parts {
		if p == nil || p.Thought || strings.TrimSpace(p.Text) == "" {
			continue
		}
		part := *p
		part.ThoughtSignature = nil
		out.Parts = append(out.Parts, &part)
	}
	if len(out.Parts) == 0 {
		return nil
	}
	return out
}

func textOf(c *genai.Content) string {
	var parts []string
	for _, p := range c.Parts {
		if p != nil && p.Text != "" {
			parts = append(parts, p.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
