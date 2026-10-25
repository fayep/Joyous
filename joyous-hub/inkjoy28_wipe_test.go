package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// Luminance steps run black → 1 … white → 31 and are never 0.
func TestLuminanceSteps28(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 1))
	for x := range 256 {
		img.Set(x, 0, color.RGBA{uint8(x), uint8(x), uint8(x), 255})
	}
	steps := luminanceSteps28(img)[0]
	if steps[0] != 1 || steps[255] != 31 {
		t.Fatalf("black → %d, white → %d; want 1 and 31", steps[0], steps[255])
	}
	for x := 1; x < 256; x++ {
		if steps[x] < steps[x-1] || steps[x] < 1 || steps[x] > 31 {
			t.Fatalf("step at gray %d = %d (prev %d)", x, steps[x], steps[x-1])
		}
	}
}

// Steps land in the upper 5 bits of each panel byte; the ink stays in the low 3.
func TestPackInkJoy28PlaneSteps(t *testing.T) {
	idx, steps := make([][]byte, ij28H), make([][]byte, ij28H)
	for y := range idx {
		idx[y], steps[y] = make([]byte, ij28W), make([]byte, ij28W)
		for x := range idx[y] {
			idx[y][x] = byte((x + y) % 8)
			steps[y][x] = byte(1 + (x/50+y)%31)
		}
	}
	plane, err := packInkJoy28PlaneSteps(idx, steps)
	if err != nil {
		t.Fatal(err)
	}
	got := unpackInkJoy28PlaneBytes(plane)
	for _, p := range [][2]int{{0, 0}, {ij28W - 1, 1}, {777, 2049}, {5, ij28H - 1}} {
		x, y := p[0], p[1]
		if want := hiBytes8[idx[y][x]] | steps[y][x]<<3; got[y][x] != want {
			t.Fatalf("byte at %v = %#x, want %#x", p, got[y][x], want)
		}
	}
	plain, _ := packInkJoy28Plane(idx)
	for i, b := range plain {
		if b>>3 != 0 {
			t.Fatalf("off: byte %d has step %d", i, b>>3)
		}
	}
}

func writeWipe28PNG(t *testing.T, dataDir, name string, gray byte) {
	t.Helper()
	g := image.NewGray(image.Rect(0, 0, ij28W, ij28H))
	for i := range g.Pix {
		g.Pix[i] = gray
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, g); err != nil {
		t.Fatal(err)
	}
	dir := capturedWipe28Dir(dataDir)
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, name+".png"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The device's transition picks the steps for a send.
func TestInkJoy28WipeSelection(t *testing.T) {
	dataDir := t.TempDir()
	store := NewImageStore(dataDir)
	writeWipe28PNG(t, dataDir, "art", 0x58) // step 11
	writeWipe28PNG(t, dataDir, "zero", 0)   // clamps to step 1
	lum := [][]byte{{7}}

	for sel, want := range map[string]byte{"": 7, "luminance": 7, "captured:art": 11, "captured:zero": 1} {
		if got := store.inkjoy28Steps(lum, sel); got == nil || got[0][0] != want {
			t.Fatalf("%s: step %v, want %d", sel, got, want)
		}
	}
	if got := store.inkjoy28Steps(lum, "random"); got == nil || (got[0][0] != 11 && got[0][0] != 1) {
		t.Fatalf("random: %v", got)
	}
	if got := store.inkjoy28Steps(lum, "off"); got != nil {
		t.Fatal("off returned steps")
	}
	for _, bad := range []string{"captured:../x", "captured:", "sideways"} {
		if validateInkJoy28Wipe(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	choices := inkJoy28WipeChoices(dataDir)
	// The standard transition leads the device dropdown.
	if len(choices) != 5 || choices[0].ID != Wipe28Luminance || choices[3].ID != "captured:art" {
		t.Fatalf("choices %+v", choices)
	}
}
