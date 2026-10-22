package ui

// wallpaper_paint.go — the compositor that puts a picture behind an ANSI
// frame. A terminal has no alpha channel, so "text on wallpaper" has to be
// built cell by cell: every glyph is drawn over a colour derived from the pixel
// underneath it, and a glyph's own cells are darkened more than the empty ones
// so the text sits in a visible, darker shell instead of floating on the
// picture.
//
// The rest of dmcode knows nothing about this. buildFrame and the editor each
// draw exactly what they drew before; the picture is composited over the
// finished string on the way to the renderer, and only when a wallpaper is
// active. That keeps every geometry test, hit test and selection path working
// on the unstyled frame it was written for.

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/dedomorozoff/dmcode/internal/imgprev"
	"github.com/dedomorozoff/dmcode/internal/theme"
)

// wallGlyph is one printable cell of a frame row. text may hold a combining
// mark after its base rune; width is the number of terminal cells it takes.
type wallGlyph struct {
	text  string
	width int
	style wallStyle
}

// wallStyle is the SGR state an ANSI row has established at this glyph. Only
// the attributes the compositor must re-emit are kept: everything else is
// structure the terminal has already forgotten by the time a cell is drawn.
type wallStyle struct {
	fg, bg       color.RGBA
	hasFg, hasBg bool
	bold         bool
	faint        bool
	italic       bool
	underline    bool
	reverse      bool
}

// paintWallpaper composites the picture behind content. The frame's width and
// height are its own: buildFrame is already sized to the window, and the
// editor's View is the window in the editor's mode, so no extra geometry has
// to be passed in.
func (m *uiModel) paintWallpaper(content string) string {
	if (m.wallpaper == nil && m.wallFx == "") || content == "" {
		return content
	}
	rows := strings.Split(content, "\n")
	w := 0
	for _, row := range rows {
		if n := ansi.StringWidth(row); n > w {
			w = n
		}
	}
	if w < 1 {
		return content
	}
	grid := m.wallpaperCells(w, len(rows))
	out := make([]string, len(rows))
	for y, row := range rows {
		var cells []color.RGBA
		if (y+1)*w <= len(grid) {
			cells = grid[y*w : (y+1)*w]
		}
		out[y] = composeWallpaperRow(row, w, cells, theme.Current())
	}
	return strings.Join(out, "\n")
}

// wallpaperCells samples the picture once per geometry. The generation counter
// is bumped whenever the picture changes, so a resize is the only other thing
// that makes the sample stale.
func (m *uiModel) wallpaperCells(w, h int) []color.RGBA {
	if m.wallGridGen == m.wallpaperGen && m.wallGridW == w && m.wallGridH == h && m.wallGrid != nil {
		return m.wallGrid
	}
	if m.wallFx != "" {
		m.wallGrid = effectCells(m.wallFx, w, h, time.Since(m.wallStart).Seconds())
	} else {
		m.wallGrid = imgprev.SampleCover(m.wallpaper, w, h)
	}
	m.wallGridW, m.wallGridH = w, h
	m.wallGridGen = m.wallpaperGen
	return m.wallGrid
}

