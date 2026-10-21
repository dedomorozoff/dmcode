package tools

import (
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/dedomorozoff/dmcode/internal/rag"
)

type ragIndexArgs struct {
	IncludeSessions bool `json:"include_sessions,omitempty" jsonschema:"Also index saved conversations. Leave it out to index only the workspace; conversations can be large and private."`
}

type ragIndexResult struct {
	Files    int    `json:"files"`
	Sessions int    `json:"sessions"`
	Chunks   int    `json:"chunks"`
	Bytes    int    `json:"bytes"`
	Skipped  int    `json:"skipped"`
	Index    string `json:"index"`
}

func makeRAGIndexTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "rag_index",
		Description: "Builds or rebuilds the local retrieval index for the workspace, so rag_search can find code and notes by keyword. " +
			"Set include_sessions=true to also index saved conversations. The index is a cache under ~/.dmcode/rag; it never changes the workspace.",
	}, func(ctx agent.Context, in ragIndexArgs) (ragIndexResult, error) {
		root := Root()
		if root == "" {
			return ragIndexResult{}, fmt.Errorf("the workspace root is not set")
		}
		idx, st, err := rag.Build(ctx, root, rag.Options{IncludeSessions: in.IncludeSessions})
		if err != nil {
			return ragIndexResult{}, err
		}
		if err := rag.Save(idx); err != nil {
			return ragIndexResult{}, err
		}
		return ragIndexResult{
			Files: st.Files, Sessions: st.Sessions, Chunks: st.Chunks,
			Bytes: st.Bytes, Skipped: st.Skipped, Index: rag.IndexPath(root),
		}, nil
	})
}

type ragSearchArgs struct {
	Query string `json:"query" jsonschema:"What to look for, in natural language or code identifiers. BM25 matches words, so include the terms you expect to see."`
	Limit int    `json:"limit,omitempty" jsonschema:"How many chunks to return at most. Default 5, maximum 20."`
}

type ragHit struct {
	Path      string  `json:"path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float64 `json:"score"`
	Text      string  `json:"text"`
}

type ragSearchResult struct {
	Query   string   `json:"query"`
	Results []ragHit `json:"results"`
	Note    string   `json:"note,omitempty"`
}

func makeRAGSearchTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "rag_search",
		Description: "Searches the local retrieval index built by rag_index. Returns the best-matching chunks with their path and line range. " +
			"Call rag_index first when the index is missing or stale; rag_search never rebuilds it.",
	}, func(_ agent.Context, in ragSearchArgs) (ragSearchResult, error) {
		root := Root()
		if root == "" {
			return ragSearchResult{}, fmt.Errorf("the workspace root is not set")
		}
		idx, err := rag.Load(root)
		if err != nil {
			return ragSearchResult{}, fmt.Errorf("no usable index for this workspace: %w — call rag_index first", err)
		}
		hits := rag.Search(idx, in.Query, in.Limit)
		out := ragSearchResult{Query: in.Query}
		for _, h := range hits {
			out.Results = append(out.Results, ragHit{
				Path: h.Path, StartLine: h.StartLine, EndLine: h.EndLine,
				Score: h.Score, Text: truncate(strings.TrimSpace(h.Text), 2500),
			})
		}
		if len(out.Results) == 0 {
			out.Note = "no chunks matched; try different terms, or call rag_index if the workspace changed"
		}
		return out, nil
	})
}
