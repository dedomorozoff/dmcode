package imgprev

import (
	"image"
	"image/color"
	"testing"
)

func TestFitPreservesAspectAndCapsBothSides(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4000, 1000))
	got := Fit(img, 1280, 720)
	b := got.Bounds()
	if b.Dx() > 1280 || b.Dy() > 720 {
		t.Fatalf("Fit returned %dx%d, over the cap", b.Dx(), b.Dy())
	}
	if b.Dx() != 1280 || b.Dy() != 320 {
		t.Fatalf("aspect changed: %dx%d, want 1280x320", b.Dx(), b.Dy())
	}
}

func TestFitLeavesASmallPictureAlone(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 10))
	if got := Fit(img, 1280, 720); got != img {
		t.Fatal("Fit replaced an image that already fits")
	}
}

func TestSampleCoverKeepsTransparencyOutOfTheRGB(t *testing.T) {
	// Half fully transparent, half opaque red. The transparent half must not
	// darken the red side through premultiplied averaging.
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		img.SetNRGBA(0, y, color.NRGBA{0, 0, 0, 0})
		img.SetNRGBA(1, y, color.NRGBA{255, 0, 0, 255})
	}
	grid := SampleCover(img, 2, 1)
	if len(grid) != 2 {
		t.Fatalf("grid = %d cells, want 2", len(grid))
	}
	if grid[1].R < 200 {
		t.Fatalf("the opaque cell was darkened: %v", grid[1])
	}
}
