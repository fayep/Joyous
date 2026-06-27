package main

import (
	"strings"
	"testing"
	"time"
)

func TestExecuteOverlayTemplateDefault(t *testing.T) {
	cfg := defaultOverlayConfig()
	weather := WeatherSnapshot{
		City:        "San Francisco",
		Condition:   "Mainly clear",
		DisplayDate: time.Date(2026, 6, 27, 9, 0, 0, 0, time.UTC),
		Temperature: OverlayTemperature{Current: 15.4, Min: 13.6, Max: 17.9},
		Precipitation: OverlayPrecipitation{Hour: 2, Max: 3},
	}
	out, err := executeOverlayTemplate(cfg, weather)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "San Francisco") || !strings.Contains(out, "60°F") {
		t.Fatalf("out=%q", out)
	}
}

func TestExecuteOverlayTemplateCustom(t *testing.T) {
	cfg := defaultOverlayConfig()
	cfg.Template = "{{fahrenheit .Temperature.Min}}-{{fahrenheit .Temperature.Max}}  {{pct .Precipitation.Hour}}"
	weather := WeatherSnapshot{
		Temperature:   OverlayTemperature{Min: 13.6, Max: 17.9},
		Precipitation: OverlayPrecipitation{Hour: 5},
	}
	out, err := executeOverlayTemplate(cfg, weather)
	if err != nil {
		t.Fatal(err)
	}
	if out != "56°F-64°F  5%" {
		t.Fatalf("out=%q", out)
	}
}

func TestHourlyIndexFor(t *testing.T) {
	times := []string{"2026-06-27T08:00", "2026-06-27T09:00", "2026-06-27T10:00"}
	at := time.Date(2026, 6, 27, 9, 15, 0, 0, time.UTC)
	if got := hourlyIndexFor(times, at); got != 1 {
		t.Fatalf("index=%d", got)
	}
}

func TestOverlayTemplateInvalid(t *testing.T) {
	cfg := defaultOverlayConfig()
	cfg.Template = "{{.Broken"
	_, err := executeOverlayTemplate(cfg, WeatherSnapshot{})
	if err == nil {
		t.Fatal("expected error")
	}
}
