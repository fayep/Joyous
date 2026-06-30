package main

import (
	"image"
	"image/color"
	"testing"
	"time"
)

func TestOverlayEncodePinsWhiteAfterDR(t *testing.T) {
	initOverlayFonts()
	if overlayFontErr != nil {
		t.Skip(overlayFontErr)
	}

	photo := image.NewRGBA(image.Rect(0, 0, frameW, frameH))
	for y := 0; y < frameH; y++ {
		for x := 0; x < frameW; x++ {
			v := uint8(40 + x*180/frameW)
			photo.Set(x, y, color.RGBA{v, v / 2, v / 3, 255})
		}
	}

	cfg := defaultOverlayConfig()
	cfg.WeatherStyle = overlayWeatherStyleOutline
	weather := WeatherSnapshot{
		TempC:       18,
		Condition:   "Clear",
		City:        "OverlayTest",
		DisplayDate: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
		Temperature: OverlayTemperature{Current: 18, Min: 12, Max: 22},
	}
	mutate := func(img image.Image, flatRGB bool) (image.Image, error) {
		return drawWeatherOverlay(img, cfg, weather, "", false), nil
	}

	pipe := ResolveColorPipeline(defaultColorConfig())
	pipe.LABDynamicRangeEnabled = true
	pipe.LABDynamicRangeStops = 4.5
	pipe.LABInkAffinityEnabled = true
	pipe.LABChromaEnabled = true

	bin, err := encodeInkJoyBinWithOverlay(photo, false, pipe, nil, WipeUniform248, mutate)
	if err != nil {
		t.Fatal(err)
	}
	binPlain, err := encodeInkJoyBinFromRGBA(photo, false, pipe, nil, WipeUniform248)
	if err != nil {
		t.Fatal(err)
	}
	hi, _ := FromBin(bin, frameW, frameH)
	hiPlain, _ := FromBin(binPlain, frameW, frameH)

	changed := 0
	for y := 0; y < frameH; y++ {
		for x := 0; x < frameW; x++ {
			if hi[y][x] == hiPlain[y][x] {
				continue
			}
			changed++
		}
	}
	if changed < 100 {
		t.Fatalf("expected overlay to change many pixels, got %d", changed)
	}

	img, err := decodeBinToImage(bin, false, pipe.InkJoyDisplay)
	if err != nil {
		t.Fatal(err)
	}
	for y := frameH - 220; y < frameH; y++ {
		for x := 0; x < frameW; x++ {
			if hi[y][x] != 0x02 {
				continue
			}
			r, g, b, _ := img.At(x, y).RGBA()
			want := PaletteInkJoyDisplay[1]
			if abs(int(r>>8)-int(want[0])) > 2 || abs(int(g>>8)-int(want[1])) > 2 || abs(int(b>>8)-int(want[2])) > 2 {
				t.Fatalf("white glyph at %d,%d rgb=(%d,%d,%d) want P2 white", x, y, r>>8, g>>8, b>>8)
			}
		}
	}
}

func TestOverlayEncodeSkipsRandomWipeReapply(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)
	store.SetColorStore(NewColorStore(dir))

	id, err := store.Store(bytesReader(makeTestPNG()), "test.png")
	if err != nil {
		t.Fatal(err)
	}

	token := "overlaytest"
	cfg := defaultOverlayConfig()
	cfg.WeatherStyle = overlayWeatherStyleOutline
	weather := WeatherSnapshot{City: "X", DisplayDate: time.Now(), Temperature: OverlayTemperature{Current: 20}}

	mutate := func(img image.Image, flatRGB bool) (image.Image, error) {
		return drawWeatherOverlay(img, cfg, weather, "", false), nil
	}
	bin1, err := store.ServeBinOrientationOverlay(id, false, token, mutate)
	if err != nil {
		t.Fatal(err)
	}
	bin2, err := store.ServeBinOrientationOverlay(id, false, token, mutate)
	if err != nil {
		t.Fatal(err)
	}
	if len(bin1) != len(bin2) {
		t.Fatalf("length mismatch %d vs %d", len(bin1), len(bin2))
	}
	for i := range bin1 {
		if bin1[i] != bin2[i] {
			t.Fatalf("overlay cache hit re-randomized wipe at byte %d", i)
		}
	}
}

func makeTestPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{100, 120, 140, 255})
		}
	}
	return encodePNG(img)
}
