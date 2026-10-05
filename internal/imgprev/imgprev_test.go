package imgprev

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// solid builds a one-colour PNG of the given size.
func solid(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// quadrants is a 2x2 image: red top-left, green top-right, blue bottom-left,
// white bottom-right. Every half-block preview of it must show all four.
func quadrants(t *testing.T, n int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	cols := []color.RGBA{
		{R: 255, A: 255}, {G: 255, A: 255},
		{B: 255, A: 255}, {R: 255, G: 255, B: 255, A: 255},
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			img.SetRGBA(x, y, cols[(y/(n/2))*2+x/(n/2)])
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// TestSolidColourSurvivesTheRoundTrip is the basic contract: an image of one
// colour comes back as a preview of that colour, and what goes on the wire is
// still a PNG the model can decode.
func TestSolidColourSurvivesTheRoundTrip(t *testing.T) {
	want := color.RGBA{R: 0x33, G: 0x66, B: 0x99, A: 255}
	data := solid(t, 8, 8, want)
	a, err := Load(data, "flat.png", 16, 0, colorprofile.TrueColor)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if a.MIME != "image/png" {
		t.Errorf("MIME = %q, want image/png", a.MIME)
	}
	if a.PixelWidth != 8 || a.PixelHeight != 8 {
		t.Errorf("dimensions = %dx%d, want 8x8", a.PixelWidth, a.PixelHeight)
	}
	if !strings.Contains(a.Art, "▀") {
		t.Fatal("the art carries no half blocks")
	}
	if !strings.Contains(a.Art, "51;102;153") {
		t.Errorf("the art does not carry the source colour as truecolor: %q", a.Art)
	}
	// An image already inside the limit is not re-encoded: the bytes on the wire
	// are the bytes that were read.
	if !bytes.Equal(a.Data, data) {
		t.Error("a small image was re-encoded; it should go on the wire untouched")
	}
}

// TestRowsFollowTheAspectRatio pins the divisor that makes a half-block preview
// look like the image rather than twice as tall as it.
func TestRowsFollowTheAspectRatio(t *testing.T) {
	for _, tc := range []struct{ w, h, cols, wantRows int }{
		{w: 100, h: 100, cols: 20, wantRows: 10}, // square, 2 pixels per cell
		{w: 200, h: 100, cols: 20, wantRows: 5},  // twice as wide, half as many rows
		{w: 100, h: 200, cols: 20, wantRows: 20}, // twice as tall, twice as many rows
		{w: 400, h: 100, cols: 20, wantRows: 3},  // panorama, never zero
	} {
		a, err := Load(solid(t, tc.w, tc.h, color.RGBA{A: 255}), "x.png", tc.cols, 0, colorprofile.TrueColor)
		if err != nil {
			t.Fatalf("Load %dx%d: %v", tc.w, tc.h, err)
		}
		if a.Rows != tc.wantRows {
			t.Errorf("%dx%d at %d cols: rows = %d, want %d", tc.w, tc.h, tc.cols, a.Rows, tc.wantRows)
		}
		if got := len(strings.Split(a.Art, "\n")); got != tc.wantRows {
			t.Errorf("%dx%d: art has %d rows, want %d", tc.w, tc.h, got, tc.wantRows)
		}
	}
}

// TestArtWidthIsExactlyCols guards the property the transcript depends on: the
// art is stored verbatim, and a row wider than the panel is dropped whole, so
// the width has to be right at the moment it is produced rather than trimmed
// later.
func TestArtWidthIsExactlyCols(t *testing.T) {
	for _, cols := range []int{1, 8, 32, 200} {
		a, err := Load(quadrants(t, 8), "q.png", cols, 0, colorprofile.TrueColor)
		if err != nil {
			t.Fatalf("Load at %d cols: %v", cols, err)
		}
		for i, row := range strings.Split(a.Art, "\n") {
			if got := ansi.StringWidth(row); got != cols {
				t.Errorf("at %d cols, row %d is %d cells wide, want %d", cols, i, got, cols)
			}
		}
		if a.Width != cols {
			t.Errorf("Width = %d, want %d", a.Width, cols)
		}
	}
}

// TestTheBleedIsPartOfThePicture is the fix for a preview with a stripe down its
// left side.
//
// The transcript indents every row, so the picture has to reach the panel border
// rather than stop short of it. Two ways to do that were tried: a margin of bare
// spaces, which carries the terminal's own background and so shows as a dark
// column beside the image; and a margin painted in a sampled colour, which is
// worse, because a space can hold only one colour while the cell beside it carries
// two — so a seam runs down the edge wherever the image changes top to bottom.
//
// The bleed is the third way: the extra cells are sampled from the image like any
// other, so the edge is continuous and there is nothing beside it to disagree.
func TestTheBleedIsPartOfThePicture(t *testing.T) {
	// Red on the left, cyan on the right, so a border between them is visible and
	// a continuous picture is not.
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			c := color.RGBA{B: 200, G: 200, A: 255}
			if x < 16 {
				c = color.RGBA{R: 200, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}

	a, err := Load(buf.Bytes(), "edge.png", 16, 2, colorprofile.TrueColor)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Every cell is a half block: no spaces at all, at either edge.
	for i, row := range strings.Split(a.Art, "\n") {
		if strings.ContainsAny(row, " ") {
			t.Errorf("row %d has a space in it; the bleed must be picture, not margin: %q",
				i, ansi.Strip(row))
		}
		if got := ansi.StringWidth(row); got != 18 {
			t.Errorf("row %d is %d cells wide, want 18", i, got)
		}
		if n := strings.Count(row, "▀"); n != 18 {
			t.Errorf("row %d has %d half blocks, want 18", i, n)
		}
	}
	if a.Width != 18 {
		t.Errorf("Width = %d, want 18", a.Width)
	}
	// And the bleed is image content: the leftmost cells carry the image's left
	// colour, not the terminal's.
	if !strings.Contains(a.Art, "200;0;0") {
		t.Errorf("the bleed is not showing the picture:\n%q", a.Art)
	}
}

// TestZeroBleedIsUnchanged keeps the bleed out of the geometry when none is asked
// for: with bleed 0 the art is exactly cols wide, as it was before.
func TestZeroBleedIsUnchanged(t *testing.T) {
	a, err := Load(quadrants(t, 8), "q.png", 16, 0, colorprofile.TrueColor)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i, row := range strings.Split(a.Art, "\n") {
		if got := ansi.StringWidth(row); got != 16 {
			t.Errorf("row %d is %d cells wide, want 16", i, got)
		}
	}
	if a.Width != 16 {
		t.Errorf("Width = %d, want 16", a.Width)
	}
}

// TestTheBleedWidensThePictureNotTheGrid: the rows go up with the picture, since a
// taller grid means more rows of it. A bleed that only widened each row would
// distort the aspect ratio, which is the one thing a preview cannot do.
func TestTheBleedWidensThePictureNotTheGrid(t *testing.T) {
	src := solid(t, 64, 64, color.RGBA{A: 255})
	plain, err := Load(src, "s.png", 20, 0, colorprofile.TrueColor)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	bled, err := Load(src, "s.png", 20, 4, colorprofile.TrueColor)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Square source: rows track half the cells, on both paths.
	if want := plain.Rows + 2; bled.Rows != want {
		t.Errorf("a 4-cell bleed on a square image gave %d rows, want %d",
			bled.Rows, want)
	}
}

// TestTheBleedReachesTheRampToo: the fallback has no colours to lose, but it must
// still be the requested width or the block is narrower on a colourless terminal.
func TestTheBleedReachesTheRampToo(t *testing.T) {
	a, err := Load(quadrants(t, 8), "q.png", 16, 2, colorprofile.ASCII)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i, row := range strings.Split(a.Art, "\n") {
		if got := ansi.StringWidth(row); got != 18 {
			t.Errorf("row %d is %d cells wide, want 18", i, got)
		}
	}
}

// TestEveryQuadrantReachesThePreview is what makes this a preview and not a
// thumbnail of the wrong corner: the sampler has to visit the whole image, so
// all four of red, green, blue and white must appear.
func TestEveryQuadrantReachesThePreview(t *testing.T) {
	a, err := Load(quadrants(t, 8), "q.png", 16, 0, colorprofile.TrueColor)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, want := range []string{
		"255;0;0",     // red
		"0;255;0",     // green
		"0;0;255",     // blue
		"255;255;255", // white
	} {
		if !strings.Contains(a.Art, want) {
			t.Errorf("the art is missing %s:\n%q", want, a.Art)
		}
	}
}

// TestProfileDegradation pins the three steps down. The point of each is that the
// preview stays a picture: escapes the terminal cannot read must not be sent, and
// a colourless terminal must get shape rather than a field of identical blocks.
func TestProfileDegradation(t *testing.T) {
	src := solid(t, 8, 8, color.RGBA{R: 0x33, G: 0x66, B: 0x99, A: 255})

	t.Run("ansi256 quantises", func(t *testing.T) {
		a, err := Load(src, "flat.png", 8, 0, colorprofile.ANSI256)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if strings.Contains(a.Art, "51;102;153") {
			t.Error("truecolor escapes reached an ANSI256 terminal")
		}
		if !strings.Contains(a.Art, "38;5;") {
			t.Errorf("the art carries no 256-color escapes:\n%q", a.Art)
		}
	})

	t.Run("ansi16 quantises", func(t *testing.T) {
		a, err := Load(src, "flat.png", 8, 0, colorprofile.ANSI)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if strings.Contains(a.Art, "38;5;") {
			t.Error("256-color escapes reached a 16-color terminal")
		}
		if !strings.Contains(a.Art, "\x1b[3") {
			t.Errorf("the art carries no 16-color escapes:\n%q", a.Art)
		}
	})

	t.Run("ascii falls back to a ramp", func(t *testing.T) {
		a, err := Load(quadrants(t, 8), "q.png", 16, 0, colorprofile.ASCII)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if strings.Contains(a.Art, "\x1b") {
			t.Errorf("escape sequences reached a terminal with no colour:\n%q", a.Art)
		}
		if strings.Contains(a.Art, "▀") {
			t.Errorf("half blocks were used with no colour to give them:\n%q", a.Art)
		}
		// Only the glyphs this fixture's four luminances land on are expected, so
		// the check is that brightness separates them. A ramp that returned one
		// glyph for everything would satisfy "some ramp glyph is present" and
		// still carry none of the picture.
		for _, tc := range []struct{ want, why string }{
			{"%", "blue, the darkest"},
			{"#", "red"},
			{"=", "green"},
			{" ", "white, the brightest"},
		} {
			if !strings.Contains(a.Art, tc.want) {
				t.Errorf("%s is not shown as %q:\n%q", tc.why, tc.want, a.Art)
			}
		}
	})

	t.Run("the ramp is as tall as the coloured path", func(t *testing.T) {
		// Half the rows would make the fallback a visibly different picture of
		// the same image, which is worse than a rougher one.
		quad := quadrants(t, 8)
		coloured, err := Load(quad, "q.png", 16, 0, colorprofile.TrueColor)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		plain, err := Load(quad, "q.png", 16, 0, colorprofile.ASCII)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if plain.Rows != coloured.Rows {
			t.Errorf("ramp rows = %d, coloured rows = %d: they must match", plain.Rows, coloured.Rows)
		}
	})
}

// TestReductionHappensBeforeTheWire pins the ordering that keeps the preview
// honest: what is drawn is what is sent.
func TestReductionHappensBeforeTheWire(t *testing.T) {
	big := solid(t, 3000, 2000, color.RGBA{R: 10, G: 200, B: 30, A: 255})
	a, err := Load(big, "huge.png", 16, 0, colorprofile.TrueColor)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The caption reports the file the user picked, not the smaller thing.
	if a.PixelWidth != 3000 || a.PixelHeight != 2000 {
		t.Errorf("dimensions = %dx%d, want the source 3000x2000", a.PixelWidth, a.PixelHeight)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(a.Data))
	if err != nil {
		t.Fatalf("the wire bytes are not a decodable image: %v", err)
	}
	if got := max(cfg.Width, cfg.Height); got > maxEdge {
		t.Errorf("the wire image is %dx%d, so nothing was reduced (limit %d)", cfg.Width, cfg.Height, maxEdge)
	}
	if got, want := cfg.Width/cfg.Height, 3000/2000; got != want {
		t.Errorf("aspect ratio = %d, want %d — reduction must not distort", got, want)
	}
}

// TestTransparencyKeepsPNG pins the one case where a re-encode would change what
// the model sees: JPEG has no alpha, so a transparent picture re-encoded as JPEG
// comes back with black where the transparency was.
func TestTransparencyKeepsPNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 255, A: 0})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	a, err := Load(buf.Bytes(), "alpha.png", 8, 0, colorprofile.TrueColor)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if a.MIME != "image/png" {
		t.Errorf("MIME = %q: a transparent image was re-encoded and lost its alpha", a.MIME)
	}
}

// TestOversizeIsRefusedNotTruncated pins the limit that is not a slow path but a
// lost turn: base64 of an over-limit image does not fit the session store's
// per-line cap, and the store's reader skips a line it cannot scan.
func TestOversizeIsRefusedNotTruncated(t *testing.T) {
	// Random-ish noise compresses badly, so this reliably crosses the limit.
	img := image.NewRGBA(image.Rect(0, 0, 1600, 1600))
	seed := uint32(12345)
	for y := 0; y < 1600; y++ {
		for x := 0; x < 1600; x++ {
			seed = seed*1664525 + 1013904223
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(seed >> 16), G: uint8(seed >> 8), B: uint8(seed), A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if buf.Len() <= maxBytes {
		t.Skipf("the fixture is only %d KB, so it cannot cross the %d KB limit", buf.Len()/1024, maxBytes/1024)
	}
	if _, err := Load(buf.Bytes(), "noise.png", 8, 0, colorprofile.TrueColor); err == nil {
		t.Error("an oversized image was accepted; it would be silently lost with the session")
	} else if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %v, want one naming the size", err)
	}
}

// TestUnsupportedFormatsAreNamed keeps the failure legible: "unsupported" with
// nothing attached leaves the user guessing whether the file was missing.
func TestUnsupportedFormatsAreNamed(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"text", []byte("this is not an image, it is a sentence")},
		{"webp", append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 32)...)},
	} {
		_, err := Load(tc.data, tc.name, 8, 0, colorprofile.TrueColor)
		if err == nil {
			t.Errorf("%s: Load accepted it", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), "image") && !strings.Contains(err.Error(), "empty") {
			t.Errorf("%s: error = %v, want one that names the problem", tc.name, err)
		}
	}
}

