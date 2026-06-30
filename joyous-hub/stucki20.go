package main

import (
	"fmt"
	"image"
	"image/color"
	"sync"
)

var stucki20LabCache sync.Map // paletteCacheKey → [][3]float64

// stucki20Labs returns unique LAB colors achievable as the average of four P2 inks
// in a 2×2 Stucki tile (6^4 tuples, deduped — 126 multisets for six inks).
func stucki20Labs(display [6][3]float64) [][3]float64 {
	key := paletteCacheKey(display)
	if v, ok := stucki20LabCache.Load(key); ok {
		return v.([][3]float64)
	}
	labs := buildStucki20Labs(display)
	stucki20LabCache.Store(key, labs)
	return labs
}

func buildStucki20Labs(display [6][3]float64) [][3]float64 {
	seen := make(map[[3]uint32]struct{})
	var labs [][3]float64
	for i := 0; i < 6; i++ {
		for j := 0; j < 6; j++ {
			for k := 0; k < 6; k++ {
				for l := 0; l < 6; l++ {
					rgb := averagePaletteRGB(display[i], display[j], display[k], display[l])
					key := quantizeRGB255(rgb)
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					labs = append(labs, srgbToLAB(paletteRGB01(rgb)))
				}
			}
		}
	}
	return labs
}

func averagePaletteRGB(a, b, c, d [3]float64) [3]float64 {
	return [3]float64{
		(a[0] + b[0] + c[0] + d[0]) / 4,
		(a[1] + b[1] + c[1] + d[1]) / 4,
		(a[2] + b[2] + c[2] + d[2]) / 4,
	}
}

func quantizeRGB255(rgb [3]float64) [3]uint32 {
	return [3]uint32{
		uint32(rgb[0] + 0.5),
		uint32(rgb[1] + 0.5),
		uint32(rgb[2] + 0.5),
	}
}

func paletteCacheKey(pal [6][3]float64) string {
	return fmt.Sprintf("%.0f,%.0f,%.0f|%.0f,%.0f,%.0f|%.0f,%.0f,%.0f|%.0f,%.0f,%.0f|%.0f,%.0f,%.0f|%.0f,%.0f,%.0f",
		pal[0][0], pal[0][1], pal[0][2],
		pal[1][0], pal[1][1], pal[1][2],
		pal[2][0], pal[2][1], pal[2][2],
		pal[3][0], pal[3][1], pal[3][2],
		pal[4][0], pal[4][1], pal[4][2],
		pal[5][0], pal[5][1], pal[5][2],
	)
}

func nearestStucki20LAB(lab [3]float64, mixLabs [][3]float64) [3]float64 {
	if len(mixLabs) == 0 {
		return lab
	}
	best := mixLabs[0]
	bestD := labDeltaE76(lab, best)
	for _, m := range mixLabs[1:] {
		if d := labDeltaE76(lab, m); d < bestD {
			bestD = d
			best = m
		}
	}
	return best
}

// ApplyInkAffinityMix nudges hues toward the nearest 2×2 Stucki mixture (four-ink average)
// instead of a single primary — better for browns and mid-tones Stucki will actually render.
func ApplyInkAffinityMix(img image.Image, displayPalette [6][3]float64, strength float64, portraitEnhance bool) image.Image {
	if strength <= 0 {
		return img
	}
	mixLabs := stucki20Labs(displayPalette)
	return applyInkAffinityToward(img, func(lab [3]float64) [3]float64 {
		return nearestStucki20LAB(lab, mixLabs)
	}, strength, portraitEnhance)
}

func applyInkAffinityToward(img image.Image, target func([3]float64) [3]float64, strength float64, portraitEnhance bool) image.Image {
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r8, g8, b8, a8 := img.At(x, y).RGBA()
			rgb := [3]float64{float64(r8>>8) / 255.0, float64(g8>>8) / 255.0, float64(b8>>8) / 255.0}
			lab := srgbToLAB(rgb)
			inkLab := target(lab)

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

func shouldApplyInkAffinityMix(pipe ColorPipeline, img image.Image, flatRGB bool) bool {
	if flatRGB || !pipe.LABInkAffinityMixEnabled || pipe.LABInkAffinityMixStrength <= 0 {
		return false
	}
	return UniqueColors(img) > 6
}