// composeWallpaperRow renders one parsed row over its slice of the grid.
func composeWallpaperRow(row string, width int, cells []color.RGBA, p theme.Palette) string {
	glyphs := parseStyledRow(row)
	// shell marks the cells that belong to a text run, dilated by one column.
	// Without the dilation a word would sit in one dark box per glyph and the
	// spaces between the boxes would punch holes in the "capsule" the picture is
	// supposed to leave for the prose.
	shell := make([]bool, width)
	x := 0
	for _, g := range glyphs {
		if g.width < 1 || x >= width || x+g.width > width {
			x += max(g.width, 0)
			continue
		}
		if g.text != " " {
			for i := max(0, x-1); i < min(width, x+g.width+1); i++ {
				shell[i] = true
			}
		}
		x += g.width
	}

	var b strings.Builder
	b.Grow(len(row) + width*16)
	b.WriteString("\x1b[0m")

	x = 0
	var last wallStyle
	lastSet := false
	for _, g := range glyphs {
		if x >= width {
			break
		}
		if g.width < 1 {
			continue
		}
		if x+g.width > width {
			// A double-width glyph does not fit in the last column; the frame
			// should never draw one there, and cutting it would corrupt the
			// terminal's idea of the row.
			break
		}
		text := g.text != " " || (x < len(shell) && shell[x])
		bg := wallpaperBackdrop(cells, x, text, p)
		if g.style.hasBg {
			bg = g.style.bg
		}
		fg := wallpaperForeground(p)
		if g.style.hasFg {
			fg = g.style.fg
		}
		if g.style.reverse {
			fg, bg = bg, fg
		}
		fg = readable(fg, bg)
		if g.style.faint {
			fg = mix(fg, bg, 0.55)
		}
		st := wallStyle{fg: fg, bg: bg, bold: g.style.bold, italic: g.style.italic, underline: g.style.underline}
		if !lastSet || st != last {
			b.WriteString(sgrFor(st))
			last, lastSet = st, true
		}
		b.WriteString(g.text)
		x += g.width
	}
	for x < width {
		text := x < len(shell) && shell[x]
		bg := wallpaperBackdrop(cells, x, text, p)
		st := wallStyle{bg: bg, fg: readable(wallpaperForeground(p), bg)}
		if !lastSet || st != last {
			b.WriteString(sgrFor(st))
			last, lastSet = st, true
		}
		b.WriteByte(' ')
		x++
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// wallpaperBackdrop is the colour behind one cell. Empty cells keep more of
// the picture; a glyph's cells are darkened further, which is the "shell a few
// tones darker than the wallpaper" the feature is for. The picture is also
// pulled towards black rather than the theme background so a light theme does
// not turn into a picture of its own paper colour.
func wallpaperBackdrop(cells []color.RGBA, x int, text bool, p theme.Palette) color.RGBA {
	base := color.RGBA{8, 8, 12, 255}
	if p.Background != nil {
		if r, g, b, ok := theme.RGB(p.Background); ok {
			base = color.RGBA{r, g, b, 255}
		}
	}
	c := base
	if x >= 0 && x < len(cells) {
		s := cells[x]
		if s.A == 0 {
			s.A = 255
		}
		// A transparent picture is blended over the theme background before it
		// is darkened, so a PNG with a cut-out does not show terminal black.
		a := float64(s.A) / 255
		c = color.RGBA{
			R: uint8(float64(s.R)*a + float64(base.R)*(1-a)),
			G: uint8(float64(s.G)*a + float64(base.G)*(1-a)),
			B: uint8(float64(s.B)*a + float64(base.B)*(1-a)),
			A: 255,
		}
	}
	f := 0.50
	if text {
		f = 0.68
	}
	return mix(c, color.RGBA{0, 0, 0, 255}, f)
}

// wallpaperForeground is prose over a picture. It is a light colour regardless
// of the theme: the backdrop is always darkened, so the light text is what
// reads. Explicit semantic colours are kept and adjusted for contrast below.
func wallpaperForeground(p theme.Palette) color.RGBA {
	if r, g, b, ok := theme.RGB(p.OnWallpaper); ok {
		return color.RGBA{r, g, b, 255}
	}
	if r, g, b, ok := theme.RGB(p.Foreground); ok {
		return color.RGBA{r, g, b, 255}
	}
	return color.RGBA{240, 240, 240, 255}
}

// parseStyledRow turns an ANSI row into its printable glyphs and the SGR state
// each one carries. Non-SGR escapes are dropped: the compositor is re-emitting
// the style itself, so carrying a cursor move along would put the terminal's
// cursor somewhere the frame did not ask for.
func parseStyledRow(s string) []wallGlyph {
	var out []wallGlyph
	var st wallStyle
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if i+1 < len(s) && s[i+1] == '[' {
				j := i + 2
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				if j >= len(s) {
					break
				}
				if s[j] == 'm' {
					applySGR(&st, s[i+2:j])
				}
				i = j + 1
				continue
			}
			if i+1 < len(s) && s[i+1] == ']' {
				j := i + 2
				for j < len(s) && s[j] != 0x07 {
					if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
						j++
						break
					}
					j++
				}
				if j < len(s) {
					j++
				}
				i = j
				continue
			}
			i += 2
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 0 {
			i++
			continue
		}
		w := ansi.StringWidth(string(r))
		if w == 0 {
			if len(out) > 0 {
				out[len(out)-1].text += string(r)
			}
			i += size
			continue
		}
		if w < 0 {
			w = 1
		}
		out = append(out, wallGlyph{text: string(r), width: w, style: st})
		i += size
	}
	return out
}

