package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"testing"
)

// TestNativePNGRoundtrip: encode hi/lo → native PNG → decode → identical arrays.
func TestNativePNGRoundtrip(t *testing.T) {
	const W, H = 16, 12
	hi := make([]byte, W*H)
	lo := make([]byte, W*H)
	hiBytesSet := []byte{0x01, 0x02, 0x03, 0x04, 0x06, 0x07}
	for i := range hi {
		hi[i] = hiBytesSet[i%6]
		lo[i] = byte((i % 31) * 8)
	}
	hiArr := bytesToGrid(hi, H, W)
	loArr := bytesToGrid(lo, H, W)

	png := NativeEncode(hiArr, loArr)
	hiOut, loOut := NativeDecode(png)

	for y := range H {
		for x := range W {
			if hiOut[y][x] != hiArr[y][x] {
				t.Errorf("hi mismatch at (%d,%d): got %02x want %02x", y, x, hiOut[y][x], hiArr[y][x])
			}
			if loOut[y][x] != loArr[y][x] {
				t.Errorf("lo mismatch at (%d,%d): got %02x want %02x", y, x, loOut[y][x], loArr[y][x])
			}
		}
	}
}

// TestBinRoundtrip: hi/lo → ToBin() → FromBin() → identical arrays.
func TestBinRoundtrip(t *testing.T) {
	const W, H = 8, 6
	hiArr := make([][]byte, H)
	loArr := make([][]byte, H)
	for y := range H {
		hiArr[y] = make([]byte, W)
		loArr[y] = make([]byte, W)
		for x := range W {
			hiArr[y][x] = []byte{0x01, 0x02, 0x03, 0x04, 0x06, 0x07}[(y*W+x)%6]
			loArr[y][x] = byte(((y*W + x) % 31) * 8)
		}
	}
	bin := ToBin(hiArr, loArr)
	hiOut, loOut := FromBin(bin, W, H)
	for y := range H {
		for x := range W {
			if hiOut[y][x] != hiArr[y][x] || loOut[y][x] != loArr[y][x] {
				t.Errorf("mismatch at (%d,%d)", y, x)
			}
		}
	}
}

// TestBinSize: 1600×1200 → ToBin is exactly 1600*1200*2 bytes.
func TestBinSize(t *testing.T) {
	const W, H = 1600, 1200
	hi := makeGrid(H, W, 0x01)
	lo := makeGrid(H, W, 0x00)
	bin := ToBin(hi, lo)
	if len(bin) != W*H*2 {
		t.Errorf("ToBin size: got %d want %d", len(bin), W*H*2)
	}
}

// TestBinByteOrder: hi[0,0]=0x01, lo[0,0]=0 → last two bytes are [0x01, 0x00]
// (bin is bottom-to-top, so row 0 display = last row in file).
func TestBinByteOrder(t *testing.T) {
	const W, H = 4, 4
	hi := makeGrid(H, W, 0x02) // white everywhere
	lo := makeGrid(H, W, 0x10)
	hi[0][0] = 0x01 // black at top-left display pixel
	lo[0][0] = 0x00

	bin := ToBin(hi, lo)
	// Top-left display pixel → last row in bin → bytes at len-W*2 and len-W*2+1
	lastRowStart := len(bin) - W*2
	if bin[lastRowStart] != 0x01 || bin[lastRowStart+1] != 0x00 {
		t.Errorf("top-left pixel at bin[%d:%d]: got [%02x,%02x] want [0x01,0x00]",
			lastRowStart, lastRowStart+2, bin[lastRowStart], bin[lastRowStart+1])
	}
}

// TestClockWipeDimensions: MakeClockWipe returns correct shape and value set.
func TestClockWipeDimensions(t *testing.T) {
	lo := MakeClockWipe(1600, 1200)
	if len(lo) != 1200 {
		t.Fatalf("rows: got %d want 1200", len(lo))
	}
	if len(lo[0]) != 1600 {
		t.Fatalf("cols: got %d want 1600", len(lo[0]))
	}
	seen := map[byte]bool{}
	for _, row := range lo {
		for _, v := range row {
			if v%8 != 0 || v > 248 {
				t.Errorf("invalid wipe value %d (must be multiple of 8, ≤248)", v)
			}
			seen[v] = true
		}
	}
	// All 31 steps should appear in a 1600×1200 image.
	if len(seen) < 31 {
		t.Errorf("only %d distinct wipe steps (want 31)", len(seen))
	}
}

// TestUniqueColors6: image with exactly the 6 palette colors → UniqueColors() == 6.
func TestUniqueColors6(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 6, 1))
	palColors := [][3]uint8{
		{30, 30, 30}, {149, 162, 165}, {166, 165, 17}, {121, 23, 17}, {0, 76, 136}, {46, 91, 65},
	}
	for i, c := range palColors {
		img.SetRGBA(i, 0, color.RGBA{c[0], c[1], c[2], 255})
	}
	if n := UniqueColors(img); n != 6 {
		t.Errorf("UniqueColors: got %d want 6", n)
	}
}

