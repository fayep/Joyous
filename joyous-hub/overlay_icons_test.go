package main

import (
	"image"
	"strings"
	"testing"

	"golang.org/x/image/font"
)

func TestOverlayRainGlyph(t *testing.T) {
	if overlayRainGlyph() != string(overlayIconRainRune) {
		t.Fatal("expected rain icon sentinel")
	}
}

func TestExecuteOverlayTemplateRain(t *testing.T) {
	cfg := defaultOverlayConfig()
	cfg.Template = "{{.Rain}} {{.Condition}}"
	weather := WeatherSnapshot{Condition: "Clear", WeatherCode: 0}
	out, err := executeOverlayTemplate(cfg, weather)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, string(overlayIconRainRune)) {
		t.Fatalf("expected rain icon marker in out=%q", out)
	}
	if !strings.Contains(out, "Clear") {
		t.Fatalf("out=%q", out)
	}
}

func TestOverlayLineContentWidthWithRainIcon(t *testing.T) {
	face := overlayFace(40)
	if face == nil {
		t.Fatal("font unavailable")
	}
	ln := overlayLine{
		text:   string(overlayIconRainRune) + " Rain",
		face:   face,
		fontPx: 40,
	}
	iconW := overlayLineContentWidth(overlayLine{text: string(overlayIconRainRune), face: face, fontPx: 40})
	textW := font.MeasureString(face, " Rain").Ceil()
	total := overlayLineContentWidth(ln)
	if total != iconW+textW {
		t.Fatalf("width=%d want %d", total, iconW+textW)
	}
}

func TestRenderRainCloudSVG(t *testing.T) {
	img := renderRainCloudSVG(48, image.White, 2)
	if img == nil {
		t.Fatal("expected rendered icon")
	}
	if got, want := img.Bounds().Dx(), 48; got != want {
		t.Fatalf("width=%d want %d", got, want)
	}
	if got, want := img.Bounds().Dy(), overlayRainIconHeight(48); got != want {
		t.Fatalf("height=%d want %d", got, want)
	}
	found := false
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a > 0 {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatal("expected non-empty pixels")
	}
}
