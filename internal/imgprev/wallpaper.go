package imgprev

import (
	"image"
	"image/color"
	"math"
)

// SampleCover fills a cols×rows grid of terminal cells with the average colour
// of the part of img that cell covers. The picture is scaled to cover the grid
// rather than to fit it: a wallpaper with empty bars down two sides would be a
// picture in a frame, not a wallpaper.
//
// Terminal cells are about twice as tall as they are wide, so the target aspect
// is cols/(rows*2). Without that correction a square photograph comes out
// stretched vertically by a factor of two on the screen.
//
// The result is row-major, len == cols*rows. An empty grid returns nil.
func SampleCover(img image.Image, cols, rows int) []color.RGBA {
	if img == nil || cols < 1 || rows < 1 {
		return nil
	}
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw < 1 || sh < 1 {
		return nil
	}

	// Crop the source to the grid's aspect before sampling, so the scale is a
	// cover rather than a fit and the lost part is the same on both sides.
	target := float64(cols) / float64(rows*2)
	srcAspect := float64(sw) / float64(sh)
	x0, y0 := b.Min.X, b.Min.Y
	cw, ch := sw, sh
	if srcAspect > target {
		cw = max(1, int(float64(sh)*target))
		x0 += (sw - cw) / 2
	} else if srcAspect < target {
		ch = max(1, int(float64(sw)/target))
		y0 += (sh - ch) / 2
	}

	out := make([]color.RGBA, cols*rows)
	for y := 0; y < rows; y++ {
		sy0 := y0 + y*ch/rows
		sy1 := y0 + (y+1)*ch/rows
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < cols; x++ {
			sx0 := x0 + x*cw/cols
			sx1 := x0 + (x+1)*cw/cols
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			// A fixed 3x3 probe, not every pixel in the cell. An animated
			// wallpaper is resampled on every frame, and a full average over a
			// 1080p GIF costs more than drawing the interface does. Three probes
			// per axis keep edges and gradients while the work stays proportional
			// to the grid rather than to the source resolution.
			//
			// Colour is accumulated premultiplied and divided back out at the
			// end. Averaging non-premultiplied RGB with transparent probes would
			// let a transparent edge darken the colour it is blended over, which
			// the compositor then blends a second time.
			const probes = 3
			var r, g, bl, al int64
			for iy := 0; iy < probes; iy++ {
				sy := sy0 + (iy*2+1)*(sy1-sy0)/(probes*2)
				for ix := 0; ix < probes; ix++ {
					sx := sx0 + (ix*2+1)*(sx1-sx0)/(probes*2)
					c := color.NRGBAModel.Convert(img.At(sx, sy)).(color.NRGBA)
					a := int64(c.A)
					r += int64(c.R) * a
					g += int64(c.G) * a
					bl += int64(c.B) * a
					al += a
				}
			}
			if al == 0 {
				out[y*cols+x] = color.RGBA{}
				continue
			}
			out[y*cols+x] = color.RGBA{
				R: uint8(r / al), G: uint8(g / al), B: uint8(bl / al),
				A: uint8(al / (probes * probes)),
			}
		}
	}
	return out
}

// Fit shrinks an image so neither side exceeds maxW/maxH, preserving its
// aspect. It is a nearest-neighbour scale on purpose: the result is averaged
// into terminal cells a moment later, so a smoother filter would be work done
// before the same loss of detail.
//
// A wallpaper is held in memory for as long as it is on screen, and an
// animated one holds every frame. Decoding a 1080p GIF at full size would cost
// hundreds of megabytes to paint a grid of a few thousand cells.
func Fit(img image.Image, maxW, maxH int) image.Image {
	if img == nil || maxW < 1 || maxH < 1 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxW && h <= maxH {
		return img
	}
	scale := math.Min(float64(maxW)/float64(w), float64(maxH)/float64(h))
	nw := max(1, int(float64(w)*scale))
	nh := max(1, int(float64(h)*scale))
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*h/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*w/nw
			dst.SetNRGBA(x, y, color.NRGBAModel.Convert(img.At(sx, sy)).(color.NRGBA))
		}
	}
	return dst
}