// TestErrorsAreInspectable lets the UI tell "this format I cannot read" from
// "this file is not there", which are different problems with different fixes.
func TestErrorsAreInspectable(t *testing.T) {
	_, err := Load([]byte("not an image"), "x.webp", 8, 0, colorprofile.TrueColor)
	if !errors.Is(err, ErrUnsupportedImage) {
		t.Errorf("error = %v, want one matching ErrUnsupportedImage", err)
	}
}

// TestDegenerateSizesDoNotPanic: a caller that asks for zero columns, or hands a
// one-pixel image, must get an answer rather than a division by zero.
func TestDegenerateSizesDoNotPanic(t *testing.T) {
	for _, tc := range []struct{ w, h, cols int }{
		{1, 1, 0}, {1, 1, 1}, {1, 4000, 8}, {4000, 1, 8}, {3, 7, 1},
	} {
		if _, err := Load(solid(t, tc.w, tc.h, color.RGBA{A: 255}), "d.png", tc.cols, 2, colorprofile.TrueColor); err != nil {
			t.Errorf("%dx%d at %d cols: %v", tc.w, tc.h, tc.cols, err)
		}
	}
}

// TestNegativeMarginIsIgnored rather than panicking or inverting the block.
func TestNegativeMarginIsIgnored(t *testing.T) {
	a, err := Load(solid(t, 8, 8, color.RGBA{A: 255}), "d.png", 8, -3, colorprofile.TrueColor)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i, row := range strings.Split(a.Art, "\n") {
		if got := ansi.StringWidth(row); got != 8 {
			t.Errorf("row %d is %d cells wide, want 8", i, got)
		}
	}
}

