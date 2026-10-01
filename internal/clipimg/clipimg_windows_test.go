//go:build windows

package clipimg

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
	"unsafe"
)

// buildDIB assembles the byte layout a CF_DIB payload has: a BITMAPINFOHEADER
// followed by rows padded to a four-byte boundary.
//
// The decode is a function of these bytes and nothing else, so it is tested
// through them. A test that went through the clipboard instead would be testing
// the operating system's clipboard rather than this code, and it would have to
// destroy whatever the user had copied to do it.
func buildDIB(t *testing.T, img image.Image, bits int, topDown bool, compression uint32) []byte {
	t.Helper()
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	rowBytes := ((w*bits + 31) / 32) * 4
	headerLen := 40
	extra := 0
	if compression == 3 { // BI_BITFIELDS
		extra = 12
	}

	buf := make([]byte, headerLen+extra+rowBytes*h)
	put32 := func(off int, v uint32) {
		buf[off] = byte(v)
		buf[off+1] = byte(v >> 8)
		buf[off+2] = byte(v >> 16)
		buf[off+3] = byte(v >> 24)
	}
	put16 := func(off int, v uint16) {
		buf[off] = byte(v)
		buf[off+1] = byte(v >> 8)
	}
	// biSize is the header alone. The BI_BITFIELDS masks follow it and are not
	// counted, which is what makes them a thing the decoder has to add
	// separately.
	put32(0, uint32(headerLen))
	put32(4, uint32(w))
	if topDown {
		put32(8, uint32(-int32(h))) // negative: top-down
	} else {
		put32(8, uint32(h)) // positive: bottom-up
	}
	put16(12, 1) // planes
	put16(14, uint16(bits))
	put32(16, compression)
	put32(20, uint32(rowBytes*h))

	for y := 0; y < h; y++ {
		src := y
		if !topDown {
			src = h - 1 - y // stored bottom-up
		}
		row := buf[headerLen+extra+src*rowBytes : headerLen+extra+src*rowBytes+rowBytes]
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			p := x * bits / 8
			row[p] = byte(bl >> 8)
			row[p+1] = byte(g >> 8)
			row[p+2] = byte(r >> 8)
			if bits == 32 {
				row[p+3] = byte(a >> 8)
			}
		}
	}
	return buf
}

