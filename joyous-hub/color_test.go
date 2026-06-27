package main

import (
	"encoding/json"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestColorConfigSaveLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewColorStore(dir)
	cfg := defaultColorConfig()
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
	if !shouldApplyLABProcessing(pipe, img, false) {
		t.Fatal("expected lab for multi-color image")
	}
	pipe.LABChromaEnabled = false
	pipe.LABHighlightEnabled = false
	if shouldApplyLABProcessing(pipe, img, false) {
		t.Fatal("expected off when disabled")
	}
	if shouldApplyLABProcessing(pipe, img, true) {
		t.Fatal("expected off for flat RGB")
	}
}

func TestLABProcessingChromaOnly(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{240, 240, 240, 255})
	chroma := ApplyLABProcessing(img, ColorPipeline{LABChromaEnabled: true, LABChromaStrength: 1})
	highlight := ApplyLABProcessing(img, ColorPipeline{LABHighlightEnabled: true, LABHighlightStrength: 1})
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
	compressed := ApplyLABProcessing(blueSky, ColorPipeline{LABHighlightEnabled: true, LABHighlightStrength: 1})
	br, _, _, _ := compressed.At(0, 0).RGBA()
	if br>>8 >= 150 {
		t.Fatalf("highlight should compress bright blue sky, got %d", br>>8)
	}
}

func TestLABProcessingShadowLift(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{15, 15, 15, 255})
	out := ApplyLABProcessing(img, ColorPipeline{LABShadowEnabled: true, LABShadowStrength: 1})
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
