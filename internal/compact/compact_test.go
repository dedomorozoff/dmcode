package compact

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dedomorozoff/dmcode/internal/memsession"
)

type fakeModel struct{}

func (fakeModel) Name() string { return "fake" }

func (fakeModel) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content: genai.NewContentFromText("summary of the earlier conversation", genai.RoleModel),
		}, nil)
	}
}

func TestNowSummarizesOlderEventsAndAppendsTheSummary(t *testing.T) {
	svc := memsession.NewMemory()
	ctx := context.Background()
	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "dmcode", UserID: "user", SessionID: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		ev := session.NewEvent(ctx, fmt.Sprintf("inv-%d", i))
		if i%2 == 0 {
			ev.Author = "user"
			ev.Content = genai.NewContentFromText(fmt.Sprintf("question %d", i), genai.RoleUser)
		} else {
			ev.Author = "dmcode"
			ev.Content = genai.NewContentFromText(fmt.Sprintf("answer %d", i), genai.RoleModel)
		}
		if err := svc.AppendEvent(ctx, resp.Session, ev); err != nil {
			t.Fatal(err)
		}
	}

	res, err := Now(ctx, svc, "dmcode", "user", "s1", fakeModel{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Covered != 10 || res.Retained != 2 {
		t.Errorf("result = %+v, want 10 covered and 2 retained", res)
	}
	if !strings.Contains(res.Summary, "summary of the earlier conversation") {
		t.Errorf("summary = %q", res.Summary)
	}
	if !svc.HasCompaction("dmcode", "user", "s1") {
		t.Fatal("the store does not report a compaction event")
	}

	got, err := svc.Get(ctx, &session.GetRequest{AppName: "dmcode", UserID: "user", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	var last *session.Event
	for ev := range got.Session.Events().All() {
		last = ev
	}
	if last == nil || last.Actions.Compaction == nil {
		t.Fatalf("last event is not a compaction: %+v", last)
	}
	compacted := last.Actions.Compaction.CompactedContent
	if compacted == nil || len(compacted.Parts) == 0 ||
		!strings.Contains(compacted.Parts[0].Text, "summary") {
		t.Errorf("compacted content = %+v", compacted)
	}
}

func TestNowSaysNothingWhenThereIsNothingToCompact(t *testing.T) {
	svc := memsession.NewMemory()
	ctx := context.Background()
	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "dmcode", UserID: "user", SessionID: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ev := session.NewEvent(ctx, "inv-1")
	ev.Author = "user"
	ev.Content = genai.NewContentFromText("only one", genai.RoleUser)
	if err := svc.AppendEvent(ctx, resp.Session, ev); err != nil {
		t.Fatal(err)
	}
	if _, err := Now(ctx, svc, "dmcode", "user", "s1", fakeModel{}, 8); !errors.Is(err, ErrNothing) {
		t.Fatalf("Now = %v, want ErrNothing", err)
	}
}
