package main

// overlayLayoutKind selects how weather/date is composited onto a frame image.
type overlayLayoutKind int

const (
	overlayLayoutBar   overlayLayoutKind = iota // full-width bottom band (InkJoy)
	overlayLayoutPanel                          // bottom-left panel (large Samsung)
)

// overlayPanelPixelThreshold: above this pixel count use a corner panel instead of a full-width bar.
// InkJoy 1600×1200 ≈ 1.9M; Samsung EM32 2560×1440 ≈ 3.7M.
const overlayPanelPixelThreshold = 3_000_000

// Large-display panel matches InkJoy typography; box size fits stacked content.
const (
	overlayFontLarge    = 56
	overlayFontMedium   = 40
	overlayFontSmall    = 30
	overlayPadMin       = 24
	overlayLineStep     = 36
	overlayDateStep     = 64
	overlayBarHeightMin = 120
)

func overlayLayoutForSize(w, h int) overlayLayoutKind {
	if w <= 0 || h <= 0 {
		return overlayLayoutBar
	}
	if w*h >= overlayPanelPixelThreshold {
		return overlayLayoutPanel
	}
	return overlayLayoutBar
}

// overlayBarHeight is the InkJoy bottom-band height for a frame of height h.
func overlayBarHeight(h int) int {
	barH := h / 5
	if barH < overlayBarHeightMin {
		barH = overlayBarHeightMin
	}
	return barH
}

// overlayPanelHeightFor sizes the Samsung panel to its stacked text (InkJoy font metrics).
func overlayPanelHeightFor(cfg OverlayConfig, weather WeatherSnapshot) int {
	h := overlayPadMin
	if cfg.ShowCity && weather.City != "" {
		h += overlayLineStep
	}
	if cfg.ShowDate {
		h += overlayDateStep
	}
	if cfg.ShowTemp {
		h += overlayDateStep
	}
	if cfg.ShowCondition && weather.Condition != "" {
		h += overlayLineStep
	}
	return h + overlayPadMin
}

func overlayPadForWidth(w int) int {
	pad := w / 40
	if pad < overlayPadMin {
		pad = overlayPadMin
	}
	return pad
}

func overlayInt(v float64) int {
	if v < 1 {
		return 1
	}
	return int(v + 0.5)
}
