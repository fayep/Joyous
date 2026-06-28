package main

import (
	"image"
)

// peopleDetectVersion bumps when the heuristic changes so stored meta is re-analyzed.
const peopleDetectVersion = 2

// detectPeopleLikely uses LAB skin scoring with landscape rejection. Hint only — not face detection.
func detectPeopleLikely(img image.Image) bool {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 24 || h < 24 {
		return false
	}

	long := w
	if h > long {
		long = h
	}
	step := long / 128
	if step < 1 {
		step = 1
	}

	var skinScore, vegScore, total, centerSkin, centerTotal float64
	cx, cy := float64(w)*0.5, float64(h)*0.5
	maxR := float64(w) * 0.35
	if float64(h)*0.35 < maxR {
		maxR = float64(h) * 0.35
	}
	centerR2 := maxR * maxR

	for y := 0; y < h; y += step {
		for x := 0; x < w; x += step {
			dx := float64(x) - cx
			dy := float64(y) - cy
			dist2 := dx*dx + dy*dy
			weight := 1.0
			if dist2 < centerR2 {
				weight = 3.0
			} else if dist2 > (maxR*1.4)*(maxR*1.4) {
				continue
			}
			total += weight

			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(bl>>8)
			if isVegetationDominant(r8, g8, b8) {
				vegScore += weight
			}
			if !rgbCompatibleWithSkin(r8, g8, b8) {
				continue
			}
			rgb := [3]float64{float64(r8) / 255, float64(g8) / 255, float64(b8) / 255}
			sw := skinToneWeight(srgbToLAB(rgb))
			if sw < 0.45 {
				continue
			}
			skinScore += weight * sw
			if dist2 < centerR2 {
				centerSkin += weight * sw
				centerTotal += weight
			}
		}
	}
	if total == 0 {
		return false
	}
	if vegScore/total > 0.30 {
		return false
	}
	if skinScore/total < 0.055 {
		return false
	}
	if centerTotal > 0 && centerSkin/centerTotal < 0.04 {
		return false
	}
	return true
}

func isVegetationDominant(r, g, b uint8) bool {
	return g > r+12 && g > b+12 && g > 55
}

// rgbCompatibleWithSkin rejects yellow foliage and grey neutrals that overlap LAB skin hue.
func rgbCompatibleWithSkin(r, g, b uint8) bool {
	maxC := r
	if g > maxC {
		maxC = g
	}
	if b > maxC {
		maxC = b
	}
	minC := r
	if g < minC {
		minC = g
	}
	if b < minC {
		minC = b
	}
	if maxC-minC < 18 {
		return false
	}
	if g > r+10 && g > b+8 {
		return false
	}
	// Strong yellow (wheat, autumn leaves) without skin-like red/blue balance.
	if r > 90 && g > 70 && b < 70 && b < r-25 && g >= r-25 {
		return false
	}
	return int(r)+int(g) > int(b)+20
}
