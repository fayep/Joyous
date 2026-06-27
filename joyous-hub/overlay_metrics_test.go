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
	lines, err := overlayRenderedLines(cfg, weather)
	if err != nil {
		t.Fatal(err)
	}
	m := overlayMetricsForLines(lines)
	if len(m.Lines) != 3 {
		t.Fatalf("lines: %d", len(m.Lines))
	}
	if m.Lines[0].FontSize != overlayFontSmall || m.Lines[1].FontSize != overlayFontLarge {
		t.Fatalf("font sizes: %+v", m.Lines)
	}
	if m.Box.WidthPx != 397 || m.Box.HeightPx != 184 {
		t.Fatalf("box: %+v", m.Box)
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
	lines, err := overlayRenderedLines(cfg, weather)
	if err != nil {
		t.Fatal(err)
	}
	if got := overlayContentWidth(lines); got != 349 {
		t.Fatalf("content width: %d", got)
	}
	if got := overlayContentHeight(lines); got != 136 {
		t.Fatalf("content height: %d", got)
	}
	w, h := overlayBoxSize(lines)
	if w != 397 || h != 184 {
		t.Fatalf("box size: %d×%d want 397×184", w, h)
	}
}

func TestOverlayMetricsTemplateError(t *testing.T) {
	cfg := defaultOverlayConfig()
	cfg.Template = "{{.Broken"
	m := overlayMetrics(cfg, WeatherSnapshot{})
	if m.Error == "" {
		t.Fatal("expected error")
	}
}
