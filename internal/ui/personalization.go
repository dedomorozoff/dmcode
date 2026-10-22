package ui

// personalization.go — /theme and /wallpaper. The look is a global preference
// (settings.json), applied before the first frame, and changed at runtime from
// two small dialogs. The wallpaper itself is painted by wallpaper_paint.go;
// this file only owns choosing, decoding and remembering it.

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dedomorozoff/dmcode/internal/i18n"
	"github.com/dedomorozoff/dmcode/internal/imgprev"
	"github.com/dedomorozoff/dmcode/internal/settings"
	"github.com/dedomorozoff/dmcode/internal/theme"
)

// themePickState is the /theme overlay. The set is fixed and short, so there is
// no filter and no text field: up/down and enter.
type themePickState struct {
	open     bool
	selected int
}

// wallpaperSpec is one chosen wallpaper: a file (one frame or a GIF) or a
// generated effect. It is what main hands the first model, and what the
// decode goroutine hands the event loop.
type wallpaperSpec struct {
	path   string
	fx     string
	frames []image.Image
	delays []time.Duration
}

// display is what the status line and settings call this wallpaper.
func (s *wallpaperSpec) display() string {
	if s == nil {
		return ""
	}
	if s.fx != "" {
		return s.fx
	}
	return s.path
}

// setting is the value written to settings.json. An effect is prefixed so a
// future file named "fire" cannot silently become the fire effect.
func (s *wallpaperSpec) setting() string {
	if s == nil {
		return ""
	}
	if s.fx != "" {
		return "fx:" + s.fx
	}
	return s.path
}

// Startup personalisation is read by main before RunTUI and handed to the
// first model through these values. It is deliberately not read inside
// InitialModel: tests build a model without a settings file, and a developer's
// own wallpaper must not appear in them.
var bootWallpaper *wallpaperSpec

// LoadPersonalization applies the saved theme and decodes the saved wallpaper.
// It returns notes for anything that could not be applied: a missing picture
// is not a reason to refuse to start, but it is a reason to say so.
func LoadPersonalization() []string {
	s, err := settings.Load()
	var notes []string
	if err != nil {
		notes = append(notes, "settings: "+err.Error())
	}
	if _, err := theme.Set(s.Theme); err != nil {
		notes = append(notes, err.Error())
	}
	applyTheme(theme.Current())
	bootWallpaper = nil
	if strings.TrimSpace(s.Wallpaper) != "" {
		wd, _ := os.Getwd()
		spec, err := loadWallpaperSpec(s.Wallpaper, wd)
		if err != nil {
			notes = append(notes, "wallpaper: "+err.Error())
		} else {
			bootWallpaper = spec
		}
	}
	return notes
}

// currentThemeName is the stable id shown in status lines and written to
// settings.json.
func currentThemeName() string { return theme.Current().Name }

func themeIndex(name string) int {
	for i, n := range theme.Names() {
		if n == name {
			return i
		}
	}
	return 0
}

func (m *uiModel) openThemePicker() tea.Cmd {
	m.themePick = themePickState{open: true, selected: themeIndex(currentThemeName())}
	return nil
}

// themeKey drives /theme. Choosing takes effect immediately and is persisted,
// the same contract /lang has: the rest of the session and the next start use
// it.
func (m *uiModel) themeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.themePick
	switch msg.String() {
	case "esc", "ctrl+p", "q":
		p.open = false
		return m, nil
	case "up":
		if p.selected > 0 {
			p.selected--
		}
		return m, nil
	case "down":
		if p.selected < len(theme.Names())-1 {
			p.selected++
		}
		return m, nil
	case "enter":
		p.open = false
		return m, m.chooseTheme(theme.Names()[min(p.selected, len(theme.Names())-1)])
	}
	return m, nil
}

