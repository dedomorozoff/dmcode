//go:build windows

package clipimg

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The clipboard formats a picture can arrive in.
//
// CF_DIBV5 is preferred over CF_DIB because it states its own colour space and
// alpha channel, whereas a CF_DIB header leaves the alpha byte undefined вЂ” read
// as written it produces either a black fringe or a transparent one depending on
// which machine put it there. V5 exists to remove that ambiguity, and every
// Windows dmcode builds for has had it since Windows 7.
const (
	cfDIB   = 8
	cfDIBV5 = 17

	// gmemMoveable is the allocation flag the clipboard requires of the handles
	// it is given. Getting it wrong is accepted by SetClipboardData and then
	// corrupts whatever reads the handle next.
	gmemMoveable = 0x0002
)

var (
	user32                     = windows.NewLazySystemDLL("user32.dll")
	kernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	procIsClipboardFormatAvail = user32.NewProc("IsClipboardFormatAvailable")
	procOpenClipboard          = user32.NewProc("OpenClipboard")
	procCloseClipboard         = user32.NewProc("CloseClipboard")
	procEmptyClipboard         = user32.NewProc("EmptyClipboard")
	procGetClipboardData       = user32.NewProc("GetClipboardData")
	procSetClipboardData       = user32.NewProc("SetClipboardData")
	procGlobalAlloc            = kernel32.NewProc("GlobalAlloc")
	procGlobalLock             = kernel32.NewProc("GlobalLock")
	procGlobalUnlock           = kernel32.NewProc("GlobalUnlock")
	procGlobalSize             = kernel32.NewProc("GlobalSize")
)

// The formats a picture can be read as, in the order they are tried. V5 is
// preferred because it states its own colour space and alpha channel, whereas a
// CF_DIB header leaves the alpha byte undefined — read as written it produces
// either a black fringe or a transparent one depending on which machine put it
// there. V5 exists to remove that ambiguity, and every Windows dmcode builds for
// has had it since Windows 7.
var dibFormats = []uintptr{cfDIBV5, cfDIB}

// read pulls a DIB off the clipboard and decodes it.
//
// The whole thing runs on a locked OS thread. The clipboard is a process-wide
// resource guarded by OpenClipboard, and a goroutine migrating between the open
// and the close is the classic way to deadlock against another process that got
// in between: this thread would hold a lock it can never release, because the
// release would run on the new thread.
func read() (image.Image, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := openClipboard(); err != nil {
		return nil, err
	}
	defer procCloseClipboard.Call()

	available := func(f uintptr) bool {
		got, _, _ := procIsClipboardFormatAvail.Call(f)
		return got != 0
	}
	return firstImage(available, readFormat)
}

// firstImage returns the first format that yields a decodable picture.
//
// It exists because IsClipboardFormatAvailable only answers whether a format is
// *listed*, which is not the same as whether it can be read. The clipboard
// synthesises formats from the ones it holds, and an application that publishes a
// format in its list but cannot render it leaves GetClipboardData returning
// nothing and ERROR_NOT_FOUND. Committing to the first advertised format and
// reporting that as the failure is what made a pasted screenshot come back as
// "Element not found" on a machine where CF_DIBV5 was advertised and CF_DIB was
// perfectly readable.
//
// Taking the first format that *decodes* is also the only thing that can tell the
// two apart: a handle that is not a DIB is not distinguishable from a missing one
// until its header has been read.
//
// available and attempt are passed in rather than called directly so the policy
// can be tested without a clipboard, which is a global resource no test may own.
func firstImage(available func(uintptr) bool, attempt func(uintptr) (image.Image, error)) (image.Image, error) {
	var firstErr error
	for _, f := range dibFormats {
		if !available(f) {
			continue
		}
		img, err := attempt(f)
		if err == nil {
			return img, nil
		}
		// Remembered, not returned: a failure on the preferred format means
		// nothing while a later one may still work, and the error is only worth
		// reporting if every format failed.
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, ErrNoImage
}

// readFormat reads one clipboard format. The clipboard must already be open.
func readFormat(format uintptr) (image.Image, error) {
	name := "CF_DIB"
	if format == cfDIBV5 {
		name = "CF_DIBV5"
	}

	h, _, err := procGetClipboardData.Call(format)
	if h == 0 {
		if err == nil {
			err = ErrNoImage
		}
		return nil, fmt.Errorf("the clipboard offered %s but would not hand it over: %w", name, err)
	}
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		if err == nil {
			err = ErrNoImage
		}
		return nil, fmt.Errorf("the clipboard's %s could not be locked: %w", name, err)
	}
	defer procGlobalUnlock.Call(h)

	size, _, _ := procGlobalSize.Call(h)
	img, err := decodeDIB(p, uintptr(size))
	if err != nil {
		return nil, fmt.Errorf("the clipboard's %s is not a picture dmcode can read: %w", name, err)
	}
	return img, nil
}

