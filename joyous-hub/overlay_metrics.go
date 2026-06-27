package main

import (
	"golang.org/x/image/font"
)

// OverlayLineMetrics describes one rendered overlay row.
type OverlayLineMetrics struct {
	Index    int    `json:"index"`
	Text     string `json:"text"`
	FontSize int    `json:"font_size"`
	WidthPx  int    `json:"width_px"`
	StepPx   int    `json:"step_px"`
}

// OverlayBoxMetrics summarizes content-sized overlay dimensions.
type OverlayBoxMetrics struct {
	Lines   []OverlayLineMetrics `json:"lines"`
	Content struct {
		WidthPx  int `json:"width_px"`
		HeightPx int `json:"height_px"`
	} `json:"content"`
	Box struct {
		WidthPx  int `json:"width_px"`
		HeightPx int `json:"height_px"`
		BorderPx int `json:"border_px"`
	} `json:"box"`
	Error string `json:"error,omitempty"`
}

func overlayLineFontSize(index int) int {
	switch index {
	case 0:
		return overlayFontSmall
	case 1:
		return overlayFontLarge
	default:
		return overlayFontMedium
	}
}

func overlayLineWidthPx(ln overlayLine) int {
	if ln.face == nil || ln.text == "" {
		return 0
	}
	return font.MeasureString(ln.face, ln.text).Ceil()
}

func overlayLineStepPx(index int) int {
	return overlayLineStepAfter(index)
}

func overlayMetricsForLines(lines []overlayLine) OverlayBoxMetrics {
	var m OverlayBoxMetrics
	for i, ln := range lines {
		m.Lines = append(m.Lines, OverlayLineMetrics{
			Index:    i + 1,
			Text:     ln.text,
			FontSize: overlayLineFontSize(i),
			WidthPx:  overlayLineWidthPx(ln),
			StepPx:   overlayLineStepPx(i),
		})
	}
	m.Content.WidthPx = overlayContentWidth(lines)
	m.Content.HeightPx = overlayContentHeight(lines)
	m.Box.WidthPx, m.Box.HeightPx = overlayBoxSize(lines)
	m.Box.BorderPx = overlayPadMin
	return m
}

func overlayMetrics(cfg OverlayConfig, weather WeatherSnapshot) OverlayBoxMetrics {
	lines, err := overlayRenderedLines(cfg, weather)
	if err != nil {
		return OverlayBoxMetrics{Error: err.Error()}
	}
	return overlayMetricsForLines(lines)
}
