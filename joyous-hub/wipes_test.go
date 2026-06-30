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

func TestFlatCalibrationWipeGrid(t *testing.T) {
	loadEmbeddedWipes()
	white := flatCalibrationWipeGrid(inkjoyWhiteName)
	green := flatCalibrationWipeGrid(inkjoyGreenName)
	petals := flatCalibrationWipeGrid(inkjoyGreenPetalsName)
	uniform := flatCalibrationWipeGrid(inkjoyGreenUniformName)
	vertical := flatCalibrationWipeGrid(inkjoyCalibrationName)
	blend := calibrationBlendWipeGrid()
	if wipeFingerprint(white) != wipeFingerprint(blend) {
		t.Fatal("white calibration should use blend wipe")
	}
	if wipeFingerprint(green) != wipeFingerprint(blend) {
		t.Fatal("green calibration should use blend wipe")
	}
	if wipeFingerprint(petals) != wipeFingerprint(calibrationGreenPetalsWipeGrid()) {
		t.Fatal("green petals calibration should use wipe_petals.png")
	}
	if wipeFingerprint(uniform) != wipeFingerprint(calibrationGreenUniformWipeGrid()) {
		t.Fatal("green uniform calibration should use lo=248 everywhere")
	}
	if uniform[0][0] != 248 || uniform[frameH-1][frameW-1] != 248 {
		t.Fatal("uniform wipe should be 248 at all pixels")
	}
	if wipeFingerprint(vertical) != wipeFingerprint(wipeGridByName("wipe_vertical_sweep.png")) {
		t.Fatal("primaries calibration should use wipe_vertical_sweep.png")
	}
}

func TestCalibrationWipeIsVerticalSweep(t *testing.T) {
	loadEmbeddedWipes()
	wipe := calibrationWipeGrid()
	if len(wipe) != frameH || len(wipe[0]) != frameW {
		t.Fatalf("calibration wipe shape %dx%d", len(wipe[0]), len(wipe))
	}
	named := wipeGridByName("wipe_vertical_sweep.png")
	if named == nil {
		t.Fatal("wipe_vertical_sweep.png not embedded")
	}
	if wipeFingerprint(wipe) != wipeFingerprint(named) {
		t.Fatal("calibration wipe should be wipe_vertical_sweep.png")
	}
	// Left column finishes first; right column last; constant down each column.
	if wipe[0][0] != 0 {
		t.Fatalf("left edge lo: got %d want 0", wipe[0][0])
	}
	if wipe[0][frameW-1] != 248 {
		t.Fatalf("right edge lo: got %d want 248", wipe[0][frameW-1])
	}
	for y := 1; y < frameH; y++ {
		if wipe[y][0] != wipe[0][0] || wipe[y][frameW-1] != wipe[0][frameW-1] {
			t.Fatal("vertical sweep should be constant per column")
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
	colorStore := NewColorStore(dir)
	cfg := defaultColorConfig()
	cfg.InkJoyWipe = WipeRandom
	if err := colorStore.Save(cfg); err != nil {
		t.Fatal(err)
	}
	store.SetColorStore(colorStore)

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

func TestServeBinUsesUniformWipeByDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PNG conversion test in short mode")
	}
	dir := t.TempDir()
	store := NewImageStore(dir)
	store.SetColorStore(NewColorStore(dir))

	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			img.Set(x, y, color.RGBA{120, 100, 80, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	id, err := store.Store(bytes.NewReader(buf.Bytes()), "photo.png")
	if err != nil {
		t.Fatal(err)
	}
	bin, err := store.ServeBin(id)
	if err != nil {
		t.Fatal(err)
	}
	_, lo := FromBin(bin, frameW, frameH)
	for y := range frameH {
		for x := range frameW {
			if lo[y][x] != 248 {
				t.Fatalf("default wipe at %d,%d = %d, want 248", x, y, lo[y][x])
			}
		}
	}
}

func TestResolveWipeGridUniform248(t *testing.T) {
	lo := resolveWipeGrid(WipeUniform248)
	if len(lo) != frameH || len(lo[0]) != frameW {
		t.Fatalf("shape %dx%d", len(lo[0]), len(lo))
	}
	for y := range frameH {
		for x := range frameW {
			if lo[y][x] != 248 {
				t.Fatalf("uniform248 at %d,%d = %d", x, y, lo[y][x])
			}
		}
	}
}

func TestResolveWipeGridUniformLoSteps(t *testing.T) {
	lo0 := resolveWipeGrid("uniform:0")
	if lo0[0][0] != 0 {
		t.Fatalf("uniform:0 got %d", lo0[0][0])
	}
	lo16 := resolveWipeGrid("uniform:16")
	if lo16[0][0] != 16 {
		t.Fatalf("uniform:16 got %d", lo16[0][0])
	}
	lo248 := resolveWipeGrid("uniform:248")
	if lo248[0][0] != 248 {
		t.Fatalf("uniform:248 got %d", lo248[0][0])
	}
}

func TestInkJoyWipeUniformLevels(t *testing.T) {
	levels := inkJoyWipeUniformLevels()
	if len(levels) != wipeStepCount {
		t.Fatalf("got %d levels want %d", len(levels), wipeStepCount)
	}
	if levels[0].Lo != 0 || levels[0].ID != "uniform:0" {
		t.Fatalf("step 0: %+v", levels[0])
	}
	if levels[30].Lo != 248 || levels[30].ID != "uniform:248" {
		t.Fatalf("step 30: %+v", levels[30])
	}
}

func TestNormalizeInkJoyWipeUniform(t *testing.T) {
	if got := normalizeInkJoyWipe("uniform248"); got != "uniform:248" {
		t.Fatalf("legacy alias: %q", got)
	}
	if got := normalizeInkJoyWipe("uniform:12"); got != "uniform:8" {
		t.Fatalf("quantize: %q", got)
	}
}

func TestInkJoyWipeChoicesIncludesEmbedded(t *testing.T) {
	choices := inkJoyWipeChoices()
	if len(choices) < 3 {
		t.Fatalf("expected uniform, random, and embedded wipes, got %d", len(choices))
	}
	if choices[0].ID != WipeUniformPrefix+"picker" {
		t.Fatalf("first choice = %q", choices[0].ID)
	}
	foundClock := false
	for _, c := range choices {
		if c.ID == "wipe_clock.png" {
			foundClock = true
			break
		}
	}
	if !foundClock {
		t.Fatal("expected wipe_clock.png in choices")
	}
}

func TestServeBinGreenPixelsPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PNG conversion test in short mode")
	}
	dir := t.TempDir()
	store := NewImageStore(dir)
	img := image.NewRGBA(image.Rect(0, 0, frameW, frameH))
	green := color.RGBA{0, 255, 0, 255}
	white := color.RGBA{255, 255, 255, 255}
	for y := range frameH {
		for x := range frameW {
			if x < frameW/2 {
				img.Set(x, y, green)
			} else {
				img.Set(x, y, white)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	id, err := store.Store(bytes.NewReader(buf.Bytes()), "green-white.png")
	if err != nil {
		t.Fatal(err)
	}
	bin, err := store.ServeBin(id)
	if err != nil {
		t.Fatal(err)
	}
	greenCount := 0
	for i := 0; i < len(bin); i += 2 {
		if bin[i] != inkJoyGreenHi {
			continue
		}
		greenCount++
	}
	if greenCount == 0 {
		t.Fatal("expected some green hi pixels after dither")
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

func TestValidateInkJoyWipeLuminance(t *testing.T) {
	if err := validateInkJoyWipe(WipeLuminance); err != nil {
		t.Fatalf("validate luminance wipe: %v", err)
	}
}

func TestMakeLuminanceLoWipe(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 160, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 160; x++ {
			v := uint8(x * 255 / 159)
			img.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	lo := MakeLuminanceLoWipe(img)
	if len(lo) != 120 || len(lo[0]) != 160 {
		t.Fatalf("shape %dx%d want 160x120", len(lo[0]), len(lo))
	}
	// Inverted topo: darker left -> higher lo than right.
	left := lo[60][20]
	right := lo[60][140]
	if left <= right {
		t.Fatalf("horizontal gradient (inverted): left lo=%d right lo=%d", left, right)
	}
	// Smooth gradient uses many steps along a scanline.
	seen := map[byte]bool{}
	for x := 0; x < 160; x++ {
		seen[lo[60][x]] = true
	}
	if len(seen) < 8 {
		t.Fatalf("gradient row should use many lo steps, got %d distinct", len(seen))
	}
}

func TestLuminanceLoWipeSteepEdgeUsesMoreSteps(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 32, G: 32, B: 32, A: 255})
		}
		for x := 100; x < 200; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 220, G: 220, B: 220, A: 255})
		}
	}
	lo := MakeLuminanceLoWipe(img)
	flatDistinct := distinctLoInRect(lo, 10, 40, 30, 70)
	edgeDistinct := distinctLoInRect(lo, 92, 108, 30, 70)
	if edgeDistinct <= flatDistinct {
		t.Fatalf("edge patch distinct lo=%d should exceed flat patch=%d", edgeDistinct, flatDistinct)
	}
}

