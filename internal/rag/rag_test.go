package rag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dedomorozoff/dmcode/internal/memsession"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSearchSaveAndLoad(t *testing.T) {
	t.Setenv("DMCODE_RAG_DIR", t.TempDir())
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc ParseFile(name string) error {\n\treturn nil\n}\n")
	writeFile(t, filepath.Join(root, "notes.md"), "A note about the needle in the haystack.\n")
	writeFile(t, filepath.Join(root, "binary.bin"), "before\x00after")

	idx, st, err := Build(context.Background(), root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 2 {
		t.Errorf("Files = %d, want 2 (binary must be skipped)", st.Files)
	}
	if st.Chunks == 0 {
		t.Fatal("no chunks were indexed")
	}
	if len(idx.Chunks) != st.Chunks {
		t.Errorf("index has %d chunks, stats say %d", len(idx.Chunks), st.Chunks)
	}
	for _, c := range idx.Chunks {
		if c.Path == "binary.bin" {
			t.Error("a binary file was indexed")
		}
	}

	hits := Search(idx, "ParseFile", 5)
	if len(hits) == 0 || hits[0].Path != "main.go" {
		t.Fatalf("ParseFile search = %+v, want main.go", hits)
	}
	hits = Search(idx, "parse file", 5)
	if len(hits) == 0 || hits[0].Path != "main.go" {
		t.Fatalf("parse file search = %+v, want main.go", hits)
	}
	hits = Search(idx, "needle", 5)
	if len(hits) == 0 || hits[0].Path != "notes.md" {
		t.Fatalf("needle search = %+v, want notes.md", hits)
	}
	if got := Search(idx, "", 5); got != nil {
		t.Errorf("empty query returned %+v", got)
	}

	if err := Save(idx); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Chunks) != len(idx.Chunks) {
		t.Errorf("loaded %d chunks, want %d", len(loaded.Chunks), len(idx.Chunks))
	}
	if !strings.HasSuffix(IndexPath(root), ".json") {
		t.Errorf("IndexPath = %q", IndexPath(root))
	}
}

func TestBuildIndexesSavedSessions(t *testing.T) {
	t.Setenv("DMCODE_RAG_DIR", t.TempDir())
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\n")
	sessionsDir := t.TempDir()

	svc := memsession.NewPersistent(sessionsDir)
	ctx := context.Background()
	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "dmcode", UserID: "user", SessionID: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	user := session.NewEvent(ctx, "inv-1")
	user.Author = "user"
	user.Content = genai.NewContentFromText("How does the parser open a file?", genai.RoleUser)
	if err := svc.AppendEvent(ctx, resp.Session, user); err != nil {
		t.Fatal(err)
	}
	agent := session.NewEvent(ctx, "inv-2")
	agent.Author = "dmcode"
	agent.Content = genai.NewContentFromText("It calls ParseFile with the name.", genai.RoleModel)
	if err := svc.AppendEvent(ctx, resp.Session, agent); err != nil {
		t.Fatal(err)
	}

	idx, st, err := Build(ctx, root, Options{IncludeSessions: true, SessionsDir: sessionsDir})
	if err != nil {
		t.Fatal(err)
	}
	if st.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1", st.Sessions)
	}
	found := false
	for _, c := range idx.Chunks {
		if c.Path == "session:s1" && strings.Contains(c.Text, "ParseFile") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("saved conversation was not indexed: %+v", idx.Chunks)
	}
	if hits := Search(idx, "parser", 5); len(hits) == 0 {
		t.Error("a query for the saved conversation returned nothing")
	}
}
