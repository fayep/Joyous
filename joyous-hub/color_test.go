package main

import (
	"encoding/json"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestColorConfigSaveLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewColorStore(dir)
	cfg := defaultColorConfig()
	if !cfg.LABInkAffinityEnabled {
		t.Fatal("expected ink affinity on by default")
	}
	if cfg.InkJoyWipe != WipeUniform248 {
		t.Fatalf("default wipe = %q, want %q", cfg.InkJoyWipe, WipeUniform248)
	}
	cfg.LABChromaEnabled = true
	cfg.LABChromaStrength = 0.5
	cfg.LABHighlightEnabled = false
	cfg.InkJoyDisplayPreset = ColorPresetLegacy
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	got := NewColorStore(dir).Config()
	if !got.LABChromaEnabled || got.LABChromaStrength != 0.5 || got.LABHighlightEnabled || got.InkJoyDisplayPreset != ColorPresetLegacy {
		t.Fatalf("reload: %+v", got)
	}
}

func TestMigrateLegacyLABConfig(t *testing.T) {
	dir := t.TempDir()
	raw := `{"lab_enhance":true,"lab_strength":1.5,"inkjoy_display_preset":"calibrated"}`
	if err := os.WriteFile(filepath.Join(dir, colorConfigFile), []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	got := NewColorStore(dir).Config()
	if !got.LABChromaEnabled || !got.LABHighlightEnabled || got.LABChromaStrength != 1.5 {
		t.Fatalf("migrate: %+v", got)
	}
}

func TestResolveColorPipelinePresets(t *testing.T) {
	cfg := defaultColorConfig()
	pipe := ResolveColorPipeline(cfg)
	if pipe.InkJoyDisplay != PaletteInkJoyDisplay {
		t.Fatal("expected calibrated inkjoy display")
	}
	if pipe.SamsungSend != PaletteSamsungSend {
		t.Fatal("expected calibrated samsung send")
	}

	cfg.InkJoyDisplayPreset = ColorPresetCustom
	cfg.InkJoyDisplay = [6]PaletteRGB{{1, 2, 3}}
	pipe = ResolveColorPipeline(cfg)
	if pipe.InkJoyDisplay[0] != [3]float64{1, 2, 3} {
		t.Fatalf("custom: %v", pipe.InkJoyDisplay[0])
	}
}

func TestShouldApplyLABProcessing(t *testing.T) {
	pipe := ColorPipeline{LABChromaEnabled: true, LABChromaStrength: 1}
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 20), uint8(y * 20), 128, 255})
		}
	}
	if !shouldApplyLABProcessing(pipe, img, false, false) {
		t.Fatal("expected lab for multi-color image")
	}
	pipe.LABChromaEnabled = false
	pipe.LABHighlightEnabled = false
	if shouldApplyLABProcessing(pipe, img, false, false) {
		t.Fatal("expected off when disabled")
	}
	pipe.LABDynamicRangeEnabled = true
	if !shouldApplyLABProcessing(pipe, img, false, true) {
		t.Fatal("expected on when dynamic range enabled")
	}
	if shouldApplyLABProcessing(pipe, img, true, true) {
		t.Fatal("expected off for flat RGB")
	}
}

func TestLABProcessingChromaOnly(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{240, 240, 240, 255})
	chroma := ApplyLABProcessing(img, ColorPipeline{LABChromaEnabled: true, LABChromaStrength: 1}, PaletteSamsungDisplay[:], false)
	highlight := ApplyLABProcessing(img, ColorPipeline{LABHighlightEnabled: true, LABHighlightStrength: 1}, PaletteSamsungDisplay[:], false)
	cr, _, _, _ := chroma.At(0, 0).RGBA()
	hr, _, _, _ := highlight.At(0, 0).RGBA()
	if hr>>8 < 238 {
		t.Fatalf("highlight should leave neutral snow alone, got %d", hr>>8)
	}
	if abs(int(cr>>8)-240) > 2 {
		t.Fatalf("chroma should barely move neutral grey, got %d", cr>>8)
	}

	blueSky := image.NewRGBA(image.Rect(0, 0, 1, 1))
	blueSky.Set(0, 0, color.RGBA{151, 182, 240, 255})
	compressed := ApplyLABProcessing(blueSky, ColorPipeline{LABHighlightEnabled: true, LABHighlightStrength: 1}, PaletteSamsungDisplay[:], false)
	br, _, _, _ := compressed.At(0, 0).RGBA()
	if br>>8 >= 150 {
		t.Fatalf("highlight should compress bright blue sky, got %d", br>>8)
	}
}

func TestLABProcessingShadowLift(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{15, 15, 15, 255})
	out := ApplyLABProcessing(img, ColorPipeline{LABShadowEnabled: true, LABShadowStrength: 1}, PaletteSamsungDisplay[:], false)
	r, _, _, _ := out.At(0, 0).RGBA()
	if r>>8 <= 15 {
		t.Fatalf("shadow lift should brighten dark pixel, got %d", r>>8)
	}
}