// TestUniqueColorsPhoto: a gradient image has many distinct colors.
func TestUniqueColorsPhoto(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := range 100 {
		for x := range 100 {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 2), uint8(y * 2), 128, 255})
		}
	}
	if n := UniqueColors(img); n <= 6 {
		t.Errorf("UniqueColors: got %d for gradient, want > 6", n)
	}
}

// TestSamsungTwoPaletteEncode: PNG output uses send palette, not display palette.
func TestSamsungTwoPaletteEncode(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 1))
	for i := range 4 {
		img.SetRGBA(i, 0, color.RGBA{uint8(i * 60), 128, 64, 255})
	}
	indices := StuckiDither(img, PaletteSamsungDisplay, stuckiOptions{})
	out := RenderIndicesToRGB(indices, PaletteSamsungSend)
	for y := 0; y < out.Bounds().Dy(); y++ {
		for x := 0; x < out.Bounds().Dx(); x++ {
			r, g, b, _ := out.At(x, y).RGBA()
			r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)
			found := false
			for _, c := range PaletteSamsungSend {
				if r8 == uint8(c[0]) && g8 == uint8(c[1]) && b8 == uint8(c[2]) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("pixel (%d,%d) rgb=%d,%d,%d not in PaletteSamsungSend", x, y, r8, g8, b8)
			}
		}
	}
	// Panel white (P2) must not appear in PNG when send white is #FFFFFF.
	r, g, b, _ := out.At(0, 0).RGBA()
	if uint8(r>>8) == 169 && uint8(g>>8) == 175 {
		t.Error("output used display palette white (#A9AFAF) instead of send white")
	}
	_ = b
}

// TestStuckiTwoPaletteUsesDisplay: dither targets P2, output uses P1 send RGB.
func TestStuckiTwoPaletteUsesDisplay(t *testing.T) {
	// Solid panel-yellow in P2 space should pick yellow ink index.
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	y := PaletteSamsungDisplay[2]
	img.Set(0, 0, color.RGBA{uint8(y[0]), uint8(y[1]), uint8(y[2]), 255})
	for x := 1; x < 8; x++ {
		for yp := 0; yp < 8; yp++ {
			img.Set(x, yp, color.RGBA{uint8(y[0]), uint8(y[1]), uint8(y[2]), 255})
		}
	}
	indices := StuckiTwoPalette(img, PaletteSamsungDisplay, ColorPipeline{}, false, stuckiOptions{})
	if indices[4][4] != 2 {
		t.Fatalf("expected yellow index 2, got %d", indices[4][4])
	}
	out := RenderIndicesToRGB(indices, PaletteSamsungSend)
	r, g, b, _ := out.At(4, 4).RGBA()
	s := PaletteSamsungSend[2]
	if uint8(r>>8) != uint8(s[0]) || uint8(g>>8) != uint8(s[1]) || uint8(b>>8) != uint8(s[2]) {
		t.Fatalf("P1 output rgb=%d,%d,%d want %v", r>>8, g>>8, b>>8, s)
	}
}

func TestRemapSamsungSendPNGToDisplay(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	s := PaletteSamsungSend[2]
	img.Set(0, 0, color.RGBA{uint8(s[0]), uint8(s[1]), uint8(s[2]), 255})
	img.Set(1, 0, color.RGBA{255, 255, 255, 255})
	p1 := encodePNG(img)
	p2, err := RemapSamsungSendPNGToDisplay(p1)
	if err != nil {
		t.Fatal(err)
	}
	out, err := png.Decode(bytes.NewReader(p2))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := out.At(0, 0).RGBA()
	d := PaletteSamsungDisplay[2]
	if uint8(r>>8) != uint8(d[0]) || uint8(g>>8) != uint8(d[1]) || uint8(b>>8) != uint8(d[2]) {
		t.Fatalf("yellow pixel rgb=%d,%d,%d want P2 %v", r>>8, g>>8, b>>8, d)
	}
	r, g, b, _ = out.At(1, 0).RGBA()
	w := PaletteSamsungDisplay[1]
	if uint8(r>>8) != uint8(w[0]) || uint8(g>>8) != uint8(w[1]) || uint8(b>>8) != uint8(w[2]) {
		t.Fatalf("white pixel rgb=%d,%d,%d want P2 %v", r>>8, g>>8, b>>8, w)
	}
}

// TestStuckiOutputRange: dither output indices are all in [0,5].
func TestStuckiOutputRange(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	rng := rand.New(rand.NewSource(42))
	for y := range 24 {
		for x := range 32 {
			img.SetRGBA(x, y, color.RGBA{
				uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255,
			})
		}
	}
	indices := StuckiDither(img, PaletteInkJoy, stuckiOptions{})
	for y, row := range indices {
		for x, v := range row {
			if v > 5 {
				t.Errorf("index out of range at (%d,%d): %d", y, x, v)
			}
		}
	}
}

