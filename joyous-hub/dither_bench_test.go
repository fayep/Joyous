package main

import (
	"image"
	"testing"
)

func benchImg(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride : (y+1)*img.Stride]
		for x := 0; x < w; x++ {
			i := x * 4
			row[i] = uint8((x * 255) / w)
			row[i+1] = uint8((y * 255) / h)
			row[i+2] = uint8(200 - (x*255)/w/2)
			row[i+3] = 255
		}
	}
	return img
}

func BenchmarkStuckiInkJoyFull(b *testing.B) {
	img := benchImg(1600, 1200)
	pipe := ColorPipeline{}
	opts := stuckiOptionsInkJoy(pipe)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = StuckiTwoPalette(img, PaletteInkJoyDisplay, pipe, false, opts)
	}
}

func BenchmarkStuckiInkJoyDitherOnly(b *testing.B) {
	img := benchImg(1600, 1200)
	opts := stuckiOptions{Serpentine: true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = StuckiDither(img, PaletteInkJoyDisplay, opts)
	}
}

func BenchmarkStuckiInkJoyDitherEdge(b *testing.B) {
	img := benchImg(1600, 1200)
	opts := stuckiOptions{Serpentine: true, EdgePreserve: stuckiEdgePreserve}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = StuckiDither(img, PaletteInkJoyDisplay, opts)
	}
}

func BenchmarkPreDitherNoise(b *testing.B) {
	img := benchImg(1600, 1200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = applyPreDitherNoise(img, stuckiPreDitherNoise)
	}
}

func BenchmarkStuckiSamsungFull(b *testing.B) {
	img := benchImg(2560, 1440)
	pipe := ColorPipeline{}
	opts := stuckiOptionsSamsung(pipe)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = StuckiTwoPalette(img, PaletteSamsungDisplay, pipe, false, opts)
	}
}
