package main

import (
	"image"
	"image/color"
	"testing"
)

func sharpenTestL(img image.Image, x, y int) float64 {
	r, g, b, _ := img.At(x, y).RGBA()
	return srgbToLAB([3]float64{float64(r>>8) / 255, float64(g>>8) / 255, float64(b>>8) / 255})[0]
}

// A soft vertical edge (grey ramp over a few pixels) between two flat fields,
// with a sub-threshold row-to-row variation so it has more than six colours.
func sharpenTestEdge(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := 80
			switch {
			case x >= w/2+2:
				v = 170
			case x >= w/2-2:
				v = 80 + (x-(w/2-2))*90/4
			}
			v += y % 3
			img.SetRGBA(x, y, color.RGBA{uint8(v), uint8(v), uint8(v), 255})
		}
	}
	return img
}

func TestSharpenLSteepensEdgesLeavesFlatAreas(t *testing.T) {
	src := sharpenTestEdge(64, 16)
	out := sharpenL(src, defaultSharpenAmount, defaultSharpenRadius, sharpenThresholdL)
	y := 8
	// Dark side just before the edge gets darker, light side just after gets lighter.
	if a, b := sharpenTestL(src, 30, y), sharpenTestL(out, 30, y); b >= a {
		t.Errorf("dark shoulder L %.2f -> %.2f, want darker", a, b)
	}
	if a, b := sharpenTestL(src, 34, y), sharpenTestL(out, 34, y); b <= a {
		t.Errorf("light shoulder L %.2f -> %.2f, want lighter", a, b)
	}
	// Far from the edge nothing changes.
	for _, x := range []int{2, 61} {
		if src.RGBAAt(x, y) != out.At(x, y).(color.RGBA) {
			t.Errorf("flat pixel x=%d changed: %v -> %v", x, src.RGBAAt(x, y), out.At(x, y))
		}
	}
}

func TestSharpenLIgnoresDetailUnderThreshold(t *testing.T) {
	// ±1 code value checkerboard: well under 1.5 L*, the kind of noise that
	// would otherwise become dither grit in skin and sky.
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			v := uint8(140 + (x+y)%2)
			img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	out := sharpenL(img, defaultSharpenAmount, defaultSharpenRadius, sharpenThresholdL)
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			if img.RGBAAt(x, y) != out.At(x, y).(color.RGBA) {
				t.Fatalf("sub-threshold pixel (%d,%d) changed", x, y)
			}
		}
	}
}

func TestShouldSharpen(t *testing.T) {
	pipe := standardColorPipeline()
	photo := sharpenTestEdge(64, 16)
	if !shouldSharpen(pipe, photo, false) {
		t.Fatal("default pipeline should sharpen a photo")
	}
	if shouldSharpen(pipe, photo, true) {
		t.Error("flatRGB (calibration) must not be sharpened")
	}
	flat := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range flat.Pix {
		flat.Pix[i] = 255
	}
	if shouldSharpen(pipe, flat, false) {
		t.Error("≤6-colour image must not be sharpened")
	}
	pipe.SharpenEnabled = false
	if shouldSharpen(pipe, photo, false) {
		t.Error("disabled setting must not sharpen")
	}
}

func TestSharpenLTapersHardEdges(t *testing.T) {
	// Black/white step: the untapered boost would be tens of L*; the taper
	// caps it at amount*knee.
	img := image.NewRGBA(image.Rect(0, 0, 32, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 32; x++ {
			v := uint8(40)
			if x >= 16 {
				v = 220
			}
			v += uint8(y % 3)
			img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	out := sharpenL(img, defaultSharpenAmount, defaultSharpenRadius, sharpenThresholdL)
	limit := defaultSharpenAmount * sharpenKneeL
	for _, x := range []int{15, 16} {
		a, b := sharpenTestL(img, x, 4), sharpenTestL(out, x, 4)
		if d := b - a; d == 0 || d > limit+0.5 || -d > limit+0.5 {
			t.Errorf("x=%d boost %.2f L*, want non-zero and within ±%.1f", x, d, limit)
		}
	}
}
