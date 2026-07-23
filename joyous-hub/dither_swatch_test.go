package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteDitherSwatches(t *testing.T) {
	if os.Getenv("WRITE_DITHER_SWATCHES") == "" {
		t.Skip("set WRITE_DITHER_SWATCHES=1 to regenerate testdata/dither_swatches.png")
	}
	const cellW, cellH = 200, 56
	swatches := defaultDitherSwatches(cellW, cellH)
	algos := competingDitherAlgos()
	sheet := RenderDitherSwatchSheet(swatches, algos, PaletteInkJoyDisplay)
	dir := "testdata"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "dither_swatches.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, sheet); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%dx%d)", path, sheet.Bounds().Dx(), sheet.Bounds().Dy())
	for _, a := range algos {
		t.Logf("score %-18s %.3f (lower better on peach)", a.Name, peachSwatchScore(a, PaletteInkJoyDisplay))
	}
}

func TestDitherSwatchSheetShape(t *testing.T) {
	swatches := defaultDitherSwatches(64, 24)
	algos := competingDitherAlgos()[:3]
	sheet := RenderDitherSwatchSheet(swatches, algos, PaletteInkJoyDisplay)
	wantW := 8*2 + 4*64 + 3*4
	wantH := 8*2 + len(swatches)*(24+16) + (len(swatches)-1)*4
	if sheet.Bounds().Dx() != wantW || sheet.Bounds().Dy() != wantH {
		t.Fatalf("sheet %dx%d, want %dx%d", sheet.Bounds().Dx(), sheet.Bounds().Dy(), wantW, wantH)
	}
}

func TestFloydAndAtkinsonSmoke(t *testing.T) {
	img := makeLinearGradient(80, 24, color.RGBA{R: 20, G: 20, B: 20, A: 255}, color.RGBA{R: 240, G: 240, B: 240, A: 255})
	for name, fn := range map[string]func(image.Image, [6][3]float64) [][]byte{
		"floyd":    FloydSteinbergDither,
		"atkinson": AtkinsonDither,
	} {
		idx := fn(img, PaletteInkJoyDisplay)
		seen := map[byte]bool{}
		for y := range idx {
			for x := range idx[y] {
				seen[idx[y][x]] = true
			}
		}
		if len(seen) < 2 {
			t.Fatalf("%s: expected multi-ink gradient, got %v", name, seen)
		}
	}
}

func TestPeachSwatchScoreOrdering(t *testing.T) {
	// Classic nearest+Stucki should not explode; score is finite.
	a := stuckiAlgo("x", stuckiOptions{Serpentine: true})
	s := peachSwatchScore(a, PaletteInkJoyDisplay)
	if s < 0 || s > 3 {
		t.Fatalf("unexpected score %v", s)
	}
}