func TestImageStoreClearBinCache(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)
	os.MkdirAll(store.cacheDir(), 0755)
	if err := os.WriteFile(filepath.Join(store.cacheDir(), "test.bin"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearBinCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.cacheDir(), "test.bin")); !os.IsNotExist(err) {
		t.Fatal("cache should be cleared")
	}
}

func TestLegacyColorJSONUnmarshal(t *testing.T) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(`{"lab_enhance":false}`), &raw); err != nil {
		t.Fatal(err)
	}
	cfg := ColorConfig{}
	migrateLegacyLABConfig(&cfg, raw)
	if cfg.LABChromaEnabled || cfg.LABHighlightEnabled {
		t.Fatal("expected both off from legacy false")
	}
}

// The fit maps the scene onto each panel's own black..white range (the stops
// setting only shapes the ray pass's working space).
func TestLABDynamicRangeFit(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 128, 8))
	for x := 0; x < 128; x++ {
		v := uint8(5 + x*250/127)
		for y := 0; y < 8; y++ {
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	inLo, inHi := luminancePercentiles(img, 0.01, 0.99)
	if labLSpanStops(inLo, inHi) < 5 {
		t.Fatalf("test gradient too narrow: %.2f stops", labLSpanStops(inLo, inHi))
	}
	pipe := ResolveColorPipeline(defaultColorConfig())
	pipe.LABDynamicRangeEnabled = true
	pipe.LABDynamicRangeStops = 4
	for name, pal := range map[string][][3]float64{"samsung": PaletteSamsungDisplay[:], "inkjoy": PaletteInkJoyDisplay[:]} {
		lo, hi := displayPaletteDRRange(pal)
		out := ApplyLABProcessing(img, pipe, pal, true)
		outLo, outHi := luminancePercentiles(out, 0.01, 0.99)
		got, want := labLSpanStops(outLo, outHi), labLSpanStops(lo, hi)
		if math.Abs(got-want) > 0.3 {
			t.Errorf("%s output span %.2f stops (L %.1f–%.1f), want the panel's %.2f (L %.1f–%.1f)", name, got, outLo, outHi, want, lo, hi)
		}
	}
}

// The fit's output span hangs from panel white: the scene's highlights must
// land on (not a stop below) each palette's white, whatever its black.
func TestLABDynamicRangeFitTopIsPanelWhite(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 8))
	for x := range 256 {
		for y := range 8 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(x), uint8(x), 255})
		}
	}
	for name, pal := range map[string][][3]float64{
		"10in":    PaletteInkJoyDisplay[:],
		"28in":    paletteInkJoy28Display[:],
		"samsung": PaletteSamsungDisplay[:],
	} {
		for _, stops := range []float64{2.5, 3.5, 6} {
			pipe := ResolveColorPipeline(defaultColorConfig())
			pipe.LABDynamicRangeEnabled = true
			pipe.LABDynamicRangeStops = stops
			out := ApplyLABProcessing(img, pipe, pal, true)
			_, hi := displayPaletteDRRange(pal)
			lo, _ := displayPaletteDRRange(pal)
			_, outHi := luminancePercentiles(out, 0.01, 0.99)
			outLo, _ := luminancePercentiles(out, 0.01, 0.99)
			if outHi < hi-1.5 {
				t.Errorf("%s %.1f stops: scene white -> L*%.1f, want panel white L*%.1f", name, stops, outHi, hi)
			}
			if outLo < lo-1.5 {
				t.Errorf("%s %.1f stops: scene black -> L*%.1f, below panel black L*%.1f", name, stops, outLo, lo)
			}
		}
	}
}

// People photos (the ray pass) keep the black-anchored fit the ray pass was
// tuned on: the span climbs N stops from panel black, so scene white lands N
// stops above black, not at panel white.
func TestLABDynamicRangeFitFromBlackForRay(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 8))
	for x := range 256 {
		for y := range 8 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(x), uint8(x), 255})
		}
	}
	pal := PaletteInkJoyDisplay[:]
	lo, hi := displayPaletteDRRange(pal)
	pipe := ResolveColorPipeline(defaultColorConfig())
	pipe.LABDynamicRangeEnabled = true
	pipe.LABDynamicRangeStops = 3.5
	pipe.LABDynamicRangeFromBlack = true
	out := ApplyLABProcessing(img, pipe, pal, true)
	outLo, outHi := luminancePercentiles(out, 0.01, 0.99)
	wantHi := linearYToLabL(math.Min(labLToLinearY(lo)*math.Pow(2, 3.5), labLToLinearY(hi)))
	if math.Abs(outLo-lo) > 1.5 || math.Abs(outHi-wantHi) > 1.5 {
		t.Errorf("from black: L* %.1f-%.1f, want %.1f-%.1f", outLo, outHi, lo, wantHi)
	}
	if wantHi > hi-5 {
		t.Fatalf("fixture should leave headroom under panel white (%.1f vs %.1f)", wantHi, hi)
	}
}