// chooseTheme applies and saves a palette. A theme changes rendered colours,
// so the transcript cache has to be rebuilt: cached rows hold the old escapes.
func (m *uiModel) chooseTheme(name string) tea.Cmd {
	p, err := theme.Set(name)
	if err != nil {
		m.statusText = err.Error()
		return nil
	}
	m.applyThemeEverywhere(p)
	m.historyDirty = true
	m.statusText = i18n.T("theme: ") + p.Label
	m.history = append(m.history, line{kindSys, i18n.T("theme: ") + p.Label})
	if cerr := settings.Update(func(s *settings.S) { s.Theme = p.Name }); cerr != nil {
		m.statusText = i18n.T("could not save the theme choice")
		m.history = append(m.history, line{kindErr, cerr.Error()})
	}
	m.historyDirty = true
	m.followVP()
	return nil
}

// themeBox renders the /theme list: * marks the active palette, ▸ the cursor.
func (m *uiModel) themeBox() string {
	inner := m.floatingWidth() - panelBorder
	labels := theme.Labels()
	names := theme.Names()
	var entries [][]string
	for i, label := range labels {
		marker, style := "   ", lipgloss.NewStyle()
		switch {
		case i == m.themePick.selected:
			marker, style = " ▸ ", styleTool
		case names[i] == currentThemeName():
			marker, style = " * ", styleTool
		}
		suffix := label + "  " + names[i]
		if i != m.themePick.selected {
			suffix = label + "  " + styleHint.Render(names[i])
		}
		rows := wrapIndent(suffix, inner, marker, "     ")
		if i != m.themePick.selected {
			for j := range rows {
				rows[j] = style.Render(rows[j])
			}
		}
		entries = append(entries, rows)
	}
	return m.floatingPanel(i18n.T("? theme  (* — current, enter — select, esc — cancel)"), "", entries, &listView{sel: m.themePick.selected})
}

// wallpaperMsg carries a decoded wallpaper back to the event loop. The read is
// on a goroutine because a wallpaper can be several megabytes and Update must
// not freeze on the keystroke that chose it.
type wallpaperMsg struct {
	spec *wallpaperSpec
	off  bool
	err  error
}

// wallpaperTickMsg advances an animated wallpaper. The generation is what makes
// a tick from a wallpaper the user has already replaced a no-op.
type wallpaperTickMsg struct{ gen int }

// setWallpaper applies "/wallpaper <path|effect>", "/wallpaper list" or
// "/wallpaper off". A missing path is an error that leaves the current picture
// alone; "off" is the deliberate way back to the theme's plain background.
//
// A bare effect name wins only when no file of that name exists; fx:<id> is the
// unambiguous spelling. That keeps a photograph called "waves" usable.
func (m *uiModel) setWallpaper(arg string) tea.Cmd {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		if m.wallpaperPath == "" && m.wallFx == "" {
			m.statusText = i18n.T("no wallpaper set")
		} else {
			m.statusText = i18n.T("wallpaper: ") + m.wallpaperName()
		}
		return nil
	}
	lower := strings.ToLower(unquotePath(arg))
	if lower == "list" {
		m.showWallpaperList()
		return nil
	}
	if lower == "off" || lower == "none" || lower == "clear" {
		return func() tea.Msg { return wallpaperMsg{off: true} }
	}
	wd := m.workDir
	return func() tea.Msg {
		spec, err := loadWallpaperSpec(arg, wd)
		return wallpaperMsg{spec: spec, err: err}
	}
}

// wallpaperName is the path or the effect id, whichever is set.
func (m *uiModel) wallpaperName() string {
	if m.wallFx != "" {
		return m.wallFx
	}
	return m.wallpaperPath
}

// showWallpaperList puts the generated effects into the transcript. It is a
// list, not an overlay: the names are a reference, and the prompt stays usable
// while it is on screen.
func (m *uiModel) showWallpaperList() {
	if m.wallFx == "" && m.wallpaperPath == "" {
		m.history = append(m.history, line{kindSys, i18n.T("no wallpaper set")})
	} else {
		m.history = append(m.history, line{kindSys, i18n.T("wallpaper: ") + m.wallpaperName()})
	}
	m.history = append(m.history,
		line{kindSys, i18n.T("images and gifs: /wallpaper <path>")},
		line{kindSys, i18n.T("generated effects:")})
	for _, e := range wallpaperEffects {
		m.history = append(m.history, line{kindSys, "· " + e.id + " — " + e.label})
	}
	m.history = append(m.history, line{kindSys, i18n.T("/wallpaper off clears the picture")})
	m.historyDirty = true
	m.followVP()
}

