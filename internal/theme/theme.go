// Package theme owns dmcode's colour palettes. A palette is the whole set of
// colours the interface draws with: the terminal background and prose colour,
// the semantic accents (user, agent, tools, errors), the bars and borders, and
// the colour emphasis is rendered in.
//
// The package is deliberately tiny and free of UI dependencies. The UI and the
// embedded editor both import it and rebuild their own styles from Current();
// nothing here imports either of them, so the palette cannot start depending on
// the thing it is supposed to describe.
package theme

import (
	"fmt"
	"image/color"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

// Palette is one theme. Colour fields are lipgloss colours (ANSI names, 256
// indices or #rrggbb) so a theme can be written the same way the interface
// already wrote its colours.
//
// Background and Foreground may be nil: nil means "leave the terminal's own
// default". The default theme uses that so a fresh dmcode looks like it always
// did, and only an explicit theme paints the terminal.
type Palette struct {
	// Name is the stable id used in settings.json and on the command line.
	Name string
	// Label is what the picker shows.
	Label string

	Background color.Color
	Foreground color.Color
	// Bold is the colour of bold prose (markdown emphasis). When nil the
	// semantic colour of the surrounding style is kept.
	Bold color.Color

	Dim     color.Color
	User    color.Color
	Agent   color.Color
	Tool    color.Color
	Error   color.Color
	Accent  color.Color
	Border  color.Color
	Success color.Color
	Warning color.Color

	BarBG   color.Color
	BarFG   color.Color
	BadgeFG color.Color
	Select  color.Color

	DiffAddBG color.Color
	DiffDelBG color.Color

	// OnWallpaper is the prose colour used when a wallpaper is behind the
	// interface. The compositor darkens the picture enough for it to read; a
	// theme with a light default text colour overrides this to a light one.
	OnWallpaper color.Color
}

func hex(s string) color.Color { return lipgloss.Color(s) }

// DefaultName is the palette an unconfigured dmcode runs with. It is the
// historic colour set, so adding themes does not change the look of a session
// that never asks for one.
const DefaultName = "dmcode"

var palettes = []Palette{
	{
		Name:  DefaultName,
		Label: "dmcode",
		// The historic interface: the terminal's own background, with colour
		// carried by the semantic accents.
		Dim: hex("8"), User: hex("12"), Agent: hex("15"), Tool: hex("6"),
		Error: hex("9"), Accent: hex("5"), Border: hex("240"),
		Success: hex("10"), Warning: hex("11"), BarBG: hex("236"), BarFG: hex("253"),
		BadgeFG: hex("0"), Select: hex("13"),
		DiffAddBG: hex("22"), DiffDelBG: hex("52"),
		OnWallpaper: hex("#f2f2f2"),
	},
	{
		Name:       "midnight",
		Label:      "Midnight",
		Background: hex("#0b1020"), Foreground: hex("#d8e2ff"), Bold: hex("#ffd166"),
		Dim: hex("#6b7a99"), User: hex("#7aa2ff"), Agent: hex("#d8e2ff"), Tool: hex("#5fd1c8"),
		Error: hex("#ff6b81"), Accent: hex("#c792ea"), Border: hex("#2a3550"),
		Success: hex("#7bd88f"), Warning: hex("#f5c542"), BarBG: hex("#151c33"), BarFG: hex("#c8d3f0"),
		BadgeFG: hex("#0b1020"), Select: hex("#334066"),
		DiffAddBG: hex("#16351f"), DiffDelBG: hex("#3a1820"),
		OnWallpaper: hex("#e8eeff"),
	},
	{
		Name:       "graphite",
		Label:      "Graphite",
		Background: hex("#121212"), Foreground: hex("#e8e8e8"), Bold: hex("#ffd866"),
		Dim: hex("#7a7a7a"), User: hex("#ffffff"), Agent: hex("#e8e8e8"), Tool: hex("#9ad1ff"),
		Error: hex("#ff7b72"), Accent: hex("#ffa657"), Border: hex("#303030"),
		Success: hex("#7ee787"), Warning: hex("#e3b341"), BarBG: hex("#1c1c1c"), BarFG: hex("#d0d0d0"),
		BadgeFG: hex("#121212"), Select: hex("#333333"),
		DiffAddBG: hex("#12351a"), DiffDelBG: hex("#3a1518"),
		OnWallpaper: hex("#f0f0f0"),
	},
	{
		Name:       "light",
		Label:      "Light",
		Background: hex("#f7f3e9"), Foreground: hex("#24221e"), Bold: hex("#9a3412"),
		Dim: hex("#7a7468"), User: hex("#1a5fb4"), Agent: hex("#24221e"), Tool: hex("#0f766e"),
		Error: hex("#b3261e"), Accent: hex("#8b3a62"), Border: hex("#c9c0ae"),
		Success: hex("#2e7d32"), Warning: hex("#9a6700"), BarBG: hex("#e7dfd0"), BarFG: hex("#2b2b2b"),
		BadgeFG: hex("#f7f3e9"), Select: hex("#cfd8e8"),
		DiffAddBG: hex("#d9ead3"), DiffDelBG: hex("#f4cccc"),
		OnWallpaper: hex("#f7f3e9"),
	},
	{
		Name:       "nord",
		Label:      "Nord",
		Background: hex("#2e3440"), Foreground: hex("#eceff4"), Bold: hex("#ebcb8b"),
		Dim: hex("#7b88a1"), User: hex("#88c0d0"), Agent: hex("#e5e9f0"), Tool: hex("#8fbcbb"),
		Error: hex("#bf616a"), Accent: hex("#b48ead"), Border: hex("#4c566a"),
		Success: hex("#a3be8c"), Warning: hex("#ebcb8b"), BarBG: hex("#3b4252"), BarFG: hex("#e5e9f0"),
		BadgeFG: hex("#2e3440"), Select: hex("#434c5e"),
		DiffAddBG: hex("#33422f"), DiffDelBG: hex("#4a2c30"),
		OnWallpaper: hex("#eceff4"),
	},
	{
		Name:       "solarized",
		Label:      "Solarized",
		Background: hex("#002b36"), Foreground: hex("#eee8d5"), Bold: hex("#cb4b16"),
		Dim: hex("#657b83"), User: hex("#268bd2"), Agent: hex("#eee8d5"), Tool: hex("#2aa198"),
		Error: hex("#dc322f"), Accent: hex("#d33682"), Border: hex("#586e75"),
		Success: hex("#859900"), Warning: hex("#b58900"), BarBG: hex("#073642"), BarFG: hex("#eee8d5"),
		BadgeFG: hex("#002b36"), Select: hex("#1c4a55"),
		DiffAddBG: hex("#1d3b1d"), DiffDelBG: hex("#4a1f1f"),
		OnWallpaper: hex("#eee8d5"),
	},
}

var current = palettes[0]

// Default returns the default palette.
func Default() Palette { return palettes[0] }

// Current returns the palette in effect. It is only ever changed by Set, from
// the UI thread; a frame is drawn on that same thread, so a render can never
// catch a half-written palette.
func Current() Palette { return current }

// Names returns the stable ids in picker order.
func Names() []string {
	out := make([]string, len(palettes))
	for i, p := range palettes {
		out[i] = p.Name
	}
	return out
}

// Labels returns the display labels, index-aligned with Names.
func Labels() []string {
	out := make([]string, len(palettes))
	for i, p := range palettes {
		out[i] = p.Label
	}
	return out
}

// Lookup resolves a name case-insensitively. The empty string is the default,
// because settings.json omits an unset field and "no preference" has to mean
// the historic look rather than an error.
func Lookup(name string) (Palette, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return Default(), nil
	}
	i := slices.IndexFunc(palettes, func(p Palette) bool { return p.Name == name })
	if i < 0 {
		return Palette{}, fmt.Errorf("unknown theme %q (themes: %s)", name, strings.Join(Names(), ", "))
	}
	return palettes[i], nil
}

// Set switches the palette. An unknown name changes nothing, so a bad entry in
// settings.json or a typo at the prompt leaves the current look alone.
func Set(name string) (Palette, error) {
	p, err := Lookup(name)
	if err != nil {
		return current, err
	}
	current = p
	return p, nil
}

// RGB converts a lipgloss/ANSI colour to its 8-bit channels. An unknown or nil
// colour is reported as unset, which lets the compositor fall back to the
// terminal's own default instead of painting black.
func RGB(c color.Color) (uint8, uint8, uint8, bool) {
	if c == nil {
		return 0, 0, 0, false
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return 0, 0, 0, false
	}
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), true
}
