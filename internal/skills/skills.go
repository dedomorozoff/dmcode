// Package skills discovers reusable instruction files in the same shape
// OpenCode uses: a SKILL.md with YAML frontmatter, or a flat markdown file,
// under a skills directory.
//
// The point of a skill is that its body is not in the prompt until the model
// asks for it. Discovery builds a catalogue, the catalogue advertises the
// skills whose description says when they apply, and the skill tool loads one
// body by id. A skill that is not advertised can still be loaded by id; that is
// what the OpenCode autoinvoke flag is for.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

const (
	// maxDepth is the OpenCode nesting limit below a source root. A skill is a
	// directory with SKILL.md; looking deeper than this turns a source into a
	// whole-tree walk for a file that is almost never there.
	maxDepth = 6
	// maxSupportingFiles is how many names the tool mentions. The model can
	// read any file with the file argument; the list is a hint, not an index.
	maxSupportingFiles = 20
	// maxSkillBytes bounds the body returned in one tool result. A skill is
	// instructions, and a model that needs a 64 KB instruction file is better
	// served by a supporting file it reads deliberately.
	maxSkillBytes = 64 << 10
	// maxSupportingBytes bounds one supporting file read through the tool.
	maxSupportingBytes = 256 << 10
	// maxAdvertisedRunes keeps the tool description from becoming a second
	// context window. The list is one line per skill.
	maxAdvertisedRunes = 6000
)

// Skill is one discovered instruction file.
type Skill struct {
	// ID is the path-derived identity, exact and case-sensitive. The
	// frontmatter name is a display label only: two skills may call themselves
	// the same thing, and a model needs one string that selects exactly one.
	ID string
	// Name is the frontmatter name, or the ID when it is absent.
	Name string
	// Description says when the skill applies. An empty description keeps the
	// skill loadable by id but out of the advertised list.
	Description string
	// Body is the markdown after the frontmatter.
	Body string
	// Path is the file the skill was read from.
	Path string
	// Dir is the directory supporting files are resolved against. It is the
	// directory containing SKILL.md for a nested skill, and the source root
	// for a flat one.
	Dir string
	// Nested is true for a directory-form skill, the only form that may have
	// supporting files.
	Nested bool
	// Files are relative paths beside SKILL.md, for the model to read with the
	// skill tool's file argument.
	Files []string
	// AutoInvoke is false when the skill opts out of the advertised list.
	AutoInvoke bool
	// Source names the directory the skill came from, for /skills.
	Source string
}

// Catalog is the discovered set, already resolved by precedence.
type Catalog struct {
	// Skills is sorted by ID, so every listing is stable.
	Skills []Skill
	// Notes records a source that could not be read or a file whose
	// frontmatter was malformed. A bad skill is skipped, never fatal: one
	// broken file in a project must not stop the session from starting.
	Notes []string
}

// Discover reads the skills visible from root, using the process's home and
// user-config directories.
func Discover(root string) Catalog {
	home, _ := os.UserHomeDir()
	configHome, _ := os.UserConfigDir()
	return DiscoverIn(root, home, configHome)
}

// DiscoverIn is Discover with the two global roots supplied, which is what
// makes discovery testable without touching the real home directory.
func DiscoverIn(root, home, configHome string) Catalog {
	byID := map[string]Skill{}
	c := Catalog{}

	for _, src := range sources(root, home, configHome) {
		c.loadSource(src, byID)
	}

	c.Skills = make([]Skill, 0, len(byID))
	for _, s := range byID {
		c.Skills = append(c.Skills, s)
	}
	sort.Slice(c.Skills, func(i, j int) bool { return c.Skills[i].ID < c.Skills[j].ID })
	return c
}

// ByID returns the skill with the exact id.
func (c Catalog) ByID(id string) (Skill, bool) {
	for _, s := range c.Skills {
		if s.ID == id {
			return s, true
		}
	}
	return Skill{}, false
}

// Advertised returns the skills the model is told about. A skill with no
// description is not advertised because there is nothing to match it against;
// a skill that opted out is not advertised but remains loadable.
func (c Catalog) Advertised() []Skill {
	out := make([]Skill, 0, len(c.Skills))
	for _, s := range c.Skills {
		if s.AutoInvoke && strings.TrimSpace(s.Description) != "" {
			out = append(out, s)
		}
	}
	return out
}

