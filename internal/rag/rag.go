// Package rag is dmcode's local retrieval index: a BM25 index over the
// workspace's text files and, optionally, the saved conversations.
//
// It is deliberately not a database service. The index is a JSON file under
// ~/.dmcode/rag, one per workspace, rebuilt on request. A coding session needs
// retrieval to work on the machine it is already running on, with no daemon,
// no schema migration and no embedding endpoint that may not exist.
package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/dedomorozoff/dmcode/internal/config"
)

const (
	// indexVersion changes when the stored shape changes in a way an old file
	// cannot be read as. A mismatch is a rebuild, not an error.
	indexVersion = 1

	defaultMaxFileBytes  = 1 << 20
	defaultMaxTotalBytes = 16 << 20
	defaultChunkLines    = 60
	defaultOverlapLines  = 10
	defaultMaxChunks     = 20000
)

// Chunk is one retrievable piece of a file or a conversation.
type Chunk struct {
	// Path is workspace-relative for a file, or session:<id> for a saved
	// conversation.
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Text      string `json:"text"`
}

// Index is the on-disk shape. The BM25 statistics are derived from Chunks when
// the index is loaded, so they are not stored twice.
type Index struct {
	Version int       `json:"version"`
	Root    string    `json:"root"`
	Built   time.Time `json:"built"`
	Chunks  []Chunk   `json:"chunks"`
}

// Stats is what a build did, for the tool result and the transcript.
type Stats struct {
	Files    int `json:"files"`
	Sessions int `json:"sessions"`
	Chunks   int `json:"chunks"`
	Bytes    int `json:"bytes"`
	Skipped  int `json:"skipped"`
}

// Options controls one build. The zero value is the useful default: the
// workspace only, with the standard limits.
type Options struct {
	IncludeSessions bool
	SessionsDir     string
	MaxFileBytes    int64
	MaxTotalBytes   int64
	ChunkLines      int
	OverlapLines    int
	MaxChunks       int
}

func (o Options) withDefaults() Options {
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = defaultMaxFileBytes
	}
	if o.MaxTotalBytes <= 0 {
		o.MaxTotalBytes = defaultMaxTotalBytes
	}
	if o.ChunkLines <= 0 {
		o.ChunkLines = defaultChunkLines
	}
	if o.OverlapLines == 0 {
		o.OverlapLines = defaultOverlapLines
	} else if o.OverlapLines < 0 {
		o.OverlapLines = 0
	}
	if o.OverlapLines >= o.ChunkLines {
		o.OverlapLines = o.ChunkLines / 4
	}
	if o.MaxChunks <= 0 {
		o.MaxChunks = defaultMaxChunks
	}
	return o
}

// Build indexes root. It never follows a symlinked directory: the workspace
// boundary is lexical, and an index that walks through a link is an index of
// somewhere else.
func Build(ctx context.Context, root string, opts Options) (*Index, Stats, error) {
	opts = opts.withDefaults()
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, Stats{}, err
	}
	idx := &Index{Version: indexVersion, Root: abs, Built: time.Now().UTC()}
	var st Stats

	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			st.Skipped++
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if path != abs && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(idx.Chunks) >= opts.MaxChunks {
			return filepath.SkipAll
		}
		if !d.Type().IsRegular() || skipFile(d.Name()) {
			st.Skipped++
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() <= 0 || info.Size() > opts.MaxFileBytes {
			st.Skipped++
			return nil
		}
		if st.Bytes+int(info.Size()) > int(opts.MaxTotalBytes) {
			st.Skipped++
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || isBinary(data) {
			st.Skipped++
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			st.Skipped++
			return nil
		}
		rel = filepath.ToSlash(rel)
		chunks := chunkText(rel, string(data), opts.ChunkLines, opts.OverlapLines)
		if len(chunks) == 0 {
			st.Skipped++
			return nil
		}
		st.Files++
		st.Bytes += len(data)
		idx.Chunks = append(idx.Chunks, chunks...)
		return nil
	})
	if err != nil {
		return nil, st, err
	}

	if opts.IncludeSessions && len(idx.Chunks) < opts.MaxChunks {
		dir := opts.SessionsDir
		if dir == "" {
			dir = config.SessionsDir()
		}
		sessionChunks, sessions, err := loadSessions(ctx, dir, opts.MaxChunks-len(idx.Chunks))
		if err != nil {
			return nil, st, err
		}
		idx.Chunks = append(idx.Chunks, sessionChunks...)
		st.Sessions = sessions
	}

	st.Chunks = len(idx.Chunks)
	return idx, st, nil
}

// chunkText splits a file into line windows with overlap, preserving the line
// numbers a citation needs.
func chunkText(path, text string, chunkLines, overlap int) []Chunk {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	// A trailing newline produces one empty line that is not part of the file.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	var out []Chunk
	step := chunkLines - overlap
	if step < 1 {
		step = 1
	}
	for start := 0; start < len(lines); start += step {
		end := start + chunkLines
		if end > len(lines) {
			end = len(lines)
		}
		body := strings.Join(lines[start:end], "\n")
		if strings.TrimSpace(body) == "" || len(tokenize(body)) == 0 {
			if end == len(lines) {
				break
			}
			continue
		}
		out = append(out, Chunk{
			Path:      path,
			StartLine: start + 1,
			EndLine:   end,
			Text:      body,
		})
		if end == len(lines) {
			break
		}
	}
	return out
}

