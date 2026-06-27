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
	Style string `json:"style,omitempty"`
	Error string `json:"error,omitempty"`
}

// OverlayMetricsResponse reports overlay sizing for each frame type.
type OverlayMetricsResponse struct {
	Samsung OverlayBoxMetrics `json:"samsung"`
	InkJoy  OverlayBoxMetrics `json:"inkjoy"`
}

func overlayLineWidthPx(ln overlayLine) int {
	if ln.face == nil || ln.text == "" {
		return 0
	}
	return font.MeasureString(ln.face, ln.text).Ceil()
}

func overlayMetricsForLines(lines []overlayLine, w, h int) OverlayBoxMetrics {
	var m OverlayBoxMetrics
	for i, ln := range lines {
		m.Lines = append(m.Lines, OverlayLineMetrics{
			Index:    i + 1,
			Text:     ln.text,
			FontSize: ln.fontPx,
			WidthPx:  overlayLineWidthPx(ln),
			StepPx:   ln.stepPx,
		})
	}
	m.Content.WidthPx = overlayContentWidth(lines)
	m.Content.HeightPx = overlayContentHeight(lines)
	m.Box.WidthPx, m.Box.HeightPx = overlayBoxSize(lines, w, h)
	m.Box.BorderPx = overlayScaledPx(overlayPadMinPt, w, h)
	return m
}

func overlayMetrics(cfg OverlayConfig, weather WeatherSnapshot, w, h int) OverlayBoxMetrics {
	lines, err := overlayRenderedLines(cfg, weather, w, h)
	if err != nil {
		return OverlayBoxMetrics{Error: err.Error()}
	}
	m := overlayMetricsForLines(lines, w, h)
	m.Style = normalizeWeatherStyle(cfg.WeatherStyle)
	if m.Style == overlayWeatherStyleOutline {
		m.Box.WidthPx = m.Content.WidthPx
		m.Box.HeightPx = m.Content.HeightPx
		m.Box.BorderPx = 0
	}
	return m
}

func overlayMetricsForDisplays(cfg OverlayConfig, weather WeatherSnapshot, portrait bool) OverlayMetricsResponse {
	samW, samH := overlayMetricsDimensions(samsungW, samsungH, portrait)
	ijW, ijH := overlayMetricsDimensions(frameW, frameH, portrait)
	return OverlayMetricsResponse{
		Samsung: overlayMetrics(cfg, weather, samW, samH),
		InkJoy:  overlayMetrics(cfg, weather, ijW, ijH),
	}
}