// IDs returns every id, advertised or not, in sorted order.
func (c Catalog) IDs() []string {
	out := make([]string, 0, len(c.Skills))
	for _, s := range c.Skills {
		out = append(out, s.ID)
	}
	return out
}

type source struct {
	dir   string
	label string
}

// sources lists the roots in increasing precedence: a later source overrides
// an earlier one with the same id. The order is OpenCode's, with the native
// .dmcode roots added at the project and global ends.
func sources(root, home, configHome string) []source {
	var out []source
	add := func(dir, label string) {
		if dir == "" {
			return
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return
		}
		for _, s := range out {
			if s.dir == abs {
				return
			}
		}
		out = append(out, source{dir: abs, label: label})
	}

	if home != "" {
		add(filepath.Join(home, ".claude", "skills"), "global .claude")
		add(filepath.Join(home, ".agents", "skills"), "global .agents")
		add(filepath.Join(home, ".config", "opencode", "skills"), "global opencode")
	}
	if configHome != "" {
		add(filepath.Join(configHome, "opencode", "skills"), "global opencode")
	}
	if home != "" {
		add(filepath.Join(home, ".dmcode", "skills"), "global .dmcode")
	}
	if root != "" {
		add(filepath.Join(root, ".claude", "skills"), "project .claude")
		add(filepath.Join(root, ".agents", "skills"), "project .agents")
		add(filepath.Join(root, ".opencode", "skills"), "project opencode")
		add(filepath.Join(root, ".dmcode", "skills"), "project .dmcode")
	}
	return out
}

func (c *Catalog) loadSource(src source, into map[string]Skill) {
	entries, err := os.ReadDir(src.dir)
	if err != nil {
		if !os.IsNotExist(err) {
			c.Notes = append(c.Notes, fmt.Sprintf("skills: %s: %v", src.label, err))
		}
		return
	}

	// A flat markdown file is a skill. SKILL.md at the source root is handled
	// as the directory-form skill for that root, not as a flat one.
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") || strings.EqualFold(e.Name(), "SKILL.md") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		c.addFile(into, src, id, filepath.Join(src.dir, e.Name()), false)
	}

	if fileExists(filepath.Join(src.dir, "SKILL.md")) {
		c.addFile(into, src, filepath.Base(src.dir), filepath.Join(src.dir, "SKILL.md"), true)
	}

	for _, e := range entries {
		if !e.IsDir() || skipDir(e.Name()) {
			continue
		}
		c.walkDir(src, filepath.Join(src.dir, e.Name()), 1, into)
	}
}

func (c *Catalog) walkDir(src source, dir string, depth int, into map[string]Skill) {
	if depth > maxDepth {
		return
	}
	if fileExists(filepath.Join(dir, "SKILL.md")) {
		c.addFile(into, src, filepath.Base(dir), filepath.Join(dir, "SKILL.md"), true)
		// A directory with SKILL.md is one skill. OpenCode does not search
		// inside it for more, and neither do we.
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		c.Notes = append(c.Notes, fmt.Sprintf("skills: %s: %v", dir, err))
		return
	}
	for _, e := range entries {
		if !e.IsDir() || skipDir(e.Name()) {
			continue
		}
		c.walkDir(src, filepath.Join(dir, e.Name()), depth+1, into)
	}
}

func (c *Catalog) addFile(into map[string]Skill, src source, id, path string, nested bool) {
	s, err := parseSkill(path, id)
	if err != nil {
		c.Notes = append(c.Notes, fmt.Sprintf("skills: %s: %v", path, err))
		return
	}
	s.Source = src.label
	if nested {
		s.Nested = true
		s.Dir = filepath.Dir(path)
		s.Files = supportingFiles(s.Dir)
	} else {
		s.Dir = src.dir
	}
	into[id] = s
}

func parseSkill(path, id string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	fm, body := splitFrontmatter(text)
	var meta frontmatter
	if strings.TrimSpace(fm) != "" {
		if err := yaml.Unmarshal([]byte(fm), &meta); err != nil {
			return Skill{}, fmt.Errorf("frontmatter: %w", err)
		}
	}
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = id
	}
	auto := true
	if meta.DisableModelInvocation {
		auto = false
	}
	for k, v := range meta.Metadata {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "opencode/autoinvoke", "autoinvoke":
			if b, ok := v.(bool); ok {
				auto = b
			}
		}
	}
	return Skill{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(meta.Description),
		Body:        strings.TrimSpace(body),
		Path:        path,
		AutoInvoke:  auto,
	}, nil
}

