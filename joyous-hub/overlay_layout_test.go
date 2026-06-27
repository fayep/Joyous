package main

import (
	"testing"
	"time"
)

func TestOverlayLayoutForSize(t *testing.T) {
	if overlayLayoutForSize(1600, 1200) != overlayLayoutBar {
		t.Fatal("InkJoy should use bottom bar")
	}
	if overlayLayoutForSize(2560, 1440) != overlayLayoutPanel {
		t.Fatal("32\" Samsung should use corner panel")
	}
	if overlayLayoutForSize(1440, 2560) != overlayLayoutPanel {
		t.Fatal("portrait Samsung should use corner panel")
	}
}

func TestOverlayPanelDimensions(t *testing.T) {
	initOverlayFonts()
	if overlayFontErr != nil {
		t.Skip(overlayFontErr)
	}
	if got := overlayBarHeight(frameH); got != 240 {
		t.Fatalf("inkjoy bar height: %d", got)
	}
	cfg := defaultOverlayConfig()
	weather := WeatherSnapshot{
		City:        "Testville",
		Condition:   "Partly Cloudy",
		TempC:       20,
		DisplayDate: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
	}
	if got := overlayPanelHeightFor(cfg, weather); got != 248 {
		t.Fatalf("panel height for full stack: %d", got)
	}
	if got := overlayPanelWidthFor(cfg, weather); got != 374 {
		t.Fatalf("panel width for full stack: %d", got)
	}
}
