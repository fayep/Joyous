package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestEmbeddedWipesLoad(t *testing.T) {
	loadEmbeddedWipes()
	if loadWipesErr != nil {
		t.Fatalf("loadEmbeddedWipes: %v", loadWipesErr)
	}
	if len(embeddedWipes) < 2 {
		t.Fatalf("expected multiple embedded wipes, got %d", len(embeddedWipes))
	}
	for i, lo := range embeddedWipes {
		if len(lo) != frameH || len(lo[0]) != frameW {
			t.Errorf("wipe %s: shape %dx%d, want %dx%d",
				embeddedWipeNames[i], len(lo[0]), len(lo), frameW, frameH)
		}
	}
}

func TestApplyLoToBinRoundtrip(t *testing.T) {
	loadEmbeddedWipes()
	if len(embeddedWipes) == 0 {
		t.Fatal("no embedded wipes loaded")
	}
	hi, _ := FromBin(make([]byte, frameW*frameH*2), frameW, frameH)
	lo := randomWipeGrid()
	bin := ToBin(hi, lo)
	applyLoToBin(bin, embeddedWipes[0])
	_, loOut := FromBin(bin, frameW, frameH)
	for y := range frameH {
		for x := range frameW {
			if loOut[y][x] != embeddedWipes[0][y][x] {
				t.Fatalf("lo mismatch at (%d,%d): got %d want %d", x, y, loOut[y][x], embeddedWipes[0][y][x])
			}
		}
	}
}

func TestServeBinRandomizesWipeForPNG(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PNG conversion test in short mode")
	}
	dir := t.TempDir()
	store := NewImageStore(dir)

	img := image.NewRGBA(image.Rect(0, 0, frameW, frameH))
	palColors := []color.RGBA{
		{30, 30, 30, 255}, {149, 162, 165, 255}, {166, 165, 17, 255},
		{121, 23, 17, 255}, {0, 76, 136, 255}, {46, 91, 65, 255},
	}
	for y := range frameH {
		for x := range frameW {
			img.Set(x, y, palColors[(x+y)%6])
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	id, err := store.Store(bytes.NewReader(buf.Bytes()), "photo.png")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	seen := map[string]struct{}{}
	for range 12 {
		bin, err := store.ServeBin(id)
		if err != nil {
			t.Fatalf("ServeBin: %v", err)
		}
		_, lo := FromBin(bin, frameW, frameH)
		seen[wipeFingerprint(lo)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Errorf("expected varied wipe patterns across serves, got %d unique", len(seen))
	}
}

func TestServeBinPreservesStoredBinWipe(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)
	bin := make([]byte, frameW*frameH*2)
	for i := 0; i < len(bin); i += 2 {
		bin[i] = 0x02
		bin[i+1] = 0x40
	}
	id, err := store.Store(bytes.NewReader(bin), "photo.bin")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, err := store.ServeBin(id)
	if err != nil {
		t.Fatalf("ServeBin: %v", err)
	}
	for i := 1; i < len(got); i += 2 {
		if got[i] != 0x40 {
			t.Fatalf("stored .bin lo byte mutated at offset %d: got 0x%02x", i, got[i])
		}
	}
}

func wipeFingerprint(lo [][]byte) string {
	var b bytes.Buffer
	for y := 0; y < frameH; y += 120 {
		for x := 0; x < frameW; x += 160 {
			b.WriteByte(lo[y][x])
		}
	}
	return b.String()
}
