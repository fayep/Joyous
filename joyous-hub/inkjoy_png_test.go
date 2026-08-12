package main

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestEncodeIndexedPNG4RoundTrips(t *testing.T) {
	w, h := 8, 2
	// hiBytes = {0x01,0x02,0x03,0x04,0x06,0x07} for palette index 0-5.
	hi := make([][]byte, h)
	lo := make([][]byte, h)
	wantIdx := [][]int{
		{0, 1, 2, 3, 4, 5, 0, 1},
		{5, 4, 3, 2, 1, 0, 5, 4},
	}
	for y := 0; y < h; y++ {
		hi[y] = make([]byte, w)
		lo[y] = make([]byte, w)
		for x := 0; x < w; x++ {
			hi[y][x] = hiBytes[wantIdx[y][x]]
			lo[y][x] = 0xF8 // wipe order, dropped by the PNG encode -- irrelevant here
		}
	}
	bin := ToBin(hi, lo)

	palette := [6][3]byte{
		{10, 20, 30}, {200, 200, 200}, {230, 220, 20},
		{180, 30, 30}, {20, 60, 180}, {40, 130, 70},
	}
	pngBytes, err := EncodeIndexedPNG4(bin, w, h, palette)
	if err != nil {
		t.Fatalf("EncodeIndexedPNG4: %v", err)
	}

	// Go's png.Decoder does support reading arbitrary palette bit depths
	// (unlike its Encoder, which is why this file hand-rolls the writer --
	// see EncodeIndexedPNG4's comment), so this exercises a real, spec-
	// compliant 4-bit indexed PNG, not just our own encoder read back with
	// itself.
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	pal, ok := img.(*image.Paletted)
	if !ok {
		t.Fatalf("decoded image is %T, want *image.Paletted", img)
	}
	if len(pal.Palette) != 6 {
		t.Fatalf("palette len = %d, want 6", len(pal.Palette))
	}
	for i, want := range palette {
		r, g, b, _ := pal.Palette[i].RGBA()
		got := [3]byte{byte(r >> 8), byte(g >> 8), byte(b >> 8)}
		if got != want {
			t.Errorf("palette[%d] = %v, want %v", i, got, want)
		}
	}
	b := pal.Bounds()
	if b.Dx() != w || b.Dy() != h {
		t.Fatalf("dims = %dx%d, want %dx%d", b.Dx(), b.Dy(), w, h)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			got := int(pal.ColorIndexAt(x, y))
			if got != wantIdx[y][x] {
				t.Errorf("pixel(%d,%d) index = %d, want %d", x, y, got, wantIdx[y][x])
			}
		}
	}
}

func TestEncodeIndexedPNG4RejectsWrongSize(t *testing.T) {
	if _, err := EncodeIndexedPNG4([]byte{1, 2, 3}, 10, 10, [6][3]byte{}); err == nil {
		t.Fatal("expected error for mismatched bin size")
	}
}

func TestContentHashStableAndDistinct(t *testing.T) {
	a := ContentHash([]byte("hello"))
	b := ContentHash([]byte("hello"))
	c := ContentHash([]byte("world"))
	if a != b {
		t.Fatalf("hash not stable: %s != %s", a, b)
	}
	if a == c {
		t.Fatalf("hash collision for distinct input")
	}
	if len(a) != 16 { // 8 bytes hex-encoded
		t.Fatalf("hash len = %d, want 16", len(a))
	}
}
