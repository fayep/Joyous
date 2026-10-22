package main

import (
	"image"
	"image/color"
	"math"
	"math/rand"
	"testing"
)

// labTestImage is a deterministic photo-like RGBA image: smooth gradients plus
// noise, covering darks (sRGB linear segment), saturated hues and highlights.
func labTestImage(w, h int) *image.RGBA {
	rng := rand.New(rand.NewSource(11))
	img := image.NewRGBA(image.Rect(3, 5, 3+w, 5+h)) // non-zero origin on purpose
	for y := range h {
		for x := range w {
			n := func(base float64) uint8 { return uint8(math.Max(0, math.Min(255, base+rng.NormFloat64()*18))) }
			img.SetRGBA(3+x, 5+y, color.RGBA{
				n(255 * float64(x) / float64(w)),
				n(255 * float64(y) / float64(h)),
				n(128 + 120*math.Sin(float64(x+y)/9)),
				uint8(200 + (x+y)%56),
			})
		}
	}
	return img
}

func TestSRGBToLABRowMatchesPerPixel(t *testing.T) {
	var src [][3]float64
	for v := range 256 { // every 8-bit grey, both sides of the 0.04045 knee
		g := float64(v) / 255
		src = append(src, [3]float64{g, g, g})
	}
	rng := rand.New(rand.NewSource(3))
	for range 5000 {
		src = append(src, [3]float64{rng.Float64(), rng.Float64(), rng.Float64()})
	}
	dst := make([][3]float64, len(src))
	var s labScratch
	srgbToLABRow(dst, src, &s)
	for i, rgb := range src {
		want := srgbToLAB(rgb)
		for c := range 3 {
			if math.Abs(dst[i][c]-want[c]) > 1e-9 {
				t.Fatalf("rgb %v: lab %v, want %v", rgb, dst[i], want)
			}
		}
	}
}

func TestLABToSRGBRowMatchesPerPixel(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	src := make([][3]float64, 5000)
	for i := range src { // includes out-of-gamut LAB, which must clamp the same way
		src[i] = [3]float64{rng.Float64() * 100, rng.Float64()*200 - 100, rng.Float64()*200 - 100}
	}
	dst := make([][3]float64, len(src))
	var s labScratch
	labToSRGBRow(dst, src, &s)
	for i, lab := range src {
		want := labToSRGB(lab)
		for c := range 3 {
			if math.Abs(dst[i][c]-want[c]) > 1e-9 {
				t.Fatalf("lab %v: rgb %v, want %v", lab, dst[i], want)
			}
		}
	}
}

// labMapImage must produce the same bytes as the per-pixel loop it replaced,
// keep alpha, and leave pixels fn reports unchanged byte-for-byte untouched.
func TestLabMapImageMatchesPerPixelLoop(t *testing.T) {
	img := labTestImage(173, 61)
	fn := func(x, _ int, lab *[3]float64) bool {
		if x%7 == 0 {
			return false
		}
		lab[0] = lab[0]*0.9 + 4
		lab[1] *= 1.2
		lab[2] -= 3
		return true
	}
	got := labMapImage(img, fn)
	b := img.Bounds()
	diff := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			src := img.RGBAAt(x, y)
			want := src
			lab := srgbToLAB([3]float64{float64(src.R) / 255, float64(src.G) / 255, float64(src.B) / 255})
			if fn(x, y, &lab) {
				rgb := labToSRGB(lab)
				want = color.RGBA{floatTo8(rgb[0]), floatTo8(rgb[1]), floatTo8(rgb[2]), src.A}
			}
			g := got.RGBAAt(x, y)
			if x%7 == 0 && g != src {
				t.Fatalf("(%d,%d) unchanged pixel rewritten: %v, want %v", x, y, g, src)
			}
			if g != want {
				diff++
			}
		}
	}
	if diff != 0 {
		t.Errorf("%d of %d pixels differ from the per-pixel loop", diff, b.Dx()*b.Dy())
	}
}

func TestLabGridMatchesPixelLABAt(t *testing.T) {
	img := labTestImage(40, 30)
	g := newLabGrid(img)
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			got, want := g.at(x, y), pixelLABAt(img, x, y)
			for c := range 3 {
				if math.Abs(got[c]-want[c]) > 1e-9 {
					t.Fatalf("(%d,%d): %v, want %v", x, y, got, want)
				}
			}
		}
	}
}