// dibAt decodes a DIB held in ordinary memory, standing in for the locked
// clipboard handle decodeDIB is handed. The handle path goes through
// ReadProcessMemory for a reason that has nothing to do with decoding, so this
// exercises the same function without it.
func dibAt(t *testing.T, buf []byte) (image.Image, error) {
	t.Helper()
	if len(buf) == 0 {
		t.Fatal("empty DIB")
	}
	return decodeDIB(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
}

// quadrants is a fixture whose four solid regions make orientation checkable: a
// decoder that flips rows or columns produces the wrong colour at a known
// coordinate, whereas one that merely gets the size right does not.
//
// The fourth region is grey rather than white so that a row read from the wrong
// offset is visibly wrong instead of looking like empty space.
func quadrants(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			var c color.RGBA
			switch {
			case x < w/2 && y < h/2:
				c = color.RGBA{R: 200, A: 255} // top left
			case x >= w/2 && y < h/2:
				c = color.RGBA{G: 200, A: 255} // top right
			case x < w/2:
				c = color.RGBA{B: 200, A: 255} // bottom left
			default:
				c = color.RGBA{R: 50, G: 50, B: 50, A: 255} // bottom right
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func checkPixel(t *testing.T, got image.Image, x, y int, want color.RGBA) {
	t.Helper()
	r, g, b, a := got.At(x, y).RGBA()
	if uint8(r>>8) != want.R || uint8(g>>8) != want.G || uint8(b>>8) != want.B || uint8(a>>8) != want.A {
		t.Errorf("at (%d,%d) got %d,%d,%d,%d want %v", x, y, r>>8, g>>8, b>>8, a>>8, want)
	}
}

// TestDecodeDIBBottomUpIsTheDefault is the shape a screenshot tool puts on the
// clipboard, so it is the one that has to be right.
func TestDecodeDIBBottomUpIsTheDefault(t *testing.T) {
	want := quadrants(8, 8)
	img, err := dibAt(t, buildDIB(t, want, 32, false, 0))
	if err != nil {
		t.Fatalf("decodeDIB: %v", err)
	}
	if got := img.Bounds(); got.Dx() != 8 || got.Dy() != 8 {
		t.Fatalf("decoded %v, want 8x8", got)
	}
	checkPixel(t, img, 1, 1, color.RGBA{R: 200, A: 255})
	checkPixel(t, img, 6, 1, color.RGBA{G: 200, A: 255})
	checkPixel(t, img, 1, 6, color.RGBA{B: 200, A: 255})
	checkPixel(t, img, 6, 6, color.RGBA{R: 50, G: 50, B: 50, A: 255})
}

// TestDecodeDIBTopDownKeepsItsOrientation: a negative height says the rows
// arrive in the order they are drawn, so the decoder must not flip them. Reading
// it as bottom-up turns the image upside down, which is the one mistake here
// that no error message would catch.
func TestDecodeDIBTopDownKeepsItsOrientation(t *testing.T) {
	want := quadrants(8, 8)
	img, err := dibAt(t, buildDIB(t, want, 32, true, 0))
	if err != nil {
		t.Fatalf("decodeDIB: %v", err)
	}
	checkPixel(t, img, 1, 1, color.RGBA{R: 200, A: 255})
	checkPixel(t, img, 6, 1, color.RGBA{G: 200, A: 255})
	checkPixel(t, img, 1, 6, color.RGBA{B: 200, A: 255})
}

// TestDecodeDIB24BitHasNoAlpha: a 24-bit DIB has no fourth byte, so alpha must
// be filled in rather than read from the next pixel's blue channel.
func TestDecodeDIB24BitHasNoAlpha(t *testing.T) {
	want := quadrants(9, 4) // 9px rows are 36 bytes, already 4-aligned
	img, err := dibAt(t, buildDIB(t, want, 24, false, 0))
	if err != nil {
		t.Fatalf("decodeDIB: %v", err)
	}
	if got := img.Bounds(); got.Dx() != 9 || got.Dy() != 4 {
		t.Fatalf("decoded %v, want 9x4", got)
	}
	checkPixel(t, img, 1, 1, color.RGBA{R: 200, A: 255})
	checkPixel(t, img, 6, 1, color.RGBA{G: 200, A: 255})
}

// TestDecodeDIBPadsRowsToFourBytes: a width that is not a multiple of four
// leaves padding at the end of each row, and reading the next row from the
// un-padded offset shears the image diagonally.
func TestDecodeDIBPadsRowsToFourBytes(t *testing.T) {
	want := quadrants(3, 6) // 3px*24bpp = 9 bytes, padded to 12
	for _, bits := range []int{24, 32} {
		img, err := dibAt(t, buildDIB(t, want, bits, false, 0))
		if err != nil {
			t.Fatalf("%d-bit decodeDIB: %v", bits, err)
		}
		// Every row is asserted, not just the corners: a shear shows up as row n
		// holding row n-1's colours, and that is only visible per row.
		for y := range 6 {
			left, right := color.RGBA{R: 200, A: 255}, color.RGBA{G: 200, A: 255}
			if y >= 3 {
				left, right = color.RGBA{B: 200, A: 255}, color.RGBA{R: 50, G: 50, B: 50, A: 255}
			}
			checkPixel(t, img, 0, y, left)
			checkPixel(t, img, 2, y, right)
		}
	}
}

// TestDecodeDIBSkipsTheBitFieldMasks: BI_BITFIELDS puts three masks between the
// header and the pixels. Skipping them is what makes a 16-bit-per-channel DIB
// come out as the image rather than as colour.
func TestDecodeDIBSkipsTheBitFieldMasks(t *testing.T) {
	want := quadrants(8, 8)
	img, err := dibAt(t, buildDIB(t, want, 32, false, 3))
	if err != nil {
		t.Fatalf("decodeDIB: %v", err)
	}
	checkPixel(t, img, 1, 1, color.RGBA{R: 200, A: 255})
	checkPixel(t, img, 6, 6, color.RGBA{R: 50, G: 50, B: 50, A: 255})
}

// TestDecodeDIBRejectsAnUnreadableShape covers the DIBs that must produce an
// error rather than an image. Every one of these is a header that cannot be
// trusted to size its own pixel array, so guessing would read memory the
// clipboard does not own.
func TestDecodeDIBRejectsAnUnreadableShape(t *testing.T) {
	valid := buildDIB(t, quadrants(8, 8), 32, false, 0)

	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"shorter than a header", func(b []byte) []byte { return b[:20] }},
		{"zero width", func(b []byte) []byte { set32(b, 4, 0); return b }},
		{"zero height", func(b []byte) []byte { set32(b, 8, 0); return b }},
		{"absurd width", func(b []byte) []byte { set32(b, 4, 1<<20); return b }},
		{"absurd height", func(b []byte) []byte { set32(b, 8, 1<<20); return b }},
		{"palette depth", func(b []byte) []byte { set16(b, 14, 8); return b }},
		{"compressed", func(b []byte) []byte { set32(b, 16, 1); return b }},
		{"more pixels than bytes", func(b []byte) []byte { set32(b, 8, 4096); return b }},
		{"truncated mid-pixels", func(b []byte) []byte { return b[:len(b)-8] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := dibAt(t, tc.edit(append([]byte(nil), valid...))); err == nil {
				t.Error("decoded a DIB it should have refused")
			}
		})
	}
}

// TestDecodeDIBIgnoresWhatTheHeaderDoesNotAccountFor: a DIB can carry more bytes
// than its header describes, which is normal. The decoder must size itself by
// the header, not by the handle, or a 4 MB DIB would appear to be 4 MB of
// pixels and be rejected.
func TestDecodeDIBIgnoresWhatTheHeaderDoesNotAccountFor(t *testing.T) {
	buf := buildDIB(t, quadrants(8, 8), 32, false, 0)
	buf = append(buf, make([]byte, 4096)...)
	img, err := dibAt(t, buf)
	if err != nil {
		t.Fatalf("decodeDIB: %v", err)
	}
	if got := img.Bounds(); got.Dx() != 8 || got.Dy() != 8 {
		t.Fatalf("decoded %v, want 8x8", got)
	}
}

// TestDecodeDIBEncodesToPNG is the next step the UI takes: whatever comes off the
// clipboard has to survive being written as a PNG, or the attachment never
// reaches the preview.
func TestDecodeDIBEncodesToPNG(t *testing.T) {
	img, err := dibAt(t, buildDIB(t, quadrants(8, 8), 32, false, 0))
	if err != nil {
		t.Fatalf("decodeDIB: %v", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("the encoded PNG is empty")
	}
}

func set32(b []byte, off int, v uint32) {
	b[off] = byte(v)
	b[off+1] = byte(v >> 8)
	b[off+2] = byte(v >> 16)
	b[off+3] = byte(v >> 24)
}

func set16(b []byte, off int, v uint16) {
	b[off] = byte(v)
	b[off+1] = byte(v >> 8)
}

// dibOK is a decodable stand-in, so a test can be about which format was chosen
// rather than about decoding. The pixels are never looked at here.
func dibOK() (image.Image, error) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{R: 200, A: 255})
	return img, nil
}