// handleWallpaper is the event-loop half: it swaps the picture, invalidates the
// sampled grid, starts or stops the animation and saves the choice.
func (m *uiModel) handleWallpaper(msg wallpaperMsg) tea.Cmd {
	switch {
	case msg.err != nil:
		m.statusText = i18n.T("could not load the wallpaper: ") + msg.err.Error()
		m.history = append(m.history, line{kindErr, msg.err.Error()})
		m.historyDirty = true
		return nil
	case msg.off:
		m.clearWallpaper()
		m.statusText = i18n.T("wallpaper off")
	default:
		m.applyWallpaper(msg.spec)
		m.statusText = i18n.T("wallpaper: ") + msg.spec.display()
	}
	m.historyDirty = true
	if err := settings.Update(func(s *settings.S) { s.Wallpaper = m.wallpaperSetting() }); err != nil {
		m.statusText = i18n.T("could not save the wallpaper choice")
		m.history = append(m.history, line{kindErr, err.Error()})
	}
	return m.wallpaperTickCmd()
}

// wallpaperSetting is the settings.json value for what is currently shown.
func (m *uiModel) wallpaperSetting() string {
	if m.wallFx != "" {
		return "fx:" + m.wallFx
	}
	return m.wallpaperPath
}

// applyWallpaper installs a decoded wallpaper and starts its animation if it
// has one. The generation bumps are what invalidate both the sampled grid and
// any tick already in flight.
func (m *uiModel) applyWallpaper(spec *wallpaperSpec) {
	if spec == nil {
		m.clearWallpaper()
		return
	}
	m.wallFrames, m.wallDelays = spec.frames, spec.delays
	m.wallFx, m.wallpaperPath = spec.fx, spec.path
	m.wallFrame = 0
	m.wallpaper = nil
	if len(spec.frames) > 0 {
		m.wallpaper = spec.frames[0]
	}
	m.wallStart = time.Now()
	m.wallpaperGen++
	m.wallTickGen++
}

// clearWallpaper removes the picture and stops its animation. The tick
// generation is bumped before the fields are cleared so an in-flight tick
// cannot resurrect the frame it was carrying.
func (m *uiModel) clearWallpaper() {
	m.wallTickGen++
	m.wallpaper = nil
	m.wallpaperPath = ""
	m.wallFx = ""
	m.wallFrames = nil
	m.wallDelays = nil
	m.wallFrame = 0
	m.wallpaperGen++
}

// wallpaperAnimated reports whether there is a next frame to ask for.
func (m *uiModel) wallpaperAnimated() bool {
	return m.wallFx != "" || len(m.wallFrames) > 1
}

// wallpaperTickCmd schedules the next animation frame. A still picture returns
// nil and therefore schedules nothing at all.
func (m *uiModel) wallpaperTickCmd() tea.Cmd {
	if !m.wallpaperAnimated() {
		return nil
	}
	gen := m.wallTickGen
	delay := m.wallpaperDelay()
	return tea.Tick(delay, func(time.Time) tea.Msg { return wallpaperTickMsg{gen: gen} })
}

func (m *uiModel) wallpaperDelay() time.Duration {
	if m.wallFx != "" {
		return 90 * time.Millisecond
	}
	if len(m.wallFrames) > 1 {
		if len(m.wallDelays) > 0 {
			d := m.wallDelays[m.wallFrame%len(m.wallDelays)]
			if d > 0 {
				return d
			}
		}
		return 90 * time.Millisecond
	}
	return 0
}

