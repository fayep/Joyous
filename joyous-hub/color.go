package main

import (
	"image"
)

var colorNames = []string{"black", "white", "yellow", "red", "blue", "green"}

// PaletteRGB is one ink slot as 0–255 RGB (JSON-friendly).
type PaletteRGB [3]int

// ColorPipeline is the palette + LAB settings used when encoding. Renders use
// standardColorPipeline; the fields stay so tests and experiments can vary them.
type ColorPipeline struct {
	LABChromaEnabled       bool
	LABChromaStrength      float64
	LABHighlightEnabled    bool
	LABHighlightStrength   float64
	LABShadowEnabled       bool
	LABShadowStrength      float64
	LABDynamicRangeEnabled bool
	LABDynamicRangeStops   float64
	// LABDynamicRangeFromBlack anchors the DR fit's output span at panel black
	// instead of panel white; set per image by StuckiTwoPalette, not configured.
	LABDynamicRangeFromBlack bool
	PortraitEnhance          bool
	// PeopleLikely is the album image's people detection (ImageMeta.PeopleLikely),
	// independent of whether the skin-tone pass (PortraitEnhance) is enabled.
	PeopleLikely              bool
	PortraitStrength          float64
	LABInkAffinityEnabled     bool
	LABInkAffinityStrength    float64
	LABInkAffinityMixEnabled  bool
	LABInkAffinityMixStrength float64
	LABInkAffinityRayEnabled  bool
	LABInkAffinityRayStrength float64
	SharpenEnabled            bool
	SharpenAmount             float64
	SharpenRadius             float64
	InkJoyDisplay             [6][3]float64
	SamsungDisplay            [6][3]float64
	SamsungSend               [6][3]float64
}

// standardColorPipeline is the render pipeline every frame uses: sharpening,
// the dynamic-range fit and the mix and ray ink affinities, dithered against
// the calibrated palettes. Per-device palettes (Device.ColorOverride) and the
// per-image chroma boost (ImageMeta.ChromaBoost) are applied on top.
func standardColorPipeline() ColorPipeline {
	return ColorPipeline{
		LABChromaStrength:         1.0,
		LABDynamicRangeEnabled:    true,
		LABDynamicRangeStops:      3.5,
		LABInkAffinityMixEnabled:  true,
		LABInkAffinityMixStrength: 1.0,
		LABInkAffinityRayEnabled:  true,
		LABInkAffinityRayStrength: 1.0,
		SharpenEnabled:            true,
		SharpenAmount:             defaultSharpenAmount,
		SharpenRadius:             defaultSharpenRadius,
		InkJoyDisplay:             PaletteInkJoyDisplay,
		SamsungDisplay:            PaletteSamsungDisplay,
		SamsungSend:               PaletteSamsungSend,
	}
}

// paletteOverride6 resolves a per-device override (Device.ColorOverride) for a
// 6-ink panel. ok is false when there is no override or it was saved for a
// panel with a different ink count (the 28.5"'s 8), so callers keep their default.
func paletteOverride6(o []PaletteRGB) ([6][3]float64, bool) {
	if len(o) != 6 {
		return [6][3]float64{}, false
	}
	return paletteFromRGB([6]PaletteRGB(o)), true
}

// paletteFromRGBs is paletteFromRGB for any ink count.
func paletteFromRGBs(custom []PaletteRGB) [][3]float64 {
	out := make([][3]float64, len(custom))
	for i, c := range custom {
		out[i] = [3]float64{
			float64(clampInt(c[0], 0, 255)),
			float64(clampInt(c[1], 0, 255)),
			float64(clampInt(c[2], 0, 255)),
		}
	}
	return out
}

func paletteFromRGB(custom [6]PaletteRGB) [6][3]float64 {
	var out [6][3]float64
	for i := range out {
		out[i] = [3]float64{
			float64(clampInt(custom[i][0], 0, 255)),
			float64(clampInt(custom[i][1], 0, 255)),
			float64(clampInt(custom[i][2], 0, 255)),
		}
	}
	return out
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// paletteSliceToJSON is paletteToJSON for any ink count.
func paletteSliceToJSON(p [][3]float64) []PaletteRGB {
	out := make([]PaletteRGB, len(p))
	for i, c := range p {
		out[i] = PaletteRGB{int(c[0]), int(c[1]), int(c[2])}
	}
	return out
}

func shouldApplyLABProcessing(pipe ColorPipeline, img image.Image, flatRGB bool, dynamicRange bool) bool {
	if flatRGB || (!pipe.LABChromaEnabled && !pipe.LABHighlightEnabled && !pipe.LABShadowEnabled && !dynamicRange) {
		return false
	}
	return UniqueColors(img) > 6
}

func displayPaletteLABRange(pal [][3]float64) (lo, hi float64) {
	bl := srgbToLAB(paletteRGB01(pal[0]))[0]
	wl := srgbToLAB(paletteRGB01(pal[1]))[0]
	if wl <= bl {
		return 12, 75
	}
	return bl, wl
}

func paletteRGB01(c [3]float64) [3]float64 {
	return [3]float64{c[0] / 255, c[1] / 255, c[2] / 255}
}

// pipelineForPeople records the image's people detection, which steers the
// ray affinity's face handling.
func pipelineForPeople(pipe ColorPipeline, peopleLikely bool) ColorPipeline {
	pipe.PeopleLikely = peopleLikely
	return pipe
}
