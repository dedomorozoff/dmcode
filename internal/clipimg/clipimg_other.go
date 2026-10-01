//go:build !windows

package clipimg

import "image"

// read is the non-Windows answer: nothing.
//
// Linux and macOS would each need their own protocol — an image URI from the
// selection owner on X11 and Wayland, a file URL from the pasteboard on macOS —
// and neither is a guess worth making in a feature that already works by path.
// The honest report is that this platform cannot do it, which is what lets
// ctrl+v fall through to its text paste instead of looking broken.
func read() (image.Image, error) {
	return nil, ErrNoImage
}
