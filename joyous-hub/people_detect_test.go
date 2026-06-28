package main

import (
	"image"
	"image/color"
	"testing"
)

func solidImage(w, h int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestDetectPeopleLikelySkinPatch(t *testing.T) {
	img := solidImage(400, 300, color.RGBA{210, 160, 130, 255})
	if !detectPeopleLikely(img) {
		t.Fatal("expected skin-toned image to register as people likely")
	}
}

func TestDetectPeopleLikelyLandscape(t *testing.T) {
	img := solidImage(400, 300, color.RGBA{70, 130, 190, 255})
	if detectPeopleLikely(img) {
		t.Fatal("expected blue sky image not to register as people likely")
	}
}

func TestDetectPeopleLikelySmallImage(t *testing.T) {
	img := solidImage(8, 8, color.RGBA{210, 160, 130, 255})
	if detectPeopleLikely(img) {
		t.Fatal("expected tiny image to skip detection")
	}
}
