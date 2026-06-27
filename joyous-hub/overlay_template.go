package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	"golang.org/x/image/font"
)

// OverlayTemplateData is the root value for overlay text templates.
type OverlayTemplateData struct {
	City          string
	Condition     string
	Date          time.Time
	ObservedAt    time.Time
	Temperature   OverlayTemperature
	Precipitation OverlayPrecipitation
	UseFahrenheit bool
	DateStyle     int
}

type OverlayTemperature struct {
	Current float64
	Min     float64
	Max     float64
}

type OverlayPrecipitation struct {
	Hour int // current hour %
	Max  int // daily max %
}

type overlayLine struct {
	text string
	face font.Face
}

func defaultOverlayTemplate() string {
	return "{{.City}}\n{{date .Date .DateStyle}}\n{{if .UseFahrenheit}}{{fahrenheit .Temperature.Current}}{{else}}{{celsius .Temperature.Current}}{{end}}  {{.Condition}}"
}

func effectiveOverlayTemplate(cfg OverlayConfig) string {
	if t := strings.TrimSpace(cfg.Template); t != "" {
		return t
	}
	return legacyOverlayTemplate(cfg)
}

func legacyOverlayTemplate(cfg OverlayConfig) string {
	var lines []string
	if cfg.ShowCity {
		lines = append(lines, "{{.City}}")
	}
	if cfg.ShowDate {
		lines = append(lines, "{{date .Date .DateStyle}}")
	}
	if cfg.ShowTemp {
		lines = append(lines, "{{if .UseFahrenheit}}{{fahrenheit .Temperature.Current}}{{else}}{{celsius .Temperature.Current}}{{end}}")
	}
	if cfg.ShowCondition {
		lines = append(lines, "{{.Condition}}")
	}
	if len(lines) == 0 {
		return defaultOverlayTemplate()
	}
	return strings.Join(lines, "\n")
}

func overlayTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"fahrenheit": formatFahrenheit,
		"celsius":    formatCelsius,
		"date":       formatOverlayDate,
		"pct":        formatPercent,
	}
}

func formatFahrenheit(c float64) string {
	return formatOverlayTemp(c, true)
}

func formatCelsius(c float64) string {
	return formatOverlayTemp(c, false)
}

func formatPercent(n int) string {
	return strconv.Itoa(n) + "%"
}

func (weather WeatherSnapshot) templateData(cfg OverlayConfig) OverlayTemplateData {
	cur := weather.Temperature.Current
	if cur == 0 && weather.TempC != 0 {
		cur = weather.TempC
	}
	return OverlayTemplateData{
		City:          weather.City,
		Condition:     weather.Condition,
		Date:          weather.DisplayDate,
		ObservedAt:    weather.ObservedAt,
		Temperature:   weather.Temperature,
		Precipitation: weather.Precipitation,
		UseFahrenheit: cfg.UseFahrenheit,
		DateStyle:     cfg.DateStyle,
	}
}

func executeOverlayTemplate(cfg OverlayConfig, weather WeatherSnapshot) (string, error) {
	tmplText := effectiveOverlayTemplate(cfg)
	tmpl, err := template.New("overlay").Funcs(overlayTemplateFuncs()).Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("overlay template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, weather.templateData(cfg)); err != nil {
		return "", fmt.Errorf("overlay template: %w", err)
	}
	return buf.String(), nil
}

func overlayRenderedLines(cfg OverlayConfig, weather WeatherSnapshot) ([]overlayLine, error) {
	raw, err := executeOverlayTemplate(cfg, weather)
	if err != nil {
		return nil, err
	}
	large, medium, _ := overlayFacesStandard()
	var lines []overlayLine
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		face := overlayLineFace(i, line)
		if face == nil {
			switch i {
			case 0:
				face = medium
			case 1:
				face = large
			default:
				face = medium
			}
		}
		lines = append(lines, overlayLine{text: line, face: face})
	}
	return lines, nil
}

func overlayLineFace(index int, _ string) font.Face {
	large, medium, _ := overlayFacesStandard()
	switch index {
	case 0:
		return medium
	case 1:
		return large
	default:
		return medium
	}
}

func overlayLineStepAfter(index int) int {
	if index == 1 {
		return overlayDateStep
	}
	return overlayLineStep
}

// overlayContentWidth is X: the widest rendered line.
func overlayContentWidth(lines []overlayLine) int {
	maxW := 0
	for _, ln := range lines {
		if w := overlayLineWidthPx(ln); w > maxW {
			maxW = w
		}
	}
	return maxW
}

// overlayContentHeight is Y: the sum of each line's row height.
func overlayContentHeight(lines []overlayLine) int {
	h := 0
	for i := range lines {
		h += overlayLineStepAfter(i)
	}
	return h
}

// overlayBoxSize returns the content box plus a small border on all sides.
func overlayBoxSize(lines []overlayLine) (width, height int) {
	if len(lines) == 0 {
		return overlayPadMin * 2, overlayPadMin * 2
	}
	return overlayContentWidth(lines) + 2*overlayPadMin,
		overlayContentHeight(lines) + 2*overlayPadMin
}
