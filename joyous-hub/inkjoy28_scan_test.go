package main

import (
	"bytes"
	"math/rand"
	"testing"
)

// inkjoy28LegacyZipPlane is the 28.5" scan as it was first solved empirically,
// in 2160-byte rows: flip X, then per source row pair (2i, 2i+1) column-zip the
// left half into byte row 2i and the right half into byte row 2i+1. Kept only as
// an independent reference — packInkJoy28Plane must produce the same bytes.
func inkjoy28LegacyZipPlane(indices [][]byte) []byte {
	const half = ij28W / 2
	buf := make([]byte, ij28W*ij28H)
	pix := func(r, c int) byte { return hiBytes8[indices[r][ij28W-1-c]] }
	for i := 0; i < ij28H/2; i++ {
		b0, b1 := (2*i)*ij28W, (2*i+1)*ij28W
		for c := 0; c < half; c++ {
			buf[b0+2*c] = pix(2*i, c)
			buf[b0+2*c+1] = pix(2*i+1, c)
			buf[b1+2*c] = pix(2*i, half+c)
			buf[b1+2*c+1] = pix(2*i+1, half+c)
		}
	}
	return buf
}

func randomInkJoy28Grid(seed int64) [][]byte {
	rng := rand.New(rand.NewSource(seed))
	g := make([][]byte, ij28H)
	for y := range g {
		g[y] = make([]byte, ij28W)
		for x := range g[y] {
			g[y][x] = byte(rng.Intn(len(hiBytes8)))
		}
	}
	return g
}

// The u16 big-endian description must be byte-identical to the layout the panel
// was verified against.
func TestPackInkJoy28PlaneMatchesLegacyZip(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		g := randomInkJoy28Grid(seed)
		got, err := packInkJoy28Plane(g)
		if err != nil {
			t.Fatal(err)
		}
		want := inkjoy28LegacyZipPlane(g)
		if !bytes.Equal(got, want) {
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("seed %d: first mismatch at byte %d (got %#x want %#x)", seed, i, got[i], want[i])
				}
			}
		}
	}
}

// Pin the corners: upper row of a pair in the MSB, X flipped, rows of word
// pairs running top to bottom.
func TestPackInkJoy28PlaneCorners(t *testing.T) {
	g := make([][]byte, ij28H)
	for y := range g {
		g[y] = make([]byte, ij28W)
	}
	// Distinct inks at the four corners and the pixel under each top corner.
	g[0][ij28W-1], g[1][ij28W-1] = 2, 3 // top-right pair → word 0
	g[0][0], g[1][0] = 4, 5             // top-left pair → word 2159
	g[ij28H-2][0], g[ij28H-1][0] = 6, 7 // bottom-left pair → last word
	plane, err := packInkJoy28Plane(g)
	if err != nil {
		t.Fatal(err)
	}
	word := func(n int) (hi, lo byte) { return plane[2*n], plane[2*n+1] }
	cases := []struct {
		name   string
		n      int
		hi, lo byte
	}{
		{"top-right", 0, hiBytes8[2], hiBytes8[3]},
		{"top-left", ij28W - 1, hiBytes8[4], hiBytes8[5]},
		{"bottom-left", ij28W*ij28H/2 - 1, hiBytes8[6], hiBytes8[7]},
	}
	for _, c := range cases {
		if hi, lo := word(c.n); hi != c.hi || lo != c.lo {
			t.Errorf("%s word %d = %#02x%02x, want %#02x%02x", c.name, c.n, hi, lo, c.hi, c.lo)
		}
	}
}

func TestPackInkJoy28PlaneRoundTrip(t *testing.T) {
	g := randomInkJoy28Grid(42)
	plane, err := packInkJoy28Plane(g)
	if err != nil {
		t.Fatal(err)
	}
	back := inkjoy28UnscanIndices(plane)
	for y := range g {
		for x := range g[y] {
			if want := hiBytes8[g[y][x]]; back[y][x] != want {
				t.Fatalf("(%d,%d) = %#x, want ink code %#x", x, y, back[y][x], want)
			}
		}
	}
}
