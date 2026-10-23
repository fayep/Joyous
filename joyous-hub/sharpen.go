package main

import (
	"image"
	"math"
)

// Defaults chosen on the Samsung (23 Oct 2026): at 0.6 / 1.0 px the boost was
// lost in the dither; 0.9 / 1.4 px survives it without etching faces.
const (
	defaultSharpenAmount = 0.9
	defaultSharpenRadius = 1.4
	// sharpenThresholdL is the soft threshold in L* units.
	sharpenThresholdL = 1.5
)

// shouldSharpen gates the unsharp mask like the LAB passes: never for flat
// palette images (calibration swatches, ≤6-colour art), whose hard edges
// would only gain off-palette fringes.
func shouldSharpen(pipe ColorPipeline, img image.Image, flatRGB bool) bool {
	if !pipe.SharpenEnabled || pipe.SharpenAmount <= 0 || pipe.SharpenRadius <= 0 || flatRGB {
		return false
	}
	return UniqueColors(img) > 6
}

// sharpenL applies an unsharp mask to CIELAB lightness only, at the image's
// own (panel) resolution, so the dither gets crisper edges to place dots on.
// Chroma is left alone: sharpening colour would ring into stray red/blue dots
// with only six inks. sigma is the Gaussian blur radius in pixels; amount
// scales the edge boost; changes smaller than thresholdL (L* units) are
// ignored and larger ones reduced by it (a soft threshold), so smooth skin,
// sky and JPEG noise aren't turned into dither grit.
func sharpenL(img image.Image, amount, sigma, thresholdL float64) image.Image {
	if amount <= 0 || sigma <= 0 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	labs := newLabGrid(img)
	l := make([]float32, w*h)
	for i, p := range labs.lab {
		l[i] = float32(p[0])
	}
	blur := gaussianBlurPlane(l, w, h, sigma)
	return labMapImage(img, func(x, y int, lab *[3]float64) bool {
		i := (y-b.Min.Y)*w + (x - b.Min.X)
		d := float64(l[i] - blur[i])
		mag := math.Abs(d) - thresholdL
		if mag <= 0 {
			return false
		}
		lab[0] = math.Max(0, math.Min(100, lab[0]+amount*math.Copysign(mag, d)))
		return true
	})
}

// gaussianBlurPlane blurs a w×h plane with a separable Gaussian (edges clamped).
func gaussianBlurPlane(src []float32, w, h int, sigma float64) []float32 {
	r := int(math.Ceil(3 * sigma))
	k := make([]float32, 2*r+1)
	var sum float32
	for i := range k {
		x := float64(i - r)
		k[i] = float32(math.Exp(-x * x / (2 * sigma * sigma)))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	tmp := make([]float32, w*h)
	out := make([]float32, w*h)
	parallelRows(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			row := src[y*w : (y+1)*w]
			for x := range w {
				var acc float32
				for j, kv := range k {
					acc += kv * row[min(w-1, max(0, x+j-r))]
				}
				tmp[y*w+x] = acc
			}
		}
	})
	parallelRows(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := range w {
				var acc float32
				for j, kv := range k {
					acc += kv * tmp[min(h-1, max(0, y+j-r))*w+x]
				}
				out[y*w+x] = acc
			}
		}
	})
	return out
}
