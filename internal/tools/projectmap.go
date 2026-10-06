package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// project_map answers "what is this project" in one call.
//
// It exists because the alternative is asking the model to be thorough. Told to
// explore, a model runs list_dir once, reads one file and considers the project
// understood — and there is nothing in that failure for dmcode to detect, let
// alone correct, because every step it took was a successful one. A map is the
// one orientation fact that does not depend on the model's discipline: the shape
// of the tree, what it is written in, and which files say so.
//
// list_dir recursive is not a substitute and the difference is the point. It
// returns a flat, sorted list of paths, which on a large repository is several
// pages of names with no grouping — structure is what a reader is looking for,
// and a flat list does not carry it. It also cannot say what language the project
// is or name the build file.
//
// Two walks, two jobs. The tree is rendered to a depth the model asked for,
// because that is the part it reads. The language and marker survey covers the
// whole repository, because a share of the top level of a large project is a
// share of its README files and its lockfiles — the first version computed both
// from the depth-limited walk and reported a Go project as "44%".

// Bounds. Every one of them exists because the alternative is a map that costs
// more than the problem it answers.
const (
	// defaultMapDepth is two levels, which is where the shape of a project
	// usually becomes legible without descending into build output.
	defaultMapDepth = 2
	// maxMapDepth stops a caller asking for the whole tree by accident.
	maxMapDepth = 4
	// maxMapDirs bounds the rendered tree.
	maxMapDirs = 120
	// maxFilesPerDir bounds one directory's file list, the level at which a
	// folder of a hundred files stops being orientation and starts being a
	// listing. The remainder is named rather than dropped, so the number is not
	// a claim that those files do not exist.
	maxFilesPerDir = 10
	// maxDirsPerDir is the same idea for subdirectories.
	maxDirsPerDir = 24
	// maxMarkers bounds the marker list, since every file called Makefile or
	// package.json in a monorepo would otherwise fill it.
	maxMarkers = 12
	// maxSurveyFiles stops the whole-tree survey. A share of ten thousand files
	// and a share of a hundred thousand are the same percentage to read, so the
	// walk is cut where it stops changing the answer.
	maxSurveyFiles = 20000
)

// mapLanguages maps an extension to the language a reader would name. Only
// extensions that decide a project's language are here: a build file's `.mod` and
// a lockfile's `.sum` are Go's and nothing else's, and counting them as Go files
// flatters the answer without saying anything new about it.
var mapLanguages = map[string]string{
	".go": "go",
	".ts": "typescript", ".tsx": "typescript", ".js": "javascript",
	".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".py": "python", ".rs": "rust", ".java": "java", ".kt": "kotlin",
	".kts": "kotlin", ".swift": "swift", ".rb": "ruby", ".php": "php",
	".cs": "csharp", ".cpp": "cpp", ".cc": "cpp", ".c": "c", ".h": "c",
	".hpp": "cpp", ".m": "objc", ".mm": "objc", ".scala": "scala",
	".ex": "elixir", ".exs": "elixir", ".erl": "erlang", ".hs": "haskell",
	".clj": "clojure", ".dart": "dart", ".lua": "lua", ".jl": "julia",
	".zig": "zig", ".vue": "vue", ".svelte": "svelte",
}

// mapMarkers are the filenames that state what a project is and how to build it.
// A map that walked past go.mod and said nothing about it would have walked for
// nothing; these are the lines a reader reads first.
var mapMarkers = map[string]bool{
	"go.mod": true, "go.work": true,
	"package.json": true, "tsconfig.json": true, "pnpm-workspace.yaml": true,
	"Cargo.toml": true, "pyproject.toml": true, "setup.py": true,
	"requirements.txt": true, "Pipfile": true, "poetry.lock": true,
	"pom.xml": true, "build.gradle": true, "build.gradle.kts": true,
	"Gemfile": true, "composer.json": true, "mix.exs": true,
	"Makefile": true, "justfile": true, "Taskfile.yml": true, "CMakeLists.txt": true,
	"Dockerfile": true, "docker-compose.yml": true, "compose.yml": true,
	"main.go": true, "main.py": true, "index.ts": true, "main.rs": true,
	"lib.rs": true, "manage.py": true, "server.ts": true, "app.py": true,
	"AGENTS.md": true, "CLAUDE.md": true, "CONTRIBUTING.md": true, "README.md": true,
}

type projectMapArgs struct {
	Path  string `json:"path,omitempty" jsonschema:"Directory to map. Defaults to the workspace root, which is what you want when you are orienting in a project for the first time."`
	Depth int    `json:"depth,omitempty" jsonschema:"How many directory levels to show in the tree, the root counted as 1. Default 2, max 4. The language and file counts always cover the whole directory tree regardless, so a shallow map is still an accurate survey."`
}

