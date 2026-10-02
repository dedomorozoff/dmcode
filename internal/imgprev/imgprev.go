// Package imgprev draws a small picture of an image in the terminal, so the user
// can tell what they are about to send without leaving the prompt.
//
// It is deliberately separate from the UI and from the wire. What it produces is
// a string of ANSI-coloured rows that goes into the transcript verbatim, and what
// the model receives is base64 — the two have nothing in common but the bytes
// they were made from, and coupling them would mean the renderer had to know
// about sessions and the wire had to know about glyphs.
package imgprev

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// The blank imports register the formats dmcode accepts: image.Decode dispatches
// on what those register, so the import is the whole mechanism — there is no
// registry to consult and no sniff to write.
//
// WebP is absent on purpose: it needs golang.org/x/image, and a decoder is a
// dependency kept alive for as long as this feature exists. A user with a WebP
// gets a named error rather than a silent no-op.
var (
	_ = png.Decode
	_ = jpeg.Decode
	_ = gif.Decode
)

// ErrUnsupportedImage is what Load reports for a format dmcode cannot read. The
// message names the format so the user is not left wondering whether the file
// was missing.
var ErrUnsupportedImage = errors.New("unsupported image format")

// maxEdge is the longest side, in pixels, an image is reduced to before it goes
// anywhere.
//
// The number is the one the vision APIs quote for "the largest image is worth
// sending", and it is chosen for the wire rather than for the screen: a 4K
// screenshot is several megabytes, which becomes several megabytes of base64,
// which then sits inside every session file *and* inside every subsequent
// request in the conversation. Below this the picture is still legible to a
// model; above it, the extra pixels cost tokens and buy nothing.
const maxEdge = 1568

// maxBytes caps what is handed on. Four megabytes becomes roughly 5.5 MB of
// base64, which fits inside the session store's 8 MB per-line limit
// (memsession.maxLineBytes) with room for the event around it. Going over that
// is not a slow path, it is a lost turn: the store's reader skips a line it
// cannot scan, so the event would vanish and the model's memory would lose a
// message with nothing on screen to say so.
const maxBytes = 4 << 20

// Ramp is the ASCII fallback: density from dark to light. Used when the terminal
// has no colour at all, where half-blocks would still draw *something* but the
// colour carries none of the image, so shape has to carry all of it.
const Ramp = "@%#*+=-:. "

// Preview is a rendered picture: the rows to print, and the facts about the
// image that go in the caption under them.
type Preview struct {
	// Art is the coloured block, rows joined by "\n" and carrying no trailing
	// newline. It is stored verbatim in the transcript, which is why it must
	// already be at its final width.
	Art string
	// Rows is how many terminal rows Art occupies.
	Rows int
	// Width is how many cells one row of Art spans, escape sequences excluded.
	Width int

	// PixelWidth and PixelHeight are the source dimensions, before any
	// reduction. They are what the caption reports, because a user asking "what
	// am I sending" means the file they picked, not the smaller thing on the
	// wire.
	PixelWidth, PixelHeight int
	// Data is the encoded image to send: the original when it was already
	// small enough, otherwise a re-encode at maxEdge.
	Data []byte
	// MIME is the media type of Data. A re-encode changes it, and reporting the
	// original type for a PNG that is now a JPEG would be a lie on the wire.
	MIME string
}

// Encode writes img in the format the MIME type names.
func Encode(w interface{ Write([]byte) (int, error) }, img image.Image, mime string) error {
	switch mime {
	case "image/png":
		return png.Encode(w, img)
	case "image/jpeg":
		return jpeg.Encode(w, img, &jpeg.Options{Quality: 85})
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedImage, mime)
	}
}

// Attachment is what Load produces for one file.
type Attachment struct {
	Preview
	// Name is the file's base name, for the caption.
	Name string
}

