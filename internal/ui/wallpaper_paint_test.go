package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/settings"
	"github.com/dedomorozoff/dmcode/internal/theme"
)

func whiteImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{240, 240, 240, 255})
		}
	}
	return img
}

func TestNoWallpaperLeavesTheFrameAlone(t *testing.T) {
	m := &uiModel{}
	const frame = "hello \x1b[31mred\x1b[0m"
	if got := m.paintWallpaper(frame); got != frame {
		t.Fatalf("paintWallpaper changed a frame with no picture:\n%q", got)
	}
}

func TestWallpaperPadsTheFrameToItsOwnWidth(t *testing.T) {
	m := &uiModel{wallpaper: whiteImage(8, 1)}
	got := m.paintWallpaper("hi")
	if w := ansi.StringWidth(got); w != 2 {
		t.Fatalf("the compositor invented columns: width %d, want the frame's 2", w)
	}
	// A one-row frame stays one row even though the picture could fill more.
	if strings.Count(got, "\n") != 0 {
		t.Fatalf("a one-row frame became %q", got)
	}
}

func TestWallpaperKeepsAnExplicitBackground(t *testing.T) {
	m := &uiModel{wallpaper: whiteImage(4, 1)}
	// A diff line or a badge carries its own background; the picture must not
	// paint over the thing the colour is saying.
	got := m.paintWallpaper("\x1b[48;2;1;2;3mX\x1b[0m")
	if !strings.Contains(got, "48;2;1;2;3") {
		t.Fatalf("an explicit background was replaced:\n%q", got)
	}
}

func TestWallpaperTextSitsOnADarkerShell(t *testing.T) {
	cells := []color.RGBA{
		{R: 240, G: 240, B: 240, A: 255},
		{R: 240, G: 240, B: 240, A: 255},
	}
	p := theme.Default()
	blank := wallpaperBackdrop(cells, 0, false, p)
	text := wallpaperBackdrop(cells, 1, true, p)
	if relLum(text) >= relLum(blank) {
		t.Fatalf("text shell is not darker than the picture: text %v, blank %v", text, blank)
	}
	if relLum(blank) >= relLum(cells[0]) {
		t.Fatalf("the picture was not darkened at all: %v vs %v", blank, cells[0])
	}
}

func TestReadableLiftsTextOutOfItsBackground(t *testing.T) {
	dark := color.RGBA{10, 10, 10, 255}
	got := readable(dark, dark)
	if c := contrastRatio(got, dark); c < 3 {
		t.Fatalf("readable returned %v at contrast %.2f", got, c)
	}
}

func TestTheThemeSwitchRebuildsTheChatStyles(t *testing.T) {
	t.Cleanup(func() { applyTheme(theme.Default()) })
	applyTheme(theme.Default())
	before := cUser
	p, err := theme.Lookup("midnight")
	if err != nil {
		t.Fatal(err)
	}
	applyTheme(p)
	if cUser == before {
		t.Fatal("the user colour did not move with the theme")
	}
	if styleStatusBar.GetBackground() == nil {
		t.Fatal("the status bar lost its background")
	}
}

func TestTheThemePickerLandsOnTheCurrentTheme(t *testing.T) {
	t.Cleanup(func() { applyTheme(theme.Default()) })
	applyTheme(theme.Default())
	m := &uiModel{}
	m.openThemePicker()
	if !m.themePick.open {
		t.Fatal("the picker did not open")
	}
	if got := theme.Names()[m.themePick.selected]; got != theme.Current().Name {
		t.Fatalf("the cursor landed on %q, not the current %q", got, theme.Current().Name)
	}
}

func TestChoosingAThemeAppliesAndPersistsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("DMCODE_SETTINGS_PATH", path)
	t.Cleanup(func() {
		theme.Set(theme.DefaultName)
		applyTheme(theme.Default())
	})
	m := newTurnModel(t)
	m.openThemePicker()
	m.chooseTheme("nord")
	if got := theme.Current().Name; got != "nord" {
		t.Fatalf("current theme is %q, want nord", got)
	}
	s, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.Theme != "nord" {
		t.Fatalf("saved theme is %q, want nord", s.Theme)
	}
}

func TestWallpaperIsDecodedAppliedAndSaved(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(dir, "settings.json"))
	p := filepath.Join(dir, "wall.png")
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{200, 120, 40, 255})
		}
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	m := newTurnModel(t)
	cmd := m.setWallpaper(p)
	if cmd == nil {
		t.Fatal("setWallpaper returned no command")
	}
	m.handleWallpaper(cmd().(wallpaperMsg))
	if m.wallpaper == nil {
		t.Fatal("the wallpaper was not applied")
	}
	if m.wallpaperPath != p {
		t.Fatalf("wallpaper path is %q, want %q", m.wallpaperPath, p)
	}
	s, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.Wallpaper != p {
		t.Fatalf("saved wallpaper is %q, want %q", s.Wallpaper, p)
	}

	m.handleWallpaper(wallpaperMsg{off: true})
	if m.wallpaper != nil || m.wallpaperPath != "" {
		t.Fatal("wallpaper off left the picture in place")
	}
	s, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.Wallpaper != "" {
		t.Fatalf("wallpaper off saved %q", s.Wallpaper)
	}
}