func distinctLoAlongRow(lo [][]byte, y int) int {
	seen := map[byte]bool{}
	for x := range lo[y] {
		seen[lo[y][x]] = true
	}
	return len(seen)
}

func distinctLoInRect(lo [][]byte, x0, x1, y0, y1 int) int {
	seen := map[byte]bool{}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			seen[lo[y][x]] = true
		}
	}
	return len(seen)
}

func TestLuminanceWipeNotReappliedOnServe(t *testing.T) {
	dir := t.TempDir()
	colors := NewColorStore(dir)
	colors.Save(ColorConfig{InkJoyWipe: WipeLuminance})
	store := NewImageStore(dir)
	store.SetColorStore(colors)

	img := image.NewRGBA(image.Rect(0, 0, frameW, frameH))
	for y := 0; y < frameH; y++ {
		for x := 0; x < frameW; x++ {
			v := uint8(x * 255 / (frameW - 1))
			img.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	id, err := store.Store(&buf, "gradient.png")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	bin1, err := store.ServeBin(id)
	if err != nil {
		t.Fatalf("ServeBin: %v", err)
	}
	_, lo1 := FromBin(bin1, frameW, frameH)
	bin2, err := store.ServeBin(id)
	if err != nil {
		t.Fatalf("ServeBin cache hit: %v", err)
	}
	_, lo2 := FromBin(bin2, frameW, frameH)
	if lo1[600][400] != lo2[600][400] {
		t.Fatalf("luminance lo changed on cache re-serve: %d -> %d", lo1[600][400], lo2[600][400])
	}
	if lo1[600][200] <= lo1[600][1200] {
		t.Fatalf("inverted: expected darker left higher lo than right: %d vs %d", lo1[600][200], lo1[600][1200])
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
