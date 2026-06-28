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

func TestDetectPeopleLikelyDarkSkin(t *testing.T) {
	img := solidImage(400, 300, color.RGBA{120, 82, 62, 255})
	if !detectPeopleLikely(img) {
		t.Fatal("expected dark skin patch to register as people likely")
	}
}

func TestDetectPeopleLikelyBlueSky(t *testing.T) {
	img := solidImage(400, 300, color.RGBA{70, 130, 190, 255})
	if detectPeopleLikely(img) {
		t.Fatal("expected blue sky not to register as people likely")
	}
}

func TestDetectPeopleLikelyGreenLandscape(t *testing.T) {
	img := solidImage(400, 300, color.RGBA{55, 125, 48, 255})
	if detectPeopleLikely(img) {
		t.Fatal("expected green landscape not to register as people likely")
	}
}

func TestDetectPeopleLikelyYellowField(t *testing.T) {
	img := solidImage(400, 300, color.RGBA{210, 185, 55, 255})
	if detectPeopleLikely(img) {
		t.Fatal("expected yellow field not to register as people likely")
	}
}

func TestDetectPeopleLikelyGreyscape(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			v := uint8(40 + (x+y)%80)
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	if detectPeopleLikely(img) {
		t.Fatal("expected greyscale landscape not to register as people likely")
	}
}

func TestDetectPeopleLikelySmallImage(t *testing.T) {
	img := solidImage(8, 8, color.RGBA{210, 160, 130, 255})
	if detectPeopleLikely(img) {
		t.Fatal("expected tiny image to skip detection")
	}
}

func TestRgbCompatibleWithSkinRejectsYellow(t *testing.T) {
	if rgbCompatibleWithSkin(210, 185, 55) {
		t.Fatal("yellow field should not pass rgb skin filter")
	}
}