// applySGR updates st for one SGR parameter list. The subset is the one
// lipgloss emits: weights, italic, underline, reverse, the 16/256/truecolour
// forms and their resets.
func applySGR(st *wallStyle, params string) {
	if params == "" {
		*st = wallStyle{}
		return
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		n, ok := atoi(parts[i])
		if !ok {
			continue
		}
		switch {
		case n == 0:
			*st = wallStyle{}
		case n == 1:
			st.bold = true
		case n == 2:
			st.faint = true
		case n == 3:
			st.italic = true
		case n == 4:
			st.underline = true
		case n == 7:
			st.reverse = true
		case n == 22:
			st.bold, st.faint = false, false
		case n == 23:
			st.italic = false
		case n == 24:
			st.underline = false
		case n == 27:
			st.reverse = false
		case n >= 30 && n <= 37:
			st.fg, st.hasFg = basicColor(n - 30)
		case n >= 90 && n <= 97:
			st.fg, st.hasFg = basicColor(n - 90 + 8)
		case n >= 40 && n <= 47:
			st.bg, st.hasBg = basicColor(n - 40)
		case n >= 100 && n <= 107:
			st.bg, st.hasBg = basicColor(n - 100 + 8)
		case n == 39:
			st.hasFg = false
		case n == 49:
			st.hasBg = false
		case n == 38 || n == 48:
			fg := n == 38
			if i+1 >= len(parts) {
				continue
			}
			switch parts[i+1] {
			case "5":
				if i+2 < len(parts) {
					if idx, ok := atoi(parts[i+2]); ok {
						c := indexedColor(idx)
						if fg {
							st.fg, st.hasFg = c, true
						} else {
							st.bg, st.hasBg = c, true
						}
					}
					i += 2
				}
			case "2":
				if i+4 < len(parts) {
					r, ok1 := atoi(parts[i+2])
					g, ok2 := atoi(parts[i+3])
					b, ok3 := atoi(parts[i+4])
					if ok1 && ok2 && ok3 {
						c := color.RGBA{uint8(r), uint8(g), uint8(b), 255}
						if fg {
							st.fg, st.hasFg = c, true
						} else {
							st.bg, st.hasBg = c, true
						}
					}
					i += 4
				}
			}
		}
	}
}

func atoi(s string) (int, bool) {
	if s == "" {
		return 0, true
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func basicColor(n int) (color.RGBA, bool) {
	if n < 0 || n > 15 {
		return color.RGBA{}, false
	}
	r, g, b, _ := ansi.BasicColor(uint8(n)).RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 255}, true
}

func indexedColor(n int) color.RGBA {
	if n < 0 || n > 255 {
		return color.RGBA{}
	}
	r, g, b, _ := ansi.IndexedColor(uint8(n)).RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 255}
}

// sgrFor writes a complete style. The compositor never relies on the terminal
// remembering anything from the frame it parsed, so every emitted cell starts
// from a known state.
func sgrFor(st wallStyle) string {
	var parts []string
	if st.bold {
		parts = append(parts, "1")
	}
	if st.faint {
		parts = append(parts, "2")
	}
	if st.italic {
		parts = append(parts, "3")
	}
	if st.underline {
		parts = append(parts, "4")
	}
	parts = append(parts,
		fmt.Sprintf("38;2;%d;%d;%d", st.fg.R, st.fg.G, st.fg.B),
		fmt.Sprintf("48;2;%d;%d;%d", st.bg.R, st.bg.G, st.bg.B))
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

// readable lifts a foreground until it stands out from its background. Semantic
// colours are worth keeping, so white is mixed in gradually instead of being
// replaced outright; only when no mix works does the better of black and white
// win.
func readable(fg, bg color.RGBA) color.RGBA {
	const want = 3.0
	if contrastRatio(fg, bg) >= want {
		return fg
	}
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	for i := 1; i <= 6; i++ {
		c := mix(fg, white, float64(i)/6)
		if contrastRatio(c, bg) >= want {
			return c
		}
	}
	if contrastRatio(white, bg) >= contrastRatio(black, bg) {
		return white
	}
	return black
}

func contrastRatio(a, b color.RGBA) float64 {
	la, lb := relLum(a), relLum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relLum(c color.RGBA) float64 {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func mix(a, b color.RGBA, t float64) color.RGBA {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return color.RGBA{
		R: uint8(float64(a.R)*(1-t) + float64(b.R)*t),
		G: uint8(float64(a.G)*(1-t) + float64(b.G)*t),
		B: uint8(float64(a.B)*(1-t) + float64(b.B)*t),
		A: 255,
	}
}