type projectMapResult struct {
	// Map is the tree: one line per directory, indented by level, with its files
	// beside it. Depth-limited, as asked for.
	Map string `json:"map"`
	// Root is the absolute directory that was mapped.
	Root string `json:"root"`
	// Languages is the share of source files across the whole tree, most common
	// first. "Source" means an extension the table recognises, which is the
	// denominator too: counting READMEs and lockfiles in it would make a Go
	// project that ships four markdown files a quarter documentation.
	Languages string `json:"languages"`
	// Markers are the build files, entry points and manuals found anywhere in
	// the tree, by path relative to the root. A monorepo's second go.mod is
	// distinguishable from its first, which is the one case where the list has
	// to be longer than a filename.
	Markers string `json:"markers"`
	// Files and Dirs count the whole survey, not the rendered tree.
	Files int `json:"files"`
	Dirs  int `json:"dirs"`
	// Truncated is true when the survey stopped at a bound rather than at the
	// edge of the tree. The numbers are then a floor on the project's size, and
	// saying so is the difference between a bounded survey and a wrong one.
	Truncated bool   `json:"truncated"`
	Note      string `json:"note,omitempty"`
}

func projectMap(ctx agent.Context, in projectMapArgs) (projectMapResult, error) {
	dir, err := resolve(in.Path)
	if err != nil {
		return projectMapResult{}, err
	}
	depth := in.Depth
	if depth <= 0 {
		depth = defaultMapDepth
	}
	if depth > maxMapDepth {
		depth = maxMapDepth
	}

	r := &mapRenderer{dir: dir, depth: depth}
	if err := r.render(); err != nil {
		return projectMapResult{}, err
	}
	s := &mapSurvey{root: dir, exts: map[string]int{}}
	s.run()

	res := projectMapResult{
		Map:       r.tree.String(),
		Root:      dir,
		Languages: s.languages(),
		Markers:   s.markerList(),
		Files:     s.files,
		Dirs:      s.dirs,
		Truncated: s.truncated,
	}
	if res.Markers == "" {
		res.Markers = "none found"
	}
	// Both bounds are named, and separately: a survey that stopped early makes
	// the counts a floor, while a tree that stopped early makes the tree a floor.
	// One flag for both would let a shallow tree claim the whole project is four
	// directories, which is the exact error this tool exists to stop.
	var notes []string
	if s.truncated {
		notes = append(notes, fmt.Sprintf(
			"The survey stopped after %d files, so the counts and the language share are a floor on the project, not all of it.",
			maxSurveyFiles))
	}
	if r.truncated {
		notes = append(notes, fmt.Sprintf(
			"The tree stopped at %d directories; map the subdirectory you care about to see it whole.", maxMapDirs))
	}
	res.Truncated = s.truncated || r.truncated
	res.Note = strings.Join(notes, " ")
	return res, nil
}

// mapRenderer draws the tree. It counts nothing: the numbers come from the
// survey, and one walk answering two questions would let the depth limit decide
// the language share — which is exactly what it was doing before the two were
// separated.
type mapRenderer struct {
	dir   string
	depth int

	tree      strings.Builder
	opened    int
	truncated bool
}

func (r *mapRenderer) render() error {
	return r.walk(r.dir, 0, filepath.Base(r.dir)+"/")
}

func (r *mapRenderer) walk(dir string, level int, name string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("не удалось прочитать каталог %s: %w", dir, err)
	}

	// Directories first, then files, each alphabetical: the model reads this to
	// learn the shape, and shape is order.
	subdirs, files := splitEntries(entries)

	r.tree.WriteString(mapIndent(level) + name + "\n")
	indent := mapIndent(level + 1)
	for i, e := range files {
		if i == maxFilesPerDir {
			r.tree.WriteString(fmt.Sprintf("%s… +%d more files (list_dir on this path)\n", indent, len(files)-maxFilesPerDir))
			break
		}
		r.tree.WriteString(indent + e.Name() + "\n")
	}

	if level+1 >= r.depth {
		// At the depth limit the subdirectories are still named — a project whose
		// packages are all one level down would otherwise look like a single
		// empty folder — but not opened.
		if len(subdirs) > 0 {
			r.tree.WriteString(indent + strings.Join(names(subdirs, maxDirsPerDir), "  ") + "\n")
		}
		return nil
	}

	for i, e := range subdirs {
		if r.opened >= maxMapDirs {
			r.truncated = true
			r.tree.WriteString(fmt.Sprintf("%s… +%d more directories\n", indent, len(subdirs)-i))
			break
		}
		r.opened++
		if err := r.walk(filepath.Join(dir, e.Name()), level+1, e.Name()+"/"); err != nil {
			// One unreadable subdirectory says nothing about the rest of the tree,
			// and failing the whole map over a permissions problem in one corner
			// would make the tool useless exactly where projects get messy.
			r.tree.WriteString(mapIndent(level+2) + "(not readable)\n")
		}
	}
	return nil
}