// openClipboard opens the clipboard, retrying briefly.
//
// Another application holding it open is normal rather than exceptional вЂ” it is
// held for as long as the user is copying in Explorer вЂ” so a single attempt fails
// often enough to be unusable. Four short tries cover the ordinary case without
// making a genuinely busy clipboard feel like a hang.
func openClipboard() error {
	var last error
	for attempt := range 4 {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			return nil
		} else if last == nil {
			last = errors.New("another application is holding the clipboard")
		}
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	return fmt.Errorf("cannot open the clipboard: %w", last)
}

// dibHeader is the BITMAPINFOHEADER that opens every DIB, including a CF_DIBV5
// payload вЂ” V5 extends it, so the first 40 bytes mean the same thing and the
// fields below are the ones both agree on.
type dibHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// decodeDIB turns a locked clipboard handle into an image.
//
// Everything after the header is read through an offset from the base pointer,
// because the pixel array is sized by the header and no Go struct can describe
// something whose length is not known until it is read. Every read is bounds
// checked against the handle's real size: this is memory the clipboard owns and
// this code does not, and a short DIB is not hypothetical вЂ” an application that
// died mid-copy leaves one behind.
func decodeDIB(base, size uintptr) (image.Image, error) {
	if base == 0 || size < unsafe.Sizeof(dibHeader{}) {
		return nil, errors.New("the clipboard image is truncated")
	}
	var h dibHeader
	if err := copyMem(base, unsafe.Sizeof(h), (*byte)(unsafe.Pointer(&h))); err != nil {
		return nil, err
	}

	// A negative height means top-down rows; a positive one, the usual case, is
	// bottom-up.
	topDown := h.Height < 0
	height := int(h.Height)
	if height < 0 {
		height = -height
	}
	width, bits, compression := int(h.Width), int(h.BitCount), h.Compression
	if width < 1 || height < 1 || width > 1<<16 || height > 1<<16 {
		return nil, fmt.Errorf("the clipboard image has impossible dimensions (%dx%d)", width, height)
	}
	switch bits {
	case 24, 32:
	default:
		// 1, 4 and 8-bit DIBs are palettised. A screenshot never is one, and
		// supporting them means carrying a colour table through for a case that
		// does not arise вЂ” so the error says what is supported instead.
		return nil, fmt.Errorf("the clipboard image is %d-bit, which this build does not read (24 and 32 are)", bits)
	}
	const (
		biRGB       = 0
		biBitFields = 3
	)
	if compression != biRGB && compression != biBitFields {
		// A compressed DIB needs a codec that is not in the standard library.
		return nil, fmt.Errorf("the clipboard image is compressed (format %d), which this build does not read", compression)
	}

	// Where the pixels start: the header (V5's is longer, and its extra fields are
	// skipped rather than interpreted вЂ” the pixels are in the same place either
	// way, and the colour space it describes is not one this could apply), then a
	// palette if one was declared, then three channel masks in the BI_BITFIELDS
	// case.
	off := uintptr(h.Size)
	if off < unsafe.Sizeof(h) {
		off = unsafe.Sizeof(h)
	}
	off += uintptr(h.ClrUsed) * 4
	if compression == biBitFields {
		off += 12
	}

	// Rows are padded to a four-byte boundary, which is why this is not simply
	// width*bits/8.
	rowBytes := ((width*bits + 31) / 32) * 4
	total := uint64(rowBytes) * uint64(height)

	// The check is against off+total, not total. The header, the palette and the
	// masks all sit in front of the pixels and are part of the same allocation, so
	// comparing the pixel count to the handle's size lets a DIB that lies about
	// its dimensions by more than a header's worth of bytes through — and what it
	// then reads is memory the clipboard does not own.
	end := uint64(off) + total
	if end < total || end > uint64(size) {
		return nil, errors.New("the clipboard image claims more pixels than it holds")
	}

	buf := make([]byte, total)
	if err := copyMem(base+off, uintptr(total), &buf[0]); err != nil {
		return nil, err
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		src := y
		if !topDown {
			src = height - 1 - y
		}
		row := buf[src*rowBytes : src*rowBytes+rowBytes]
		for x := 0; x < width; x++ {
			p := x * bits / 8
			if p+3 > len(row) {
				break
			}
			// DIB pixels are BGR(A); image.RGBA is RGBA.
			a := byte(0xff)
			if bits == 32 {
				a = row[p+3]
			}
			img.SetRGBA(x, y, color.RGBA{R: row[p+2], G: row[p+1], B: row[p], A: a})
		}
	}
	return img, nil
}

// copyMem reads n bytes at addr into dst, bounds-checked against the handle's
// real size.
//
// It goes through ReadProcessMemory rather than dereferencing the pointer
// directly. A locked clipboard handle *is* ordinary memory in this process, so a
// direct copy would be the shorter code вЂ” but go vet refuses the
// uintptr-to-unsafe.Pointer conversion that requires, and the reason it refuses
// is the right one to honour: a pointer the garbage collector cannot see through
// is a pointer whose target may move. The kernel call takes the address as a
// uintptr, which is exactly what it is.
func copyMem(addr, n uintptr, dst *byte) error {
	if n == 0 {
		return nil
	}
	if err := windows.ReadProcessMemory(windows.CurrentProcess(), addr, dst, n, nil); err != nil {
		return fmt.Errorf("the clipboard image could not be read: %w", err)
	}
	return nil
}