type frontmatter struct {
	Name                   string         `yaml:"name"`
	Description            string         `yaml:"description"`
	DisableModelInvocation bool           `yaml:"disable-model-invocation"`
	Metadata               map[string]any `yaml:"metadata"`
}

// splitFrontmatter returns the YAML between the opening and closing --- lines,
// and the body after it. A file with no frontmatter is all body.
func splitFrontmatter(text string) (string, string) {
	if !strings.HasPrefix(text, "---\n") {
		return "", text
	}
	rest := text[len("---\n"):]
	// The empty frontmatter is a real case: an author who writes the
	// delimiters and no fields. It is the opening delimiter immediately
	// followed by the closing one.
	if strings.HasPrefix(rest, "---\n") {
		return "", rest[len("---\n"):]
	}
	if rest == "---" {
		return "", ""
	}
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return "", text
	}
	fm := rest[:idx]
	body := rest[idx+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")
	return fm, body
}

func supportingFiles(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == dir {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(d.Name(), "SKILL.md") || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		if len(out) >= maxSupportingFiles {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func skipDir(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "dist", "build", "target", "bin", "obj", "__pycache__", ".venv", "venv":
		return true
	}
	return false
}

// Load returns a skill body, or one of its supporting files when file is set.
func (c Catalog) Load(id, file string) (string, error) {
	s, ok := c.ByID(id)
	if !ok {
		ids := c.IDs()
		if len(ids) == 0 {
			return "", fmt.Errorf("unknown skill %q; no skills are installed", id)
		}
		return "", fmt.Errorf("unknown skill %q; available: %s", id, strings.Join(ids, ", "))
	}
	if file == "" {
		body := s.Body
		if len(body) > maxSkillBytes {
			cut := maxSkillBytes
			for cut > 0 && !utf8.ValidString(body[:cut]) {
				cut--
			}
			body = body[:cut] + "\n\n[skill truncated — read the rest from " + s.Path + "]"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# Skill: %s\n\nid: %s\n", s.Name, s.ID)
		if s.Nested && s.Dir != "" {
			fmt.Fprintf(&b, "base_dir: %s\n", s.Dir)
			if len(s.Files) > 0 {
				fmt.Fprintf(&b, "supporting_files: %s\n", strings.Join(s.Files, ", "))
			}
		}
		b.WriteString("\n")
		b.WriteString(body)
		return b.String(), nil
	}

	if !s.Nested {
		return "", fmt.Errorf("skill %q is a flat file and has no supporting files", id)
	}
	rel := filepath.Clean(filepath.FromSlash(file))
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file %q must be relative to the skill directory", file)
	}
	abs := filepath.Join(s.Dir, rel)
	if err := within(s.Dir, abs); err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", file)
	}
	if info.Size() > maxSupportingBytes {
		return "", fmt.Errorf("%s is %d bytes; the limit is %d", file, info.Size(), maxSupportingBytes)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("# Skill file: %s\n\n%s", file, string(data)), nil
}

func within(dir, path string) error {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(realDir, realPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s is outside the skill directory", path)
	}
	return nil
}

// MakeTool builds the skill tool. It is deliberately read-only: loading
// instructions never changes the workspace.
func MakeTool(c Catalog) (tool.Tool, error) {
	desc := "Loads a reusable skill's instructions by id. Use it when a task matches one of the skills listed below. " +
		"Set file to read a supporting file relative to the skill directory instead of the body."
	adv := c.Advertised()
	if len(adv) == 0 {
		desc += " No skills are currently advertised."
	} else {
		var b strings.Builder
		b.WriteString(" Available skills:")
		for _, s := range adv {
			desc := strings.Join(strings.Fields(s.Description), " ")
			line := fmt.Sprintf("\n- %s — %s", s.ID, clipRunes(desc, 180))
			if b.Len()+len(line) > maxAdvertisedRunes {
				b.WriteString("\n- … more skills are installed; ask the user for /skills.")
				break
			}
			b.WriteString(line)
		}
		desc += b.String()
	}
	return functiontool.New(functiontool.Config{Name: "skill", Description: desc}, func(_ agent.Context, in skillArgs) (string, error) {
		return c.Load(in.ID, in.File)
	})
}

type skillArgs struct {
	ID   string `json:"id" jsonschema:"The exact skill id from the list, case-sensitive."`
	File string `json:"file,omitempty" jsonschema:"Optional path to a supporting file relative to the skill directory. Leave it out to load the skill body."`
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
