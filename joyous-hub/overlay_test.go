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
	for _, size := range []struct {
		name string
		w, h int
	}{
		{"inkjoy", 1600, 1200},
		{"samsung", 2560, 1440},
	} {
		t.Run(size.name, func(t *testing.T) {
			src := image.NewRGBA(image.Rect(0, 0, size.w, size.h))
			for y := 0; y < size.h; y++ {
				for x := 0; x < size.w; x++ {
					src.Set(x, y, color.RGBA{120, 140, 180, 255})
				}
			}
			cfg := defaultOverlayConfig()
			weather := WeatherSnapshot{
				TempC:       20,
				Condition:   "Clear",
				City:        "Testville",
				DisplayDate: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
				Temperature: OverlayTemperature{Current: 20, Min: 14, Max: 22},
			}
			out := drawWeatherOverlay(src, cfg, weather, false)
			if similarColor(out.At(100, size.h-50), src.At(100, size.h-50)) {
				t.Fatal("expected box tint bottom-left")
			}
			if !similarColor(out.At(size.w-100, size.h-50), src.At(size.w-100, size.h-50)) {
				t.Fatal("expected bottom-right untouched")
			}
			if !similarColor(out.At(100, 100), src.At(100, 100)) {
				t.Fatal("expected top-left untouched")
			}
		})
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