// errNotFound is the failure Windows reports when a format is advertised but
// cannot be rendered: GetClipboardData returns nothing and the last error is
// ERROR_NOT_FOUND, whose text is "Element not found." This is the one that made a
// pasted screenshot fail.
var errNotFound = errors.New("Element not found.")

// TestAnAdvertisedFormatThatCannotBeRenderedFallsBackToTheNext is the bug this
// whole route had.
//
// IsClipboardFormatAvailable answers whether a format is listed, not whether it
// can be read. The clipboard synthesises formats from the ones it holds, so an
// application that publishes CF_DIBV5 in its list but will not render it leaves
// the format "available" and GetClipboardData returning ERROR_NOT_FOUND. Choosing
// the first advertised format and reporting that as the answer meant a screenshot
// on a perfectly readable CF_DIB was refused.
func TestAnAdvertisedFormatThatCannotBeRenderedFallsBackToTheNext(t *testing.T) {
	tried := []uintptr{}
	img, err := firstImage(
		func(uintptr) bool { return true }, // both advertised
		func(f uintptr) (image.Image, error) {
			tried = append(tried, f)
			if f == cfDIBV5 {
				return nil, errNotFound
			}
			return dibOK()
		})
	if err != nil {
		t.Fatalf("firstImage: %v", err)
	}
	if img == nil {
		t.Fatal("no image returned")
	}
	if len(tried) != 2 || tried[0] != cfDIBV5 || tried[1] != cfDIB {
		t.Errorf("tried %v, want CF_DIBV5 then CF_DIB", tried)
	}
}

