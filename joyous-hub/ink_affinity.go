package main

import (
	"image"
	"image/color"
	"math"
)

const (
	inkAffinityMaxDeltaE   = 10.0
	inkAffinityMinChromaW  = 0.12 // still nudge neutrals slightly toward nearest ink
	inkAffinityChromaScale = 35.0 // L*a*b* C* at which full chroma weight applies
)

// ApplyInkAffinity nudges each pixel's hue toward the nearest P2 ink in LAB space,
// keeping L* mostly intact so the scene reads as the same lighting with cleaner dither.
func ApplyInkAffinity(img image.Image, displayPalette [6][3]float64, strength float64, portraitEnhance bool) image.Image {
	if strength <= 0 {
		return img
	}
	palLabs := displayPaletteLAB(displayPalette)
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r8, g8, b8, a8 := img.At(x, y).RGBA()
			rgb := [3]float64{float64(r8>>8) / 255.0, float64(g8>>8) / 255.0, float64(b8>>8) / 255.0}
			lab := srgbToLAB(rgb)
			inkLab := inkAffinityTargetLAB(lab, palLabs)

			t := inkAffinityWeight(lab, strength, portraitEnhance)
			newLab := lab
			newLab[1] = lab[1] + t*(inkLab[1]-lab[1])
			newLab[2] = lab[2] + t*(inkLab[2]-lab[2])
			newLab = capInkAffinityShift(lab, newLab, inkAffinityMaxDeltaE*strength)

			mapped := labToSRGB(newLab)
			out.SetRGBA(x, y, color.RGBA{
				R: floatTo8(mapped[0]),
				G: floatTo8(mapped[1]),
				B: floatTo8(mapped[2]),
				A: uint8(a8 >> 8),
			})
		}
	}
	return out
}

func displayPaletteLAB(pal [6][3]float64) [6][3]float64 {
	var labs [6][3]float64
	for i := range labs {
		labs[i] = srgbToLAB(paletteRGB01(pal[i]))
	}
	return labs
}

func nearestInkLAB(lab [3]float64, palLabs [6][3]float64) int {
	best := 0
	bestD := math.MaxFloat64
	for i, p := range palLabs {
		if d := labDeltaE76(lab, p); d < bestD {
			bestD = d
			best = i
		}
	}
	return best
}

// inkAffinityTargetLAB picks the P2 ink hue to lean toward. Low-chroma pixels use
// full ΔE (black/white); saturated pixels match hue among chromatic inks.
func inkAffinityTargetLAB(lab [3]float64, palLabs [6][3]float64) [3]float64 {
	chroma := math.Hypot(lab[1], lab[2])
	if chroma < 8 {
		return palLabs[nearestInkLAB(lab, palLabs)]
	}
	return palLabs[nearestChromaticInkHue(lab, palLabs)]
}

func nearestChromaticInkHue(lab [3]float64, palLabs [6][3]float64) int {
	hue := math.Atan2(lab[2], lab[1])
	best := 0
	bestD := math.MaxFloat64
	for i, p := range palLabs {
		pc := math.Hypot(p[1], p[2])
		if pc < 6 {
			continue
		}
		d := math.Abs(angleDiff(hue, math.Atan2(p[2], p[1])))
		if d < bestD {
			bestD = d
			best = i
		}
	}
	if bestD == math.MaxFloat64 {
		return nearestInkLAB(lab, palLabs)
	}
	return best
}

func angleDiff(a, b float64) float64 {
	d := a - b
	for d > math.Pi {
		d -= 2 * math.Pi
	}
	for d < -math.Pi {
		d += 2 * math.Pi
	}
	return d
}

func labDeltaE76(a, b [3]float64) float64 {
	da := a[0] - b[0]
	db := a[1] - b[1]
	dc := a[2] - b[2]
	return math.Sqrt(da*da + db*db + dc*dc)
}

func inkAffinityWeight(lab [3]float64, strength float64, portraitEnhance bool) float64 {
	if strength <= 0 {
		return 0
	}
	chroma := math.Hypot(lab[1], lab[2])
	chromaW := chroma / inkAffinityChromaScale
	if chromaW > 1 {
		chromaW = 1
	}
	w := inkAffinityMinChromaW + (1-inkAffinityMinChromaW)*chromaW
	t := strength * w
	if portraitEnhance {
		if sw := skinToneWeight(lab); sw > 0 {
			t *= 1 - 0.75*sw
		}
	}
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return t
}

func capInkAffinityShift(before, after [3]float64, maxDE float64) [3]float64 {
	if maxDE <= 0 {
		return before
	}
	de := labDeltaE76(before, after)
	if de <= maxDE || de == 0 {
		return after
	}
	scale := maxDE / de
	return [3]float64{
		before[0] + scale*(after[0]-before[0]),
		before[1] + scale*(after[1]-before[1]),
		before[2] + scale*(after[2]-before[2]),
	}
}

func shouldApplyInkAffinity(pipe ColorPipeline, img image.Image, flatRGB bool) bool {
	if flatRGB || !pipe.LABInkAffinityEnabled || pipe.LABInkAffinityStrength <= 0 {
		return false
	}
	return UniqueColors(img) > 6
}
