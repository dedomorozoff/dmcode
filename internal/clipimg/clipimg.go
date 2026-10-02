// Package clipimg reads an image out of the system clipboard.
//
// It is its own package because the answer is entirely platform-specific: on
// Windows the clipboard is an OLE object store and a screenshot arrives as a DIB,
// on macOS as a file URL, on Linux as an image URI behind whichever tool owns the
// selection. Nothing about that belongs in the TUI, and nothing about dmcode
// belongs here.
package clipimg

import (
	"errors"
	"image"
)

// ErrNoImage says the clipboard holds no picture. It is not a failure of the
// clipboard itself: the selection may well hold text, which is the common case,
// and the caller is expected to carry on with that.
var ErrNoImage = errors.New("the clipboard holds no image")

// Read returns the clipboard's picture, decoded.
//
// A returned error other than ErrNoImage means the clipboard said it had an
// image and the bytes could not be turned into one, which is worth reporting:
// the user pasted something and dmcode is about to ignore it.
func Read() (image.Image, error) {
	return read()
}