// Load decodes, reduces and renders one image.
//
// The order matters: reduction happens before rendering so the sampler walks a
// bounded grid, and before the wire copy so the bytes that go into the session
// are the same ones the preview is a picture of. A user who sees a 1568px
// preview is looking at exactly what the model gets.
//
// bleed is how many extra cells of the picture go to the left, so that the
// picture reaches the panel border instead of stopping short of it.
//
// It is part of the image, not a margin painted beside it, and that is the whole
// point. A margin of spaces carries the terminal's own background, which on a dark
// terminal reads as a column of the image that is out of step with the rest; and a
// margin painted in a sampled colour is worse, because a space can hold only one
// colour while the cell beside it carries two — so a seam appears down the edge
// wherever the image changes top to bottom. Sampling `cols+bleed` cells makes the
// edge continuous, which is what a bleed is for.
func Load(data []byte, name string, cols, bleed int, profile colorprofile.Profile) (Attachment, error) {
	if len(data) == 0 {
		return Attachment{}, errors.New("the file is empty")
	}
	if cols < 1 {
		cols = 1
	}
	if bleed < 0 {
		bleed = 0
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Attachment{}, unsupported(format, err)
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Attachment{}, unsupported(format, err)
	}

	out := Preview{
		PixelWidth:  cfg.Width,
		PixelHeight: cfg.Height,
		Data:        data,
		MIME:        mimeFor(format),
	}

	// A small image goes on the wire untouched. Re-encoding a PNG that was
	// already inside the limit would only lose quality and add bytes.
	if max(cfg.Width, cfg.Height) > maxEdge {
		small := fit(img, maxEdge)
		var buf bytes.Buffer
		mime := out.MIME
		// JPEG has no alpha, so a picture with transparency is re-encoded as
		// PNG or the transparent parts come out black.
		if hasAlpha(small) {
			mime = "image/png"
		}
		if err := Encode(&buf, small, mime); err != nil {
			return Attachment{}, err
		}
		if buf.Len() > maxBytes {
			return Attachment{}, tooLarge(buf.Len(), "after reduction")
		}
		img = small
		out.Data, out.MIME = buf.Bytes(), mime
	} else if len(data) > maxBytes {
		return Attachment{}, tooLarge(len(data), "")
	}

	art, rows := render(img, cols+bleed, profile)
	out.Art, out.Rows, out.Width = art, rows, cols+bleed
	return Attachment{Name: name, Preview: out}, nil
}

// tooLarge reports a file over the wire limit, naming the step that produced the
// number so the user knows whether shrinking the file would help.
func tooLarge(n int, note string) error {
	if note != "" {
		note = " " + note
	}
	return fmt.Errorf("the image is too large to send (%d KB%s; the limit is %d KB)",
		n/1024, note, maxBytes/1024)
}

func unsupported(format string, err error) error {
	if format == "" {
		// DecodeConfig failed before it could name a format, so the bytes are
		// not an image this build reads. Saying so beats passing the decoder's
		// "unknown format" up as-is, which does not name dmcode as the limit.
		return fmt.Errorf("%w: this build reads png, jpeg and gif", ErrUnsupportedImage)
	}
	return fmt.Errorf("%w: %s", ErrUnsupportedImage, format)
}

func mimeFor(format string) string {
	switch format {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	}
	return ""
}

// fit scales img so its longest side is at most max, preserving the aspect
// ratio, and returns it as *image.RGBA.
//
// The filter is a box average over the source rectangle each destination pixel
// covers, which is what keeps a screenshot's text from turning into moiré: a
// nearest-neighbour downscale of high-contrast content drops whole rows of
// glyphs and leaves a legible-looking but wrong picture. Averaging is also why
// the result is in RGBA — the average of two opaque pixels is a third opaque
// pixel, and the math is far clearer than three channels at a time on an
// arbitrary color model.
func fit(img image.Image, edge int) *image.RGBA {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw < 1 || sh < 1 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	scale := math.Min(1, float64(edge)/float64(max(sw, sh)))
	dw := max(1, int(math.Round(float64(sw)*scale)))
	dh := max(1, int(math.Round(float64(sh)*scale)))

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	if dw == sw && dh == sh {
		for y := 0; y < dh; y++ {
			for x := 0; x < dw; x++ {
				dst.Set(x, y, img.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	}

	// Accumulated in int64 because a wide source rectangle covers a lot of
	// pixels and their 16-bit channels overflow uint32 well before the average
	// does.
	for y := 0; y < dh; y++ {
		y0 := b.Min.Y + y*sh/dh
		y1 := b.Min.Y + (y+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0 := b.Min.X + x*sw/dw
			x1 := b.Min.X + (x+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, al, n int64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := img.At(sx, sy).RGBA()
					// Each channel arrives as 0..65535. Dividing by 257 first
					// brings the sum to a per-pixel 0..255 scale, so one n serves
					// every channel and the average cannot overflow.
					r += int64(cr >> 8)
					g += int64(cg >> 8)
					bl += int64(cb >> 8)
					al += int64(ca >> 8)
					n++
				}
			}
			if n == 0 {
				continue
			}
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(r / n), G: uint8(g / n), B: uint8(bl / n), A: uint8(al / n),
			})
		}
	}
	return dst
}

// hasAlpha reports whether any pixel is not fully opaque.
func hasAlpha(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a < 0xffff {
				return true
			}
		}
	}
	return false
}

