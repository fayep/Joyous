package main

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestInkAffinityPullsSkyTowardBlue(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 151, G: 182, B: 240, A: 255})
	out := ApplyInkAffinity(img, PaletteSamsungDisplay, 1.0, false)
	r8, g8, b8, _ := out.At(0, 0).RGBA()
	before := srgbToLAB([3]float64{151.0 / 255, 182.0 / 255, 240.0 / 255})
	after := srgbToLAB([3]float64{float64(r8>>8) / 255, float64(g8>>8) / 255, float64(b8>>8) / 255})
	blueInk := srgbToLAB(paletteRGB01(PaletteSamsungDisplay[4]))
	if math.Abs(angleDiff(math.Atan2(after[2], after[1]), math.Atan2(blueInk[2], blueInk[1]))) >=
		math.Abs(angleDiff(math.Atan2(before[2], before[1]), math.Atan2(blueInk[2], blueInk[1]))) {
		t.Fatalf("expected sky hue angle closer to blue ink")
	}
	if mathAbs(after[0]-before[0]) > 4 {
		t.Fatalf("L* moved too much: before=%.2f after=%.2f", before[0], after[0])
	}
}

func TestInkAffinityPreservesNeutralGrey(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 128, G: 128, B: 128, A: 255})
	out := ApplyInkAffinity(img, PaletteSamsungDisplay, 1.0, false)
	r8, g8, b8, _ := out.At(0, 0).RGBA()
	dr := int(r8>>8) - 128
	dg := int(g8>>8) - 128
	db := int(b8>>8) - 128
	if dr < 0 {
		dr = -dr
	}
	if dg < 0 {
		dg = -dg
	}
	if db < 0 {
		db = -db
	}
	if dr > 8 || dg > 8 || db > 8 {
		t.Fatalf("grey shifted too far: rgb=%d,%d,%d", r8>>8, g8>>8, b8>>8)
	}
}

func TestInkAffinityReducedOnSkin(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 180, G: 120, B: 90, A: 255})
	full := ApplyInkAffinity(img, PaletteSamsungDisplay, 1.0, true)
	none := ApplyInkAffinity(img, PaletteSamsungDisplay, 1.0, false)
	fullLab := srgbToLAB(pixelRGB01(full.At(0, 0)))
	noneLab := srgbToLAB(pixelRGB01(none.At(0, 0)))
	srcLab := srgbToLAB(pixelRGB01(img.At(0, 0)))
	fullMove := labDeltaE76(srcLab, fullLab)
	noneMove := labDeltaE76(srcLab, noneLab)
	if fullMove >= noneMove {
		t.Fatalf("expected smaller move on skin with portrait enhance: full=%.2f none=%.2f", fullMove, noneMove)
	}
}

func pixelRGB01(c color.Color) [3]float64 {
	r8, g8, b8, _ := c.RGBA()
	return [3]float64{float64(r8>>8) / 255, float64(g8>>8) / 255, float64(b8>>8) / 255}
}

func mathAbs(x float64) float64 {
	return math.Abs(x)
}
