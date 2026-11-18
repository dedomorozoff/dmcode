//go:build !linux && !darwin && !windows

package clipimg

import "image"

// read is the answer for the platforms dmcode cannot ask: a BSD offers an image
// selection through an X11 or Wayland protocol this side does not speak, and no
// tool on it hands over a PNG this could trust. The honest report is "no
// picture", which is what lets ctrl+v fall through to its text paste instead of
// looking broken.
//
// The tag names every platform that has no other answer, so it must stay the
// complement of clipimg_cmd.go's `linux || darwin` and clipimg_windows.go's
// `windows`. It is a separate file rather than a clause in either because a
// build tag can only choose whole files, and the release matrix cross-builds
// all three BSDs.
func read() (image.Image, error) {
	return nil, ErrNoImage
}
