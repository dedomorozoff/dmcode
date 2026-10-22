package theme

import (
	"image/color"
	"testing"
)

func TestEveryThemeHasAUniqueStableName(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range Names() {
		if n == "" {
			t.Fatal("a theme has an empty name")
		}
		if seen[n] {
			t.Fatalf("duplicate theme name %q", n)
		}
		seen[n] = true
		if _, err := Lookup(n); err != nil {
			t.Fatalf("Names lists %q but Lookup cannot find it: %v", n, err)
		}
	}
}

func TestUnknownThemeIsRefusedWithoutChangingTheCurrentOne(t *testing.T) {
	before := Current().Name
	if _, err := Set("no-such-theme"); err == nil {
		t.Fatal("Set accepted an unknown theme")
	}
	if got := Current().Name; got != before {
		t.Fatalf("a refused switch changed the theme: %q -> %q", before, got)
	}
}

func TestDefaultIsTheHistoricPalette(t *testing.T) {
	p, err := Lookup(DefaultName)
	if err != nil {
		t.Fatalf("%s is not registered: %v", DefaultName, err)
	}
	// The default deliberately leaves the terminal's own background and prose
	// colour alone; repainting them is what every other theme opts into.
	if p.Background != nil || p.Foreground != nil {
		t.Fatalf("default palette paints background/foreground: %v/%v", p.Background, p.Foreground)
	}
}

func TestRGBReadsEveryPaletteColour(t *testing.T) {
	for _, n := range Names() {
		p, _ := Lookup(n)
		for _, c := range []struct {
			name string
			col  color.Color
		}{
			{"dim", p.Dim}, {"user", p.User}, {"agent", p.Agent},
			{"tool", p.Tool}, {"error", p.Error}, {"accent", p.Accent},
			{"border", p.Border}, {"bar", p.BarBG}, {"badge", p.BadgeFG},
			{"select", p.Select}, {"wallpaper", p.OnWallpaper},
		} {
			if _, _, _, ok := RGB(c.col); !ok {
				t.Fatalf("%s.%s does not convert to RGB", n, c.name)
			}
		}
	}
}
