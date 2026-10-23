package main

import (
	"bytes"
	"image/jpeg"
	"strings"
	"testing"
)

func testIJI3(t *testing.T) ([]byte, [][]byte) {
	t.Helper()
	idx := make([][]byte, ij28H)
	for y := range idx {
		idx[y] = make([]byte, ij28W)
		for x := range idx[y] {
			idx[y][x] = byte((x/200 + y/300) % len(hiBytes8))
		}
	}
	plane, err := packInkJoy28Plane(idx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := writeInkJoy28IJI3(plane)
	if err != nil {
		t.Fatal(err)
	}
	return b, idx
}

func TestParseIJI3RoundTrip(t *testing.T) {
	b, _ := testIJI3(t)
	w, h, flags, raw, err := parseIJI3(b)
	if err != nil || w != ij28W || h != ij28H || flags != 0x0005 || len(raw) != ij28W*ij28H {
		t.Fatalf("parse: %dx%d flags %#x raw %d err %v", w, h, flags, len(raw), err)
	}
	bad := bytes.Clone(b)
	bad[12] ^= 0xff // CRC
	if _, _, _, _, err := parseIJI3(bad); err == nil {
		t.Fatal("corrupt CRC accepted")
	}
	if _, _, _, _, err := parseIJI3(b[:len(b)-100]); err == nil {
		t.Fatal("truncated body accepted")
	}
}

// A relayed cloud play may be the 10"'s raw .bin or any intact IJI3.
func TestValidatePlayBin(t *testing.T) {
	b, _ := testIJI3(t)
	for name, c := range map[string]struct {
		bin []byte
		ok  bool
	}{
		"28.5 IJI3":   {b, true},
		"10 raw":      {make([]byte, frameW*frameH*2), true},
		"short raw":   {make([]byte, 1705417), false},
		"broken IJI3": {b[:len(b)/2], false},
		"empty":       {nil, false},
	} {
		if err := validatePlayBin(c.bin); (err == nil) != c.ok {
			t.Errorf("%s: err %v, want ok=%v", name, err, c.ok)
		}
	}
}

func TestIJI3PreviewOrientation(t *testing.T) {
	b, _ := testIJI3(t)
	for _, portrait := range []bool{true, false} {
		j, err := iji3PreviewJPEG(b, portrait)
		if err != nil {
			t.Fatal(err)
		}
		img, err := jpeg.Decode(bytes.NewReader(j))
		if err != nil {
			t.Fatal(err)
		}
		w, h := img.Bounds().Dx(), img.Bounds().Dy()
		if portrait != (h > w) {
			t.Fatalf("portrait=%v preview %dx%d", portrait, w, h)
		}
	}
}

// The 28.5" transition step (upper 5 bits) and the 10" lo byte come out as
// wipe images in the embedded wipes' gray = step*8 format, upright.
func TestPlayBinWipe(t *testing.T) {
	// 28.5": upright grid of whole bytes (step<<3 | code), scanned as the panel reads it.
	step := func(x, y int) byte { return byte(1 + (x/100+y/150)%31) }
	plane := make([]byte, ij28W*ij28H)
	for i := 0; i < ij28H/2; i++ {
		for c := 0; c < ij28W; c++ {
			x := ij28W - 1 - c
			plane[(i*ij28W+c)*2] = step(x, 2*i)<<3 | 2
			plane[(i*ij28W+c)*2+1] = step(x, 2*i+1)<<3 | 1
		}
	}
	b, err := writeInkJoy28IJI3(plane)
	if err != nil {
		t.Fatal(err)
	}
	g, err := playBinWipe(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]int{{0, 0}, {ij28W - 1, 0}, {5, ij28H - 1}, {1234, 2345}} {
		if got, want := g.GrayAt(p[0], p[1]).Y, step(p[0], p[1])*8; got != want {
			t.Fatalf("28.5 wipe at %v = %d, want %d", p, got, want)
		}
	}

	// 10": lo bytes via applyLoToBin's layout.
	lo := make([][]byte, frameH)
	for y := range lo {
		lo[y] = make([]byte, frameW)
		for x := range lo[y] {
			lo[y][x] = byte((x + y) % 31 * 8)
		}
	}
	bin := make([]byte, frameW*frameH*2)
	applyLoToBin(bin, lo)
	g, err = playBinWipe(bin)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]int{{0, 0}, {frameW - 1, 0}, {3, frameH - 1}} {
		if got := g.GrayAt(p[0], p[1]).Y; got != lo[p[1]][p[0]] {
			t.Fatalf("10 wipe at %v = %d, want %d", p, got, lo[p[1]][p[0]])
		}
	}

	c := NewInkJoyCache(t.TempDir())
	path, err := c.saveCapturedWipe("art", b)
	if err != nil || !strings.HasSuffix(path, "wipes-captured/2160x3060/art.png") {
		t.Fatalf("capture path %q err %v", path, err)
	}
	if again, err := c.saveCapturedWipe("art", b); err != nil || again != "" {
		t.Fatalf("second capture %q %v, want no rewrite", again, err)
	}
}
