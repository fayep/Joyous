package main

import (
	"image"
	"image/color"
	"testing"
	"time"
)

func TestOverlayConfigSaveLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewOverlayStore(dir)
	cfg := defaultOverlayConfig()
	cfg.Location = "Portland"
	cfg.ShowCity = false
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	store2 := NewOverlayStore(dir)
	got := store2.Config()
	if got.Location != "Portland" || got.ShowCity {
		t.Fatalf("reload: %+v", got)
	}
}

func TestParseImageBinFilename(t *testing.T) {
	id, tok, portrait := parseImageBinFilename("abc123~deadbeef-p.bin")
	if id != "abc123" || tok != "deadbeef" || !portrait {
		t.Fatalf("got %q %q %v", id, tok, portrait)
	}
	id, tok, portrait = parseImageBinFilename("abc123.bin")
	if id != "abc123" || tok != "" || portrait {
		t.Fatalf("plain: %q %q %v", id, tok, portrait)
	}
	if got := imageBinFilename("abc", "tok", true); got != "abc~tok-p.bin" {
		t.Fatalf("filename: %s", got)
	}
}

func TestDrawWeatherOverlay(t *testing.T) {
	initOverlayFonts()
	if overlayFontErr != nil {
		t.Skip(overlayFontErr)
	}
	src := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 1600; x++ {
			src.Set(x, y, color.RGBA{120, 140, 180, 255})
		}
	}
	cfg := defaultOverlayConfig()
	weather := WeatherSnapshot{
		TempC:       20,
		Condition:   "Clear",
		City:        "Testville",
		DisplayDate: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
	}
	out := drawWeatherOverlay(src, cfg, weather, false)
	b := out.Bounds()
	if b.Dx() != 1600 || b.Dy() != 1200 {
		t.Fatalf("bounds: %v", b)
	}
	// Bottom band should be tinted; top-left should stay close to source.
	if !similarColor(out.At(100, 100), src.At(100, 100)) {
		t.Fatal("bar layout should not tint top-left")
	}
	if similarColor(out.At(100, 1150), src.At(100, 1150)) {
		t.Fatal("bar layout should tint bottom row")
	}
}

func TestDrawWeatherOverlayPanel(t *testing.T) {
	initOverlayFonts()
	if overlayFontErr != nil {
		t.Skip(overlayFontErr)
	}
	src := image.NewRGBA(image.Rect(0, 0, 2560, 1440))
	for y := 0; y < 1440; y++ {
		for x := 0; x < 2560; x++ {
			src.Set(x, y, color.RGBA{120, 140, 180, 255})
		}
	}
	cfg := defaultOverlayConfig()
	weather := WeatherSnapshot{
		TempC:       20,
		Condition:   "Clear",
		City:        "Testville",
		DisplayDate: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
	}
	out := drawWeatherOverlay(src, cfg, weather, false)
	// Panel is bottom-left; bottom-right should stay unchanged.
	if similarColor(out.At(100, 1300), src.At(100, 1300)) {
		t.Fatal("expected panel tint bottom-left")
	}
	if !similarColor(out.At(2400, 1300), src.At(2400, 1300)) {
		t.Fatal("expected bottom-right untouched")
	}
}

func similarColor(a, b color.Color) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	const tol = 12 // 8-bit channel tolerance
	return channelClose(ar, br, tol) && channelClose(ag, bg, tol) && channelClose(ab, bb, tol)
}

func channelClose(a, b uint32, tol int) bool {
	da := int(a>>8) - int(b>>8)
	if da < 0 {
		da = -da
	}
	return da < tol
}

func TestOverlaySendTokenStable(t *testing.T) {
	cfg := defaultOverlayConfig()
	w := WeatherSnapshot{TempC: 18.2, Condition: "Clear", DisplayDate: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)}
	a := cfg.sendToken(w, false)
	b := cfg.sendToken(w, false)
	if a != b || len(a) != 10 {
		t.Fatalf("token %q %q", a, b)
	}
	if cfg.sendToken(w, true) == cfg.sendToken(w, false) {
		t.Fatal("portrait should change token")
	}
}

func TestFormatOverlayTemp(t *testing.T) {
	if got := formatOverlayTemp(20, true); got != "68°F" {
		t.Fatalf("f: %s", got)
	}
	if got := formatOverlayTemp(20, false); got != "20°C" {
		t.Fatalf("c: %s", got)
	}
}

func TestWMOWeatherText(t *testing.T) {
	if wmoWeatherText(0) != "Clear" {
		t.Fatal("code 0")
	}
	if wmoWeatherText(1) != "Mainly clear" {
		t.Fatal("code 1")
	}
	if wmoWeatherText(2) != "Partly cloudy" {
		t.Fatal("code 2")
	}
	if wmoWeatherText(3) != "Overcast" {
		t.Fatal("code 3")
	}
}