// wallpaperTick advances a GIF or a generated effect. A still image and a
// replaced wallpaper both answer nil, which is how the tick chain ends without
// a cancellation path.
func (m *uiModel) wallpaperTick(msg wallpaperTickMsg) tea.Cmd {
	if msg.gen != m.wallTickGen || !m.wallpaperAnimated() {
		return nil
	}
	if m.wallFx == "" {
		m.wallFrame = (m.wallFrame + 1) % len(m.wallFrames)
		m.wallpaper = m.wallFrames[m.wallFrame]
	}
	m.wallpaperGen++
	return m.wallpaperTickCmd()
}

// resolvePersonalPath makes ~/Pictures/x.png and relative project paths usable
// from wherever the process happens to be.
func (m *uiModel) resolvePersonalPath(path string) string {
	return resolvePath(path, m.workDir)
}

func resolvePath(path, base string) string {
	path = unquotePath(strings.TrimSpace(path))
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimLeft(path[1:], `/\`))
		}
	}
	if !filepath.IsAbs(path) && base != "" {
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path)
}

func unquotePath(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// loadWallpaperSpec resolves an argument to a file or a generated effect.
//
// A bare known effect name is the effect when no file of that name exists; an
// "fx:" prefix always means the effect. The file-first rule keeps a picture
// called "waves" usable, and the prefix is the unambiguous spelling for the
// effect.
func loadWallpaperSpec(arg, base string) (*wallpaperSpec, error) {
	raw := strings.TrimSpace(unquotePath(arg))
	if strings.HasPrefix(strings.ToLower(raw), "fx:") {
		name := strings.TrimSpace(raw[3:])
		e, ok := effectByID(name)
		if !ok {
			return nil, fmt.Errorf("unknown wallpaper effect %q (effects: %s)", name, strings.Join(effectIDs(), ", "))
		}
		return &wallpaperSpec{fx: e.id}, nil
	}
	path := resolvePath(raw, base)
	if _, err := os.Stat(path); err != nil {
		if e, ok := effectByID(raw); ok {
			return &wallpaperSpec{fx: e.id}, nil
		}
		return nil, err
	}
	return readWallpaper(path)
}

// readWallpaper decodes a picture, keeping every frame and delay of an animated
// GIF. A frame timer behind prose is the same kind of decision as a moving
// desktop wallpaper: it is opt-in, and it is what /wallpaper <path> asks for.
func readWallpaper(path string) (*wallpaperSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	anim, err := imgprev.Decode(data)
	if err != nil {
		return nil, err
	}
	if len(anim.Frames) == 0 {
		return nil, errors.New("the picture has no frames")
	}
	// A wallpaper is held for as long as it is on screen, and an animated one
	// holds every frame. The preview decoder is generous (240 frames at full
	// size); behind prose, 60 frames and a moderate size are more than the cell
	// grid can show and small enough not to cost a session's memory.
	const (
		maxFrames = 60
		maxWidth  = 1280
		maxHeight = 720
	)
	frames := anim.Frames
	if len(frames) > maxFrames {
		frames = frames[:maxFrames]
	}
	spec := &wallpaperSpec{path: path}
	for i, f := range frames {
		spec.frames = append(spec.frames, imgprev.Fit(f, maxWidth, maxHeight))
		spec.delays = append(spec.delays, anim.FrameDelay(i))
	}
	return spec, nil
}

// personalizeView paints the theme's terminal colours and, when one is set,
// the wallpaper behind whatever screen is being drawn. It is applied to the
// editor's View as well as the chat's, so the whole workspace moves together.
func (m *uiModel) personalizeView(v tea.View) tea.View {
	p := theme.Current()
	if p.Background != nil {
		v.BackgroundColor = p.Background
	}
	if p.Foreground != nil {
		v.ForegroundColor = p.Foreground
	}
	if m.wallpaper != nil || m.wallFx != "" {
		v.Content = m.paintWallpaper(v.Content)
	}
	return v
}

// isWallpaperCommand reports whether the prompt is /wallpaper, with or
// without an argument. It is used by the image watcher: the path after the
// command is a choice of background, not a picture to send with the message.
func isWallpaperCommand(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	return text == "/wallpaper" || strings.HasPrefix(text, "/wallpaper ")
}