// TestAnUnreadableHandleFallsBackToo: a handle that is not a DIB cannot be told
// apart from a missing one until its header is read, so a format that decodes to
// nonsense is a failed attempt, not a result.
func TestAnUnreadableHandleFallsBackToo(t *testing.T) {
	img, err := firstImage(
		func(uintptr) bool { return true },
		func(f uintptr) (image.Image, error) {
			if f == cfDIBV5 {
				return nil, errors.New("the clipboard image is 8-bit, which this build does not read")
			}
			return dibOK()
		})
	if err != nil {
		t.Fatalf("firstImage: %v", err)
	}
	if img == nil {
		t.Fatal("no image returned")
	}
}

// TestTheWorkingFormatIsUsedWithoutReadingTheOther: the fallback must not turn
// every paste into two clipboard reads. Taking the first that decodes means a
// V5 screenshot is read once, and only the formats above the one that worked are
// attempted.
func TestTheWorkingFormatIsUsedWithoutReadingTheOther(t *testing.T) {
	tried := []uintptr{}
	if _, err := firstImage(
		func(uintptr) bool { return true },
		func(f uintptr) (image.Image, error) {
			tried = append(tried, f)
			return dibOK()
		}); err != nil {
		t.Fatalf("firstImage: %v", err)
	}
	if len(tried) != 1 || tried[0] != cfDIBV5 {
		t.Errorf("tried %v, want just CF_DIBV5", tried)
	}
}

// TestAFormatThatIsNotListedIsNotAttempted: asking for a format the clipboard does
// not hold costs a call and can only fail.
func TestAFormatThatIsNotListedIsNotAttempted(t *testing.T) {
	attempted := 0
	_, err := firstImage(
		func(f uintptr) bool { return f == cfDIB },
		func(uintptr) (image.Image, error) { attempted++; return dibOK() })
	if err != nil {
		t.Fatalf("firstImage: %v", err)
	}
	if attempted != 1 {
		t.Errorf("attempted %d formats, want 1", attempted)
	}
}

// TestNothingOnTheClipboardIsNotAFailure: the text-paste case, which has to fall
// through rather than report.
func TestNothingOnTheClipboardIsNotAFailure(t *testing.T) {
	_, err := firstImage(
		func(uintptr) bool { return false },
		func(uintptr) (image.Image, error) {
			t.Error("attempted a format that is not on the clipboard")
			return nil, nil
		})
	if !errors.Is(err, ErrNoImage) {
		t.Errorf("err = %v, want ErrNoImage", err)
	}
}

// TestEveryFormatFailingReportsTheFirstReason: the caller has to be told
// something, and the preferred format's reason is the one worth hearing. Wrapping
// rather than replacing is what keeps the distinction from a genuinely empty
// clipboard.
func TestEveryFormatFailingReportsTheFirstReason(t *testing.T) {
	first := errors.New("the clipboard offered CF_DIBV5 but would not hand it over")
	_, err := firstImage(
		func(uintptr) bool { return true },
		func(f uintptr) (image.Image, error) {
			if f == cfDIBV5 {
				return nil, first
			}
			return nil, errNotFound
		})
	if !errors.Is(err, first) {
		t.Errorf("err = %v, want the first format's reason", err)
	}
	if errors.Is(err, ErrNoImage) {
		t.Error("a failed read was reported as an empty clipboard")
	}
}
