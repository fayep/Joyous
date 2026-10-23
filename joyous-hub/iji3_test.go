package main

import (
	"bytes"
	"image/jpeg"
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
