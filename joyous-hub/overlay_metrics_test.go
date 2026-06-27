package main

import (
	"testing"
	"time"
)

func TestOverlayMetricsForLines(t *testing.T) {
	initOverlayFonts()
	if overlayFontErr != nil {
		t.Skip(overlayFontErr)
	}
	cfg := defaultOverlayConfig()
	weather := WeatherSnapshot{
		City:        "Testville",
		Condition:   "Partly Cloudy",
		DisplayDate: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
		Temperature: OverlayTemperature{Current: 20, Min: 14, Max: 22},
	}
	w, h := overlayFontRefWidth, overlayFontRefHeight
	lines, err := overlayRenderedLines(cfg, weather, w, h)
	if err != nil {
		t.Fatal(err)
	}
	m := overlayMetricsForLines(lines, w, h)
	if len(m.Lines) != 3 {
		t.Fatalf("lines: %d", len(m.Lines))
	}
	if m.Lines[0].FontSize != overlayFontMediumPt || m.Lines[1].FontSize != overlayFontLargePt {
		t.Fatalf("font sizes: %+v", m.Lines)
	}
	if m.Lines[2].FontSize != overlayFontMediumPt {
		t.Fatalf("last line font: %+v", m.Lines[2])
	}
	if m.Box.WidthPx != 397 || m.Box.HeightPx != 184 {
		t.Fatalf("box: %+v", m.Box)
	}
}

func TestOverlayMetricsInkJoyScale(t *testing.T) {
	initOverlayFonts()
	if overlayFontErr != nil {
		t.Skip(overlayFontErr)
	}
	cfg := defaultOverlayConfig()
	weather := WeatherSnapshot{
		City:        "Testville",
		Condition:   "Partly Cloudy",
		DisplayDate: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
		Temperature: OverlayTemperature{Current: 20, Min: 14, Max: 22},
	}
	samsung := overlayMetrics(cfg, weather, overlayFontRefWidth, overlayFontRefHeight)
	inkjoy := overlayMetrics(cfg, weather, frameW, frameH)
	if inkjoy.Lines[1].FontSize <= samsung.Lines[1].FontSize {
		t.Fatalf("inkjoy large font %d should exceed samsung %d", inkjoy.Lines[1].FontSize, samsung.Lines[1].FontSize)
	}
	if inkjoy.Lines[0].FontSize <= samsung.Lines[0].FontSize {
		t.Fatalf("inkjoy medium font %d should exceed samsung %d", inkjoy.Lines[0].FontSize, samsung.Lines[0].FontSize)
	}
}

func TestOverlayBoxDimensions(t *testing.T) {
	initOverlayFonts()
	if overlayFontErr != nil {
		t.Skip(overlayFontErr)
	}
	cfg := defaultOverlayConfig()
	weather := WeatherSnapshot{
		City:        "Testville",
		Condition:   "Partly Cloudy",
		DisplayDate: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
		Temperature: OverlayTemperature{Current: 20, Min: 14, Max: 22},
	}
	w, h := overlayFontRefWidth, overlayFontRefHeight
	lines, err := overlayRenderedLines(cfg, weather, w, h)
	if err != nil {
		t.Fatal(err)
	}
	if got := overlayContentWidth(lines); got != 349 {
		t.Fatalf("content width: %d", got)
	}
	if got := overlayContentHeight(lines); got != 136 {
		t.Fatalf("content height: %d", got)
	}
	boxW, boxH := overlayBoxSize(lines, w, h)
	if boxW != 397 || boxH != 184 {
		t.Fatalf("box size: %d×%d want 397×184", boxW, boxH)
	}
}

func TestOverlayMetricsTemplateError(t *testing.T) {
	cfg := defaultOverlayConfig()
	cfg.Template = "{{.Broken"
	m := overlayMetrics(cfg, WeatherSnapshot{}, overlayFontRefWidth, overlayFontRefHeight)
	if m.Error == "" {
		t.Fatal("expected error")
	}
}

func TestOverlayFontScale(t *testing.T) {
	if got := overlayFontScale(overlayFontRefWidth, overlayFontRefHeight); got < 0.99 || got > 1.01 {
		t.Fatalf("reference scale: %v want ~1", got)
	}
	if got := overlayFontScale(frameW, frameH); got < 1.45 || got > 1.49 {
		t.Fatalf("inkjoy scale: %v want ~1.47", got)
	}
}
