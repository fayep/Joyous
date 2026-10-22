package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"

	"github.com/pierrec/lz4/v4"
)

// decodeIJI3Plane validates the IJI3 container (magic, dims, flags, CRC) and
// returns the uncompressed 2160x3060 ink-code plane.
func decodeIJI3Plane(t *testing.T, blob []byte) []byte {
	t.Helper()
	if len(blob) < 16 || string(blob[:4]) != "IJI3" {
		t.Fatalf("not IJI3 (len=%d)", len(blob))
	}
	w := binary.BigEndian.Uint16(blob[4:6])
	h := binary.BigEndian.Uint16(blob[6:8])
	rawSize := binary.LittleEndian.Uint32(blob[8:12])
	crc := binary.LittleEndian.Uint16(blob[12:14])
	flags := binary.LittleEndian.Uint16(blob[14:16])
	if int(w) != ij28W || int(h) != ij28H {
		t.Fatalf("dims %dx%d want %dx%d", w, h, ij28W, ij28H)
	}
	if flags != 0x0005 {
		t.Fatalf("flags %#x want 0x0005 (LZ4|8bpp)", flags)
	}
	raw := make([]byte, rawSize)
	n, err := lz4.UncompressBlock(blob[16:], raw)
	if err != nil {
		t.Fatalf("lz4 decompress: %v", err)
	}
	if n != int(rawSize) || n != ij28W*ij28H {
		t.Fatalf("decompressed %d bytes, want %d", n, ij28W*ij28H)
	}
	if got := crc16CCITT(raw); got != crc {
		t.Fatalf("CRC mismatch: got %#x want %#x", got, crc)
	}
	return raw
}

func countInk(plane []byte, code byte) int {
	n := 0
	for _, b := range plane {
		if b&0x07 == code {
			n++
		}
	}
	return n
}

// storeSynthInkJoy28 stores a ~1:√2 portrait gradient as an album image.
func storeSynthInkJoy28(t *testing.T, store *ImageStore) string {
	t.Helper()
	return storeSynthImage(t, store, 1080, 1527)
}

// storeSynthImage stores a w×h gradient as an album image.
func storeSynthImage(t *testing.T, store *ImageStore, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	id, err := store.Store(bytes.NewReader(buf.Bytes()), "synth.png")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return id
}

// TestServeInkJoy28IJI3OverlayCompositesAndStaysValid proves the 28.5" overlay
// serve path runs the mutate on the full-res upright panel image (flatRGB=false,
// 2160x3060) just before the dither, and that both the plain and overlaid
// outputs are well-formed IJI3 — i.e. the overlay composites without corrupting
// the container or changing its dimensions.
func TestServeInkJoy28IJI3OverlayCompositesAndStaysValid(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id := storeSynthInkJoy28(t, store)

	plain, err := store.ServeInkJoy28IJI3(id, true, nil)
	if err != nil {
		t.Fatalf("plain serve: %v", err)
	}
	plainPlane := decodeIJI3Plane(t, plain)

	called := false
	overlaid, err := store.ServeInkJoy28IJI3Overlay(id, true, func(img image.Image, flatRGB bool) (image.Image, error) {
		called = true
		if flatRGB {
			t.Error("flatRGB must be false on the 28.5\" photo path")
		}
		b := img.Bounds()
		if b.Dx() != ij28W || b.Dy() != ij28H {
			t.Fatalf("mutate received %dx%d, want full panel %dx%d", b.Dx(), b.Dy(), ij28W, ij28H)
		}
		out := image.NewRGBA(b)
		draw.Draw(out, b, img, b.Min, draw.Src)
		// Paint a solid black bar along the bottom edge, standing in for the
		// overlay region, so the overlaid plane is provably different.
		bar := image.Rect(0, ij28H-240, ij28W, ij28H)
		draw.Draw(out, bar, image.NewUniform(color.Black), image.Point{}, draw.Src)
		return out, nil
	}, nil)
	if err != nil {
		t.Fatalf("overlay serve: %v", err)
	}
	if !called {
		t.Fatal("mutate was never invoked — overlay not wired into the serve path")
	}
	overPlane := decodeIJI3Plane(t, overlaid)

	if bytes.Equal(plainPlane, overPlane) {
		t.Fatal("overlaid plane identical to plain — mutate had no effect on the encode")
	}
	// The black bar forces far more black ink (code 0x01) than the gradient alone.
	plainBlack, overBlack := countInk(plainPlane, 0x01), countInk(overPlane, 0x01)
	if overBlack <= plainBlack {
		t.Fatalf("expected more black ink after bottom bar: plain=%d overlaid=%d", plainBlack, overBlack)
	}
}