// TestLABEnhanceRange: output pixels stay in [0,255].
func TestLABEnhanceRange(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	rng := rand.New(rand.NewSource(7))
	for y := range 16 {
		for x := range 16 {
			img.SetRGBA(x, y, color.RGBA{
				uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255,
			})
		}
	}
	out := ApplyLABProcessing(img, ColorPipeline{
		LABChromaEnabled: true, LABChromaStrength: 1,
		LABHighlightEnabled: true, LABHighlightStrength: 1,
	}, PaletteSamsungDisplay, false)
	bounds := out.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := out.At(x, y).RGBA()
			r8, g8, b8 := r>>8, g>>8, b>>8
			if r8 > 255 || g8 > 255 || b8 > 255 {
				t.Errorf("pixel out of range at (%d,%d): %d,%d,%d", x, y, r8, g8, b8)
			}
		}
	}
}

// TestLABEnhanceChroma: a neutral grey pixel should gain saturation.
func TestLABEnhanceChroma(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, color.RGBA{128, 128, 80, 255}) // slightly warm grey
	out := ApplyLABProcessing(img, ColorPipeline{LABChromaEnabled: true, LABChromaStrength: 1}, PaletteSamsungDisplay, false)
	r, g, b, _ := out.At(0, 0).RGBA()
	r8, g8, b8 := int(r>>8), int(g>>8), int(b>>8)
	// The warm hue should be amplified: R and B channels should differ more
	origDiff := abs(128 - 80)
	newDiff := abs(r8 - b8)
	if newDiff <= origDiff {
		t.Errorf("LABEnhance did not increase chroma: original R-B diff=%d, enhanced=%d (r=%d g=%d b=%d)",
			origDiff, newDiff, r8, g8, b8)
	}
}

func TestStuckiEdgeAttenuation(t *testing.T) {
	if stuckiEdgeAttenuation(100, 100, stuckiEdgePreserve) != 1 {
		t.Fatal("same luminance should allow full error transfer")
	}
	if a := stuckiEdgeAttenuation(30, 200, stuckiEdgePreserve); a >= 0.35 {
		t.Fatalf("hard edge attenuation = %v, want < 0.35", a)
	}
	mid := stuckiEdgeAttenuation(80, 95, stuckiEdgePreserve)
	if mid <= 0.4 || mid >= 0.95 {
		t.Fatalf("mid edge attenuation = %v, want between 0.4 and 0.95", mid)
	}
}

func TestStuckiPreservesLuminanceStep(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			v := uint8(35)
			if x >= 14 && x <= 17 {
				v = 230
			}
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	indices := StuckiDither(img, PaletteSamsungDisplay, stuckiOptionsSamsung(ColorPipeline{}))
	darkIdx := nearestColor([3]float64{35, 35, 35}, PaletteSamsungDisplay)
	brightIdx := nearestColor([3]float64{230, 230, 230}, PaletteSamsungDisplay)
	if darkIdx == brightIdx {
		t.Fatal("test palette mapping collapsed dark/bright")
	}
	darkBeside := 0
	for y := 0; y < 16; y++ {
		if indices[y][13] == byte(darkIdx) {
			darkBeside++
		}
	}
	if darkBeside < 12 {
		t.Fatalf("only %d/16 pixels beside stripe stayed dark (idx %d), edge preservation weak", darkBeside, darkIdx)
	}
}

func TestPreDitherGradientWeight(t *testing.T) {
	flat := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			flat.Set(x, y, color.RGBA{180, 190, 210, 255})
		}
	}
	if w := preDitherGradientWeight(flat, 2, 2); w != 0 {
		t.Fatalf("flat region weight = %v, want 0", w)
	}

	grad := image.NewRGBA(image.Rect(0, 0, 64, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 64; x++ {
			v := uint8(140 + x*80/63)
			grad.Set(x, y, color.RGBA{v, v, uint8(int(v) + 8), 255})
		}
	}
	if w := preDitherGradientWeight(grad, 32, 4); w <= 0 {
		t.Fatalf("smooth gradient weight = %v, want > 0", w)
	}
}

// helpers

func bytesToGrid(flat []byte, rows, cols int) [][]byte {
	grid := make([][]byte, rows)
	for y := range rows {
		grid[y] = flat[y*cols : y*cols+cols]
	}
	return grid
}

func makeGrid(rows, cols int, val byte) [][]byte {
	grid := make([][]byte, rows)
	for y := range rows {
		grid[y] = make([]byte, cols)
		for x := range cols {
			grid[y][x] = val
		}
	}
	return grid
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
