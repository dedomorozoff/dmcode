package ui

// wallpaper_fx.go — generated wallpapers. They are drawn straight into the
// terminal-cell grid rather than into an image that is then sampled: a fire or
// a wave has no source pixels to preserve, and generating at the grid's size is
// both sharper and far cheaper than drawing a 4K canvas a dozen times a second.
//
// Every effect is a pure function of (cols, rows, seconds). That is what makes
// them testable without a ticker, and what lets the grid cache in
// wallpaper_paint.go resample one only when the geometry or the frame changes.

import (
	"image/color"
	"math"
	"strings"
)

type wallpaperEffect struct {
	id    string
	label string
}

var wallpaperEffects = []wallpaperEffect{
	{id: "bubbles", label: "Bubbles"},
	{id: "fire", label: "Fire"},
	{id: "waves", label: "Waves"},
	{id: "aurora", label: "Aurora"},
	{id: "stars", label: "Stars"},
}

func effectByID(id string) (wallpaperEffect, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, e := range wallpaperEffects {
		if e.id == id {
			return e, true
		}
	}
	return wallpaperEffect{}, false
}

func effectIDs() []string {
	out := make([]string, len(wallpaperEffects))
	for i, e := range wallpaperEffects {
		out[i] = e.id
	}
	return out
}

// effectCells renders one frame. An unknown id is the dark background the
// effects share, never nil: a caller that reaches here with a typo should see
// something, not a frame with holes in it.
func effectCells(id string, cols, rows int, t float64) []color.RGBA {
	if cols < 1 || rows < 1 {
		return nil
	}
	switch id {
	case "bubbles":
		return bubblesCells(cols, rows, t)
	case "fire":
		return fireCells(cols, rows, t)
	case "waves":
		return wavesCells(cols, rows, t)
	case "aurora":
		return auroraCells(cols, rows, t)
	case "stars":
		return starsCells(cols, rows, t)
	}
	out := make([]color.RGBA, cols*rows)
	for i := range out {
		out[i] = rgba(6, 8, 14)
	}
	return out
}

func bubblesCells(cols, rows int, t float64) []color.RGBA {
	out := make([]color.RGBA, cols*rows)
	for y := 0; y < rows; y++ {
		d := float64(y) / float64(max(rows-1, 1))
		base := mixRGB(rgba(5, 20, 42), rgba(3, 7, 18), d)
		for x := 0; x < cols; x++ {
			out[y*cols+x] = base
		}
	}
	n := cols*rows/55 + 8
	span := float64(rows + 6)
	for i := 0; i < n; i++ {
		a := hash01(uint32(i*3 + 1))
		b := hash01(uint32(i*3 + 2))
		c := hash01(uint32(i*3 + 3))
		speed := 1.4 + a*4.2
		y := span - math.Mod(t*speed+b*span, span) - 3
		x := c*float64(cols) + math.Sin(t*0.7+a*6.283)*2.5
		rad := 0.8 + b*2.4
		for dy := -int(rad) - 1; dy <= int(rad)+1; dy++ {
			for dx := -int(rad) - 1; dx <= int(rad)+1; dx++ {
				px, py := int(x)+dx, int(y)+dy
				if px < 0 || px >= cols || py < 0 || py >= rows {
					continue
				}
				dist := math.Hypot(float64(dx), float64(dy)*2.1)
				if dist > rad {
					continue
				}
				rim := 1 - dist/rad
				col := rgba(80+120*rim, 180+60*rim, 225+25*rim)
				blendCell(out, py*cols+px, col, 0.10+0.55*rim)
			}
		}
	}
	return out
}

func fireCells(cols, rows int, t float64) []color.RGBA {
	out := make([]color.RGBA, cols*rows)
	for y := 0; y < rows; y++ {
		depth := 1 - float64(y)/float64(max(rows-1, 1))
		for x := 0; x < cols; x++ {
			fx := float64(x)
			fy := float64(y)
			flicker := 0.5 + 0.5*math.Sin(fx*0.55+t*3.1+math.Sin(fy*0.65-t*2.4)*2.1)
			sway := 0.5 + 0.5*math.Sin(fx*0.23-t*1.7+fy*0.37)
			v := math.Pow(depth, 1.55)*(0.55+0.45*flicker) + 0.16*sway*depth
			out[y*cols+x] = fireRamp(v)
		}
	}
	return out
}