// opened counts the directories the renderer has descended into, which is what
// maxMapDirs bounds.

// mapIndent is two spaces per level and nothing else. Tree-drawing glyphs were
// tried and removed: they have to be drawn at every level to stay aligned, and a
// child that prints its own header cannot know which branch glyph its parent
// chose for it — which is what made the first version of this map unreadable.
func mapIndent(level int) string { return strings.Repeat("  ", level) }

// splitEntries divides a directory listing into its subdirectories and its
// files, each alphabetical, with the ignored directories already gone. It is
// shared because the renderer and the survey both need the same division and a
// second one would eventually disagree about what is ignored.
func splitEntries(entries []os.DirEntry) (subdirs, files []os.DirEntry) {
	for _, e := range entries {
		if e.IsDir() {
			if !isIgnoredDir(e.Name()) {
				subdirs = append(subdirs, e)
			}
			continue
		}
		files = append(files, e)
	}
	byName := func(s []os.DirEntry) func(i, j int) bool {
		return func(i, j int) bool { return s[i].Name() < s[j].Name() }
	}
	sort.Slice(subdirs, byName(subdirs))
	sort.Slice(files, byName(files))
	return subdirs, files
}

func names(entries []os.DirEntry, cap int) []string {
	if len(entries) > cap {
		entries = entries[:cap]
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name()+"/")
	}
	return out
}

// mapSurvey walks the whole tree for the numbers: what it is written in, how
// big it is, which files describe it.
//
// Only names are read — no file is opened — so this is a stat walk and stays
// cheap on a large repository. WalkDir does not descend into symlinked
// directories, which is what keeps a survey inside the boundary the same way
// grep's walk does.
type mapSurvey struct {
	root string

	files     int
	dirs      int
	exts      map[string]int
	markers   []string
	seenMark  map[string]bool
	truncated bool
}

func (s *mapSurvey) run() {
	s.dirs = 1
	s.seenMark = map[string]bool{}
	filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// An unreadable entry is skipped rather than fatal: one locked
			// directory should not cost the model its orientation.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if isIgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			s.dirs++
			return nil
		}
		s.files++
		if s.files >= maxSurveyFiles {
			s.truncated = true
			return filepath.SkipAll
		}
		name := d.Name()
		if ext := strings.ToLower(filepath.Ext(name)); ext != "" {
			if _, known := mapLanguages[ext]; known {
				s.exts[ext]++
			}
		}
		if mapMarkers[name] && len(s.markers) < maxMarkers {
			if rel, rerr := filepath.Rel(s.root, path); rerr == nil {
				rel = filepath.ToSlash(rel)
				if !s.seenMark[rel] {
					s.seenMark[rel] = true
					s.markers = append(s.markers, rel)
				}
			}
		}
		return nil
	})
}

// languages is the survey resolved into names, most common first, as a share of
// the source files it recognised. An unrecognised repository says "unknown"
// rather than guessing from extensions it does not know.
func (s *mapSurvey) languages() string {
	if len(s.exts) == 0 {
		return "unknown"
	}
	byLang := map[string]int{}
	total := 0
	for ext, n := range s.exts {
		byLang[mapLanguages[ext]] += n
		total += n
	}
	type langCount struct {
		name string
		n    int
	}
	out := make([]langCount, 0, len(byLang))
	for name, n := range byLang {
		out = append(out, langCount{name, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].name < out[j].name
	})
	var b strings.Builder
	for _, l := range out {
		// A language at zero percent is not a language of the project, it is one
		// file. Printing "go 98%, lua 0%, php 0%" makes a reader hunt for the
		// second language of a Go project that does not have one, and it is how a
		// stray script in a scripts/ directory ends up described as part of the
		// stack.
		share := l.n * 100 / total
		if share < 1 && len(out) > 1 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s %d%%", l.name, share)
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// markerList names the markers, or says there were none — a field reading "none
// found at this depth" was once true and then stopped being true when the survey
// stopped sharing the renderer's depth, which is the kind of sentence that is
// wrong in one build and right in another.
func (s *mapSurvey) markerList() string {
	if len(s.markers) == 0 {
		return "none found"
	}
	return strings.Join(s.markers, ", ")
}

func makeProjectMapTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "project_map",
		Description: "Maps a project in one call: the directory tree to a given depth, the languages it is written in, " +
			"and the build files, entry points and manuals it contains. Start here when you do not yet know what a project " +
			"is or how it is laid out — one call instead of a dozen list_dir calls. The language share and the file counts " +
			"cover the whole tree however shallow the tree you are shown. The .git, node_modules, vendor, dist, target and " +
			"virtualenv directories are skipped. It reports when a bound stopped it early.",
	}, projectMap)
}
