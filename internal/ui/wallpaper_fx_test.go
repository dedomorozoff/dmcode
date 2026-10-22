package ui

import (
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
)

func TestEveryEffectFillsTheGrid(t *testing.T) {
	const cols, rows = 13, 7
	for _, e := range wallpaperEffects {
		grid := effectCells(e.id, cols, rows, 1.75)
		if len(grid) != cols*rows {
			t.Fatalf("%s: %d cells, want %d", e.id, len(grid), cols*rows)
		}
		for i, c := range grid {
			if c.A != 255 {
				t.Fatalf("%s: cell %d is translucent: %v", e.id, i, c)
			}
		}
		if !sameGrid(grid, effectCells(e.id, cols, rows, 1.75)) {
			t.Fatalf("%s is not deterministic for the same time", e.id)
		}
	}
}

func TestEveryEffectMovesOverTime(t *testing.T) {
	const cols, rows = 16, 8
	for _, e := range wallpaperEffects {
		a := effectCells(e.id, cols, rows, 0)
		b := effectCells(e.id, cols, rows, 1.3)
		diff := 0
		for i := range a {
			if a[i] != b[i] {
				diff++
			}
		}
		if diff == 0 {
			t.Fatalf("%s does not change with time", e.id)
		}
	}
}

func TestEffectNamesAreCaseInsensitiveAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range wallpaperEffects {
		if e.id == "" || seen[e.id] {
			t.Fatalf("bad or duplicate effect id %q", e.id)
		}
		seen[e.id] = true
		if got, ok := effectByID("  " + e.id + "  "); !ok || got.id != e.id {
			t.Fatalf("effectByID(%q) = %v, %v", e.id, got, ok)
		}
	}
	if _, ok := effectByID("nope"); ok {
		t.Fatal("effectByID accepted an unknown effect")
	}
}

func TestAnUnknownEffectStillFillsTheGrid(t *testing.T) {
	grid := effectCells("no-such-effect", 4, 3, 0)
	if len(grid) != 12 {
		t.Fatalf("grid = %d cells, want 12", len(grid))
	}
	for _, c := range grid {
		if c.A != 255 {
			t.Fatalf("hole in the fallback grid: %v", c)
		}
	}
}

func TestLoadWallpaperSpecPrefersAFileAndUnderstandsFxPrefix(t *testing.T) {
	dir := t.TempDir()
	if spec, err := loadWallpaperSpec("fx:fire", dir); err != nil || spec.fx != "fire" {
		t.Fatalf("fx:fire = %+v, %v", spec, err)
	}
	if spec, err := loadWallpaperSpec("waves", dir); err != nil || spec.fx != "waves" {
		t.Fatalf("bare effect = %+v, %v", spec, err)
	}
	if _, err := loadWallpaperSpec("fx:nope", dir); err == nil {
		t.Fatal("an unknown fx: name was accepted")
	}

	p := filepath.Join(dir, "waves")
	writeTestGIF(t, p, 1)
	spec, err := loadWallpaperSpec("waves", dir)
	if err != nil {
		t.Fatal(err)
	}
	if spec.fx != "" || spec.path != p {
		t.Fatalf("a file named like an effect lost to the effect: %+v", spec)
	}
}

func TestAnimatedGifKeepsItsFramesAndTicks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DMCODE_SETTINGS_PATH", filepath.Join(dir, "settings.json"))
	p := filepath.Join(dir, "anim.gif")
	writeTestGIF(t, p, 2)

	m := newTurnModel(t)
	cmd := m.setWallpaper(p)
	if cmd == nil {
		t.Fatal("setWallpaper returned no command")
	}
	next := m.handleWallpaper(cmd().(wallpaperMsg))
	if len(m.wallFrames) != 2 {
		t.Fatalf("frames = %d, want 2", len(m.wallFrames))
	}
	if !m.wallpaperAnimated() || next == nil {
		t.Fatal("a two-frame GIF did not start an animation")
	}
	beforeFrame, beforeGen := m.wallFrame, m.wallpaperGen
	again := m.wallpaperTick(wallpaperTickMsg{gen: m.wallTickGen})
	if m.wallFrame == beforeFrame {
		t.Fatal("the tick did not advance the frame")
	}
	if m.wallpaperGen == beforeGen {
		t.Fatal("the tick did not invalidate the sampled grid")
	}
	if again == nil {
		t.Fatal("the tick chain stopped after one frame")
	}

	stale := m.wallTickGen
	m.clearWallpaper()
	if cmd := m.wallpaperTick(wallpaperTickMsg{gen: stale}); cmd != nil {
		t.Fatal("a tick from the cleared wallpaper restarted the chain")
	}
}

func TestEffectsGoThroughTheCompositor(t *testing.T) {
	m := newTurnModel(t)
	m.applyWallpaper(&wallpaperSpec{fx: "fire"})
	got := m.paintWallpaper("hi")
	if got == "hi" {
		t.Fatal("the effect was not painted behind the frame")
	}
}

func sameGrid(a, b []color.RGBA) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writeTestGIF(t *testing.T, path string, frames int) {
	t.Helper()
	pal := color.Palette{color.RGBA{0, 0, 0, 255}, color.RGBA{255, 80, 0, 255}}
	bounds := image.Rect(0, 0, 3, 2)
	g := &gif.GIF{}
	for i := 0; i < frames; i++ {
		im := image.NewPaletted(bounds, pal)
		idx := uint8(i % len(pal))
		for j := range im.Pix {
			im.Pix[j] = idx
		}
		g.Image = append(g.Image, im)
		g.Delay = append(g.Delay, 8)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := gif.EncodeAll(f, g); err != nil {
		t.Fatal(err)
	}
}

func TestTheWallpaperCommandCompletesInsteadOfRunning(t *testing.T) {
	m := newTurnModel(t)
	for _, c := range m.commands() {
		if c.name == "wallpaper" {
			if !c.takesArg {
				t.Fatal("/wallpaper must complete so its path can be typed")
			}
			return
		}
	}
	t.Fatal("/wallpaper is not offered")
}

func TestAWallpaperPathIsNotAnAttachment(t *testing.T) {
	m := newTurnModel(t)
	m.input.SetValue("/wallpaper /tmp/desk.png")
	if cmd := m.watchInputImage(); cmd != nil {
		t.Fatal("the wallpaper path was read as a chat attachment")
	}
	if len(m.pending) != 0 {
		t.Fatalf("the wallpaper path became %d attachment(s)", len(m.pending))
	}
}