// TestCaptionNamesTheFile guards the reason the preview exists at all: the user
// is deciding whether to send, and "what is this" has to be answerable from the
// screen.
func TestCaptionNamesTheFile(t *testing.T) {
	a, err := Load(quadrants(t, 8), "screenshot.png", 8, 0, colorprofile.TrueColor)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := a.Caption(1, 1, a.Name)
	for _, want := range []string{"[image]", "screenshot.png", "8×8"} {
		if !strings.Contains(got, want) {
			t.Errorf("caption %q is missing %q", got, want)
		}
	}
	if two := a.Caption(2, 3, a.Name); !strings.Contains(two, "image 2/3") {
		t.Errorf("caption %q does not number itself among three", two)
	}
}

// TestBytesIsReadable: the caption's size has to mean something at a glance.
func TestBytesIsReadable(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{512, "512 B"},
		{2048, "2 KB"},
		{5 << 20, "5 MB"},
	} {
		if got := Bytes(tc.n); got != tc.want {
			t.Errorf("Bytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// TestEveryRowEndsWithAReset is the fix for a caption drawn in the picture's
// colour.
//
// Every cell re-states both of its colours, so the one that survives a row is
// the last cell's background. Without a reset at the end of the row that
// background stays current across the newline, and the caption under the art — or
// whatever the terminal draws next — is painted in the colour of the image's
// bottom-right pixel. It is invisible on a picture whose edges happen to be dark,
// which is why it survives a look at a screenshot and fails in a test.
func TestEveryRowEndsWithAReset(t *testing.T) {
	for _, profile := range []colorprofile.Profile{
		colorprofile.TrueColor, colorprofile.ANSI256, colorprofile.ANSI,
	} {
		t.Run(profile.String(), func(t *testing.T) {
			a, err := Load(quadrants(t, 8), "q.png", 8, 0, profile)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			for i, row := range strings.Split(a.Art, "\n") {
				if !strings.HasSuffix(row, ansi.Style{}.String()) {
					t.Errorf("row %d does not end in a reset: %q", i, row)
				}
			}
		})
	}
}

// TestTheResetDoesNotWidenTheRow: the reset is zero width, so adding it must not
// change the measured width of a row. A row that came out one cell too wide would
// be dropped whole by a panel that fits the art exactly.
func TestTheResetDoesNotWidenTheRow(t *testing.T) {
	a, err := Load(quadrants(t, 8), "q.png", 8, 0, colorprofile.TrueColor)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i, row := range strings.Split(a.Art, "\n") {
		if got := ansi.StringWidth(row); got != 8 {
			t.Errorf("row %d is %d cells wide with the reset, want 8", i, got)
		}
	}
}

// TestTheRampIsLeftAlone: the no-colour fallback emits no escapes, so there is
// nothing to reset and a reset added to it would be visible on any terminal that
// shows control characters.
func TestTheRampIsLeftAlone(t *testing.T) {
	a, err := Load(quadrants(t, 8), "q.png", 8, 0, colorprofile.ASCII)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Contains(a.Art, "\x1b") {
		t.Errorf("the ramp carries escapes it has no colour to need:\n%q", a.Art)
	}
}

// TestRampIndexEndsAtTheLightestGlyph tests the arithmetic directly, because
// the failure it guards is arithmetic that misbehaves on an architecture the
// test is not running on. Go fuses multiply-add into a single fused
// multiply-add on arm64, rounding once where an x86 build rounds three times,
// and for pure white that lands the luminance a hair under full. int()
// truncates, so the cell short of the end: the brightest thing in the picture
// drawn as the second-darkest glyph, and a white image rendered as a field of
// dots. The endpoint is only visible on the machine that has the fused
// multiply-add, so what is pinned here is the arithmetic behind it.
func TestRampIndexEndsAtTheLightestGlyph(t *testing.T) {
	first := Ramp[0]
	last := Ramp[len(Ramp)-1]
	if first == last {
		t.Fatal("the ramp's two ends are the same glyph, so brightness cannot separate")
	}

	for _, tc := range []struct {
		name string
		c    color.RGBA
		want byte
	}{
		{"black is the first glyph", color.RGBA{A: 255}, first},
		{"white is the last glyph", color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 255}, last},
	} {
		if got := Ramp[rampIndex(tc.c)]; got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}

	// One division, one rounding, at the end. A version that rounds the
	// luminance to an integer first throws away the fraction that decides the
	// cell and moves a colour onto its neighbour's glyph, and that mistake is
	// invisible except by counting through the whole range, which is what this
	// loop is for. The sweep is one channel, because a grey's weighted sum is
	// already a whole number and would agree with the rounding version
	// everywhere; only a single channel has the fraction that gets thrown away.
	prev := -1
	for v := 0; v <= 0xff; v++ {
		got := rampIndex(color.RGBA{R: uint8(v), A: 255})
		want := (299 * v) * (len(Ramp) - 1) / (255 * 1000)
		if got != want {
			t.Errorf("rampIndex(red %d) = %d, want the exact ratio truncated, %d", v, got, want)
		}
		if got < prev {
			t.Fatalf("rampIndex goes backwards at red %d: %d after %d", v, got, prev)
		}
		prev = got
	}
}