// Hit is one search result.
type Hit struct {
	Chunk
	Score float64 `json:"score"`
}

// Search returns the highest-scoring chunks for query. A query with no tokens,
// or an empty index, returns nothing.
func Search(idx *Index, query string, limit int) []Hit {
	if idx == nil || len(idx.Chunks) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	r := newRuntime(idx)
	terms := dedupe(tokenize(query))
	if len(terms) == 0 {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var hits []Hit
	for i, chunk := range idx.Chunks {
		score := 0.0
		for _, term := range terms {
			f := r.tfs[i][term]
			if f == 0 {
				continue
			}
			df := r.df[term]
			idf := math.Log(1 + (float64(len(idx.Chunks))-float64(df)+0.5)/(float64(df)+0.5))
			denom := float64(f) + r.k1*(1-r.b+r.b*float64(r.lens[i])/r.avg)
			score += idf * (float64(f) * (r.k1 + 1)) / denom
		}
		if q != "" && strings.Contains(strings.ToLower(chunk.Text), q) {
			score += 5
		}
		if score > 0 {
			hits = append(hits, Hit{Chunk: chunk, Score: score})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].StartLine < hits[j].StartLine
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

type runtime struct {
	tfs  []map[string]int
	lens []int
	df   map[string]int
	avg  float64
	k1   float64
	b    float64
}

func newRuntime(idx *Index) *runtime {
	r := &runtime{
		tfs:  make([]map[string]int, len(idx.Chunks)),
		lens: make([]int, len(idx.Chunks)),
		df:   map[string]int{},
		k1:   1.5,
		b:    0.75,
	}
	total := 0
	for i, chunk := range idx.Chunks {
		tokens := tokenize(chunk.Text)
		tf := make(map[string]int, len(tokens))
		for _, t := range tokens {
			tf[t]++
		}
		r.tfs[i] = tf
		r.lens[i] = len(tokens)
		total += len(tokens)
		for t := range tf {
			r.df[t]++
		}
	}
	if len(idx.Chunks) > 0 {
		r.avg = float64(total) / float64(len(idx.Chunks))
	}
	if r.avg == 0 {
		r.avg = 1
	}
	return r
}

// tokenize lowercases words and also adds the camelCase parts of an
// identifier, so a query for "parse file" finds ParseFile.
func tokenize(s string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		word := b.String()
		b.Reset()
		lower := strings.ToLower(word)
		if utf8Len(lower) < 2 {
			return
		}
		out = append(out, lower)
		parts := camelParts(word)
		if len(parts) > 1 {
			for _, p := range parts {
				if utf8Len(p) >= 2 {
					out = append(out, p)
				}
			}
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func camelParts(word string) []string {
	var parts []string
	var b strings.Builder
	var prev rune
	for i, r := range word {
		if i > 0 && unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
			parts = append(parts, b.String())
			b.Reset()
		}
		b.WriteRune(unicode.ToLower(r))
		prev = r
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

func utf8Len(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func isBinary(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	n := len(data)
	if n > 512 {
		n = 512
	}
	for _, b := range data[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}

func skipDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", ".idea", ".vscode", "node_modules", "vendor", "dist", "build",
		"target", "bin", "obj", "__pycache__", ".venv", "venv", "coverage", ".next", ".cache":
		return true
	}
	return false
}

// skipFile keeps credentials out of a local index that is meant to make code
// easier to find. A key in .env is not a document; indexing it would copy it
// into a second file under the user's home directory.
func skipFile(name string) bool {
	lower := strings.ToLower(name)
	if lower == ".env" || strings.HasPrefix(lower, ".env.") {
		return true
	}
	switch lower {
	case "id_rsa", "id_ed25519", "id_ecdsa", "credentials", "credentials.json":
		return true
	}
	return strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") || strings.HasSuffix(lower, ".p12")
}

// IndexPath is the one file a workspace's index lives in.
func IndexPath(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	return filepath.Join(config.RagDir(), hex.EncodeToString(sum[:8])+".json")
}

// Save writes the index atomically. A failed write leaves the previous index
// in place rather than a half-written one.
func Save(idx *Index) error {
	if idx == nil {
		return fmt.Errorf("no index to save")
	}
	path := IndexPath(idx.Root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rag-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Load reads a workspace's index. A missing file is reported as such so the
// caller can say "run rag_index" rather than treating it as a broken index.
func Load(root string) (*Index, error) {
	path := IndexPath(root)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	if idx.Version != indexVersion {
		return nil, fmt.Errorf("index version %d is not %d; rebuild it", idx.Version, indexVersion)
	}
	return &idx, nil
}