// render draws the picture as half-blocks.
//
// The upper half block ▀ paints the cell's foreground over its top half and its
// background over the bottom, so one cell carries two vertical pixels. That is
// the whole trick: two pixels per cell is what makes a 32-column preview legible
// as a picture instead of a smear, and it needs no block characters that some
// terminals render as emoji.
//
// The sample grid is cols wide and 2*rows tall, and rows follows the source
// aspect ratio through a cell that is twice as tall as it is wide. Getting that
// divisor wrong is the classic bug here: sample one pixel per cell and every
// preview comes out twice as tall as the image.
func render(img image.Image, cols int, profile colorprofile.Profile) (string, int) {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw < 1 || sh < 1 || cols < 1 {
		return "", 0
	}

	// Two pixels of height per cell, and a cell is twice as tall as it is wide.
	rows := max(1, int(math.Round(float64(cols)*float64(sh)/float64(sw)/2)))
	pixH := rows * 2

	if profile <= colorprofile.ASCII {
		return renderRamp(img, cols, rows), rows
	}

	var out strings.Builder
	for row := 0; row < rows; row++ {
		for x := 0; x < cols; x++ {
			top := sample(img, sw, sh, x, row*2, cols, pixH)
			bot := sample(img, sw, sh, x, row*2+1, cols, pixH)
			out.WriteString(paint(top, bot, profile))
		}
		// Reset at the end of the row, not once at the end of the picture. Every
		// cell re-states both of its colours, so the one that matters is the last
		// cell's: without this its background stays current across the newline
		// and paints the caption under the picture, or the next row, in the
		// colour of the image's bottom-right pixel.
		out.WriteString(ansi.Style{}.String())
		out.WriteByte('\n')
	}
	return strings.TrimSuffix(out.String(), "\n"), rows
}

// sample returns the average colour of the source rectangle covering the target
// pixel. The arithmetic mirrors fit: integer edges, no nearest-neighbour.
func sample(img image.Image, sw, sh, tx, ty, tw, th int) color.RGBA {
	x0 := tx * sw / tw
	x1 := (tx + 1) * sw / tw
	y0 := ty * sh / th
	y1 := (ty + 1) * sh / th
	if x1 <= x0 {
		x1 = x0 + 1
	}
	if y1 <= y0 {
		y1 = y0 + 1
	}
	b := img.Bounds()
	var r, g, bl, a, n int64
	for sy := y0; sy < y1; sy++ {
		for sx := x0; sx < x1; sx++ {
			cr, cg, cb, ca := img.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
			r += int64(cr >> 8)
			g += int64(cg >> 8)
			bl += int64(cb >> 8)
			a += int64(ca >> 8)
			n++
		}
	}
	if n == 0 {
		return color.RGBA{}
	}
	return color.RGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(bl / n), A: uint8(a / n)}
}

// paint writes one ▀ with the top pixel in the foreground and the bottom in the
// background.
//
// Each colour is converted to the profile's palette here rather than handed to
// lipgloss, because the two halves of a cell are two separate colours and a style
// cannot carry "this glyph, foreground one thing and background another" per
// glyph. ANSI 16 loses most of an image, but a 16-colour half-block preview is
// still a picture; without the conversion it would be truecolor escapes sent to a
// terminal that cannot read them.
func paint(top, bot color.RGBA, profile colorprofile.Profile) string {
	fg, bg := color.Color(top), color.Color(bot)
	switch profile {
	case colorprofile.TrueColor:
		// Nothing to do: RGBColor is what the wire already wants.
	case colorprofile.ANSI256:
		fg, bg = ansi.Convert256(fg), ansi.Convert256(bg)
	default:
		fg, bg = ansi.Convert16(fg), ansi.Convert16(bg)
	}
	return ansi.Style{}.ForegroundColor(fg).BackgroundColor(bg).String() + "▀"
}

// renderRamp is the no-colour fallback: one ramp character per cell, by
// luminance.
//
// Half-blocks would still be *drawn* on such a terminal, but with the colour
// stripped out what is left is a field of identical blocks — the shape of a
// rectangle, carrying none of the picture. The ramp puts the information back
// into the glyph, which is the only channel left.
//
// The grid is one sample per cell, cols by rows, covering the whole image. rows
// already carries the 2:1 cell aspect, so sampling rows*2 deep would read only
// the top of the picture — the bug this comment exists to prevent, and the one
// that makes a ramp preview show the wrong part of the image.
func renderRamp(img image.Image, cols, rows int) string {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	var out strings.Builder
	for row := 0; row < rows; row++ {
		for x := 0; x < cols; x++ {
			c := sample(img, sw, sh, x, row, cols, rows)
			lum := (0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)) / 255
			i := int(lum * float64(len(Ramp)-1))
			out.WriteByte(Ramp[min(i, len(Ramp)-1)])
		}
		out.WriteByte('\n')
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// Caption is the line printed under the picture. It is built here rather than
// at the call site so the numbers on screen are the numbers Load measured.
func (p Preview) Caption(index, total int, name string) string {
	label := "image"
	if total > 1 {
		label = fmt.Sprintf("image %d/%d", index, total)
	}
	return fmt.Sprintf("[%s] %s · %d×%d · %s", label, name, p.PixelWidth, p.PixelHeight, Bytes(len(p.Data)))
}

// Bytes formats a byte count the way a person reads it.
func Bytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