func fireRamp(v float64) color.RGBA {
	v = clamp01(v)
	switch {
	case v < 0.32:
		return mixRGB(rgba(8, 2, 2), rgba(90, 8, 4), v/0.32)
	case v < 0.66:
		return mixRGB(rgba(90, 8, 4), rgba(240, 100, 12), (v-0.32)/0.34)
	default:
		return mixRGB(rgba(240, 100, 12), rgba(255, 235, 150), (v-0.66)/0.34)
	}
}

func wavesCells(cols, rows int, t float64) []color.RGBA {
	out := make([]color.RGBA, cols*rows)
	deep := rgba(3, 16, 44)
	crest := rgba(36, 150, 185)
	foam := rgba(220, 245, 255)
	for y := 0; y < rows; y++ {
		v := float64(y) / float64(max(rows-1, 1))
		for x := 0; x < cols; x++ {
			u := float64(x) / float64(max(cols-1, 1))
			w := math.Sin(u*12+t*1.3) + math.Sin(u*7-t*1.1+v*4.2) + math.Sin((u+v)*9+t*0.7)
			k := clamp01((w + 3) / 6)
			c := mixRGB(deep, crest, k)
			spray := clamp01((k - 0.80) / 0.20)
			c = mixRGB(c, foam, spray*0.45)
			out[y*cols+x] = c
		}
	}
	return out
}

func auroraCells(cols, rows int, t float64) []color.RGBA {
	out := make([]color.RGBA, cols*rows)
	skyTop := rgba(3, 6, 20)
	skyBottom := rgba(8, 16, 34)
	green := rgba(52, 240, 165)
	violet := rgba(145, 95, 255)
	for y := 0; y < rows; y++ {
		v := float64(y) / float64(max(rows-1, 1))
		for x := 0; x < cols; x++ {
			u := float64(x) / float64(max(cols-1, 1))
			c := mixRGB(skyTop, skyBottom, v)
			curtain := math.Sin(u*8+t*0.9) + 0.6*math.Sin(u*17-t*1.4)
			center := 0.34 + 0.17*math.Sin(u*3+t*0.5)
			band := math.Exp(-math.Pow((v-center-0.07*curtain)/0.15, 2))
			c = mixRGB(c, green, band*0.80)
			top := math.Exp(-math.Pow((v-center+0.27)/0.18, 2))
			c = mixRGB(c, violet, top*0.55)
			out[y*cols+x] = c
		}
	}
	return out
}

func starsCells(cols, rows int, t float64) []color.RGBA {
	out := make([]color.RGBA, cols*rows)
	for y := 0; y < rows; y++ {
		d := float64(y) / float64(max(rows-1, 1))
		base := mixRGB(rgba(7, 9, 22), rgba(2, 3, 8), d)
		for x := 0; x < cols; x++ {
			out[y*cols+x] = base
		}
	}
	n := cols*rows/12 + 6
	for i := 0; i < n; i++ {
		s := uint32(i*5 + 1)
		x := int(hash01(s) * float64(cols))
		y := int(hash01(s+1) * float64(rows))
		phase := hash01(s + 2)
		twinkle := 0.5 + 0.5*math.Sin(t*(1.1+phase*2.4)+phase*6.283)
		bright := 0.15 + 0.85*math.Pow(twinkle, 3)
		col := mixRGB(rgba(190, 205, 255), rgba(255, 255, 255), phase)
		if x >= 0 && x < cols && y >= 0 && y < rows {
			blendCell(out, y*cols+x, col, 0.25+0.75*bright)
		}
	}
	return out
}

func blendCell(out []color.RGBA, i int, c color.RGBA, a float64) {
	if i < 0 || i >= len(out) {
		return
	}
	out[i] = mixRGB(out[i], c, clamp01(a))
}

func mixRGB(a, b color.RGBA, t float64) color.RGBA {
	t = clamp01(t)
	return color.RGBA{
		R: uint8(float64(a.R)*(1-t) + float64(b.R)*t),
		G: uint8(float64(a.G)*(1-t) + float64(b.G)*t),
		B: uint8(float64(a.B)*(1-t) + float64(b.B)*t),
		A: 255,
	}
}

func rgba(r, g, b float64) color.RGBA {
	return color.RGBA{uint8(clamp01(r/255) * 255), uint8(clamp01(g/255) * 255), uint8(clamp01(b/255) * 255), 255}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// hash01 is a cheap, stable hash in [0,1). The effects need scatter, not
// cryptography, and a package-level rand would make every frame depend on the
// previous one.
func hash01(n uint32) float64 {
	x := n*747796405 + 2891336453
	x = ((x >> ((x >> 28) + 4)) ^ x) * 277803737
	x = (x >> 22) ^ x
	return float64(x) / float64(math.MaxUint32)
}
