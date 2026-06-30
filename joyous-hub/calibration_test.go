package main

import (
	"bytes"
	"image"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCalibrationPNGEmbedded(t *testing.T) {
	if _, err := inkjoyCalibrationPNG(); err != nil {
		t.Fatal(err)
	}
	if _, err := inkjoyWhitePNG(); err != nil {
		t.Fatal(err)
	}
	if _, err := inkjoyGreenPNG(); err != nil {
		t.Fatal(err)
	}
	if _, err := samsungCalibrationPNG(); err != nil {
		t.Fatal(err)
	}
}

func TestInkJoyWhitePNG(t *testing.T) {
	data, err := inkjoyWhitePNG()
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != frameW || b.Dy() != frameH {
		t.Fatalf("size %dx%d", b.Dx(), b.Dy())
	}
	colors := make(map[[3]uint8]struct{})
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bv, _ := img.At(x, y).RGBA()
			colors[[3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(bv >> 8)}] = struct{}{}
		}
	}
	if len(colors) != 1 {
		t.Fatalf("expected 1 color, got %d", len(colors))
	}
	if _, ok := colors[[3]uint8{255, 255, 255}]; !ok {
		t.Fatalf("expected P1 white, got %v", colors)
	}
}

func TestInkJoyGreenPNG(t *testing.T) {
	data, err := inkjoyGreenPNG()
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != frameW || b.Dy() != frameH {
		t.Fatalf("size %dx%d", b.Dx(), b.Dy())
	}
	colors := make(map[[3]uint8]struct{})
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bv, _ := img.At(x, y).RGBA()
			colors[[3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(bv >> 8)}] = struct{}{}
		}
	}
	if len(colors) != 1 {
		t.Fatalf("expected 1 color, got %d", len(colors))
	}
	if _, ok := colors[[3]uint8{0, 255, 0}]; !ok {
		t.Fatalf("expected P1 green, got %v", colors)
	}
}

func TestEnsureInkJoyGreenUniformID(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id1, err := store.ensureInkJoyGreenUniformID()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := store.ensureInkJoyGreenUniformID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("expected same id, got %s and %s", id1, id2)
	}
	bin, err := store.ServeBin(id1)
	if err != nil {
		t.Fatal(err)
	}
	_, lo := FromBin(bin, frameW, frameH)
	for y := range frameH {
		for x := range frameW {
			if lo[y][x] != 248 {
				t.Fatalf("lo at %d,%d = %d, want 248", x, y, lo[y][x])
			}
		}
	}
}

func TestEnsureInkJoyGreenPetalsID(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id1, err := store.ensureInkJoyGreenPetalsID()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := store.ensureInkJoyGreenPetalsID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("expected same id, got %s and %s", id1, id2)
	}
	bin, err := store.ServeBin(id1)
	if err != nil {
		t.Fatal(err)
	}
	_, lo := FromBin(bin, frameW, frameH)
	if wipeFingerprint(lo) != wipeFingerprint(calibrationGreenPetalsWipeGrid()) {
		t.Fatal("green petals calibration bin should use petals wipe")
	}
}

func TestEnsureInkJoyGreenID(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id1, err := store.ensureInkJoyGreenID()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := store.ensureInkJoyGreenID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("expected same id, got %s and %s", id1, id2)
	}
	meta, err := store.readMeta(id1)
	if err != nil || !meta.FlatRGB {
		t.Fatalf("expected flat_rgb green meta: %+v err=%v", meta, err)
	}
	bin, err := store.ServeBin(id1)
	if err != nil {
		t.Fatal(err)
	}
	_, lo := FromBin(bin, frameW, frameH)
	if wipeFingerprint(lo) != wipeFingerprint(calibrationGreenWipeGrid()) {
		t.Fatal("green calibration bin should use blend wipe")
	}
}

func TestCalibrationPNGHandler(t *testing.T) {
	dir := t.TempDir()
	h := &Hub{images: NewImageStore(dir)}
	rec := httptest.NewRecorder()
	h.handleCalibrationPNG(rec, httptest.NewRequest(http.MethodGet, "/api/calibration/inkjoy", nil), "inkjoy")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type %q", ct)
	}
	if len(rec.Body.Bytes()) < 1000 {
		t.Fatal("png too small")
	}
}

func TestEnsureInkJoyCalibrationID(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id1, err := store.ensureInkJoyCalibrationID()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := store.ensureInkJoyCalibrationID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("expected same id, got %s and %s", id1, id2)
	}
	meta, err := store.readMeta(id1)
	if err != nil || !meta.FlatRGB {
		t.Fatalf("expected flat_rgb calibration meta: %+v err=%v", meta, err)
	}
	bin, err := store.ServeBin(id1)
	if err != nil {
		t.Fatal(err)
	}
	hi, _ := FromBin(bin, frameW, frameH)
	greenCount := 0
	for y := range frameH {
		for x := range frameW {
			if hi[y][x] != inkJoyGreenHi {
				continue
			}
			greenCount++
		}
	}
	if greenCount == 0 {
		t.Fatal("primaries chart should include green hi pixels")
	}
}

func TestEnsureInkJoyWhiteID(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id1, err := store.ensureInkJoyWhiteID()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := store.ensureInkJoyWhiteID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("expected same id, got %s and %s", id1, id2)
	}
	meta, err := store.readMeta(id1)
	if err != nil || !meta.FlatRGB {
		t.Fatalf("expected flat_rgb white meta: %+v err=%v", meta, err)
	}
	bin, err := store.ServeBin(id1)
	if err != nil {
		t.Fatal(err)
	}
	_, lo := FromBin(bin, frameW, frameH)
	if wipeFingerprint(lo) != wipeFingerprint(calibrationWhiteWipeGrid()) {
		t.Fatal("white calibration bin should use blend wipe")
	}
}

func TestBlackUniform248Bin(t *testing.T) {
	bin := BuildBlackUniform248Bin(1600, 1200)
	hi, lo := FromBin(bin, 1600, 1200)
	if hi[600][800] != 0x01 || lo[600][800] != 248 {
		t.Fatalf("got hi=0x%02x lo=%d", hi[600][800], lo[600][800])
	}
}

func TestInkJoyLoLadderPrimariesBin(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id, err := store.ensureInkJoyLoLadderPrimariesID()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.readMeta(id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.FlatRGB {
		t.Fatal("lo ladder should not use flat RGB snap")
	}
	bin, err := store.ServeBin(id)
	if err != nil {
		t.Fatal(err)
	}
	hi, lo := FromBin(bin, frameW, frameH)
	if hi[0][0] != 0x01 || lo[0][0] != 248 {
		t.Fatalf("surround: hi=0x%02x lo=%d", hi[0][0], lo[0][0])
	}
	mx, my := 48, 48
	cellW := (1600 - 2*mx) / 8
	if lo[my][mx] != 0 || hi[my][mx+cellW/6+1] != 0x02 {
		t.Fatalf("cell pattern: lo=%d hi white=0x%02x", lo[my][mx], hi[my][mx+cellW/6+1])
	}
	bin2, err := store.ServeBin(id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bin, bin2) {
		t.Fatal("lo ladder bin changed on re-serve")
	}
}