// remapRayDRSpan stretches the ray pass's black-anchored working span (fitted
// in the 10" reference range on every panel) over the target panel's full
// range: the span floor lands on that panel's black, the ceiling on its white,
// and order is preserved in between.
func TestRemapRayDRSpanStretchesToPanelRange(t *testing.T) {
	pipe := ResolveColorPipeline(defaultColorConfig())
	pipe.LABDynamicRangeStops = 3.5
	fitLoL, fitHiL, stops := drFitParams(pipe, rayFitPalette)
	botY, topY := drOutputSpanY(fitLoL, fitHiL, stops, true)
	ls := []float64{linearYToLabL(botY), 0, 0, 0, linearYToLabL(topY)}
	for i := 1; i < 4; i++ {
		ls[i] = ls[0] + (ls[4]-ls[0])*float64(i)/4
	}
	img := image.NewRGBA(image.Rect(0, 0, len(ls), 1))
	for x, L := range ls {
		rgb := labToSRGB([3]float64{L, 0, 0})
		img.SetRGBA(x, 0, color.RGBA{floatTo8(rgb[0]), floatTo8(rgb[1]), floatTo8(rgb[2]), 255})
	}
	for name, pal := range map[string][][3]float64{"10in": PaletteInkJoyDisplay[:], "28in": paletteInkJoy28Display[:]} {
		outLoL, outHiL, _ := drFitParams(pipe, pal)
		out := remapRayDRSpan(img, pipe, rayFitPalette, pal)
		prev := -1.0
		for x := range ls {
			c := out.(*image.RGBA).RGBAAt(x, 0)
			L := srgbToLAB([3]float64{float64(c.R) / 255, float64(c.G) / 255, float64(c.B) / 255})[0]
			if L <= prev {
				t.Errorf("%s x=%d: L* %.1f not above %.1f (order lost)", name, x, L, prev)
			}
			prev = L
			switch x {
			case 0:
				if math.Abs(L-outLoL) > 1.5 {
					t.Errorf("%s: span floor -> L*%.1f, want panel black L*%.1f", name, L, outLoL)
				}
			case len(ls) - 1:
				if math.Abs(L-outHiL) > 1.5 {
					t.Errorf("%s: span ceiling (L*%.1f) -> L*%.1f, want panel white L*%.1f", name, ls[x], L, outHiL)
				}
			}
		}
	}
}

// People photos go through the ray pass in its black-anchored space but must
// still use the whole panel: a full-range ramp reaches both black and white ink.
func TestPeoplePhotoUsesPanelBlackAndWhite(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 32))
	for x := range 256 {
		for y := range 32 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(x), uint8(x), 255})
		}
	}
	pipe := ResolveColorPipeline(defaultColorConfig())
	pipe.LABDynamicRangeEnabled = true
	pipe.LABDynamicRangeStops = 3.5
	pipe.LABInkAffinityRayEnabled = true
	pipe.LABInkAffinityRayStrength = 1
	pipe.PeopleLikely = true
	if !shouldApplyInkAffinityRay(pipe, img, false) {
		t.Fatal("fixture should take the ray path")
	}
	pal := PaletteInkJoyDisplay[:]
	idx := StuckiTwoPalette(img, pal, pipe, false, stuckiOptionsInkJoy(pipe))
	share := func(x0, x1 int, ink byte) float64 {
		n, hit := 0, 0
		for _, row := range idx {
			for _, i := range row[x0:x1] {
				n++
				if i == ink {
					hit++
				}
			}
		}
		return float64(hit) / float64(n)
	}
	if w := share(240, 256, 1); w < 0.6 {
		t.Errorf("brightest end is %.0f%% white ink, want mostly white", 100*w)
	}
	if k := share(0, 16, 0); k < 0.6 {
		t.Errorf("darkest end is %.0f%% black ink, want mostly black", 100*k)
	}
}

func TestDisplayPaletteDRRangeValid(t *testing.T) {
	for _, pal := range [][][3]float64{PaletteInkJoyDisplay[:], PaletteSamsungDisplay[:]} {
		lo, hi := displayPaletteDRRange(pal)
		if hi <= lo || lo < 0 || hi > 100 {
			t.Fatalf("bad DR range %.1f–%.1f", lo, hi)
		}
	}
}

func TestDisplayPaletteLABRangeInkJoyVsSamsung(t *testing.T) {
	inkLo, inkHi := displayPaletteLABRange(PaletteInkJoyDisplay[:])
	samLo, samHi := displayPaletteLABRange(PaletteSamsungDisplay[:])
	// Click-calibrated InkJoy may be within ~2 L* of Samsung reference black.
	if inkLo < samLo-2.0 {
		t.Fatalf("inkjoy black L* %.1f much darker than samsung %.1f", inkLo, samLo)
	}
	if inkHi <= samHi {
		t.Fatalf("inkjoy white L* %.1f should be brighter than samsung %.1f", inkHi, samHi)
	}
}

func TestLABDynamicRangeStopsValidation(t *testing.T) {
	dir := t.TempDir()
	store := NewColorStore(dir)
	cfg := defaultColorConfig()
	cfg.LABDynamicRangeStops = 8
	if err := store.Save(cfg); err == nil {
		t.Fatal("expected validation error for stops > 6")
	}
}
