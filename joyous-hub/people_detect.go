package main

import (
	"image"
)

// detectPeopleLikely uses a lightweight skin-tone heuristic on a downsampled,
// center-weighted sample. It is a hint only — not face detection.
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

	skinWeighted := 0
	totalWeighted := 0
	cx, cy := float64(w)*0.5, float64(h)*0.5
	maxR := float64(w) * 0.35
	if float64(h)*0.35 < maxR {
		maxR = float64(h) * 0.35
	}

	for y := 0; y < h; y += step {
		for x := 0; x < w; x += step {
			dx := float64(x) - cx
			dy := float64(y) - cy
			dist := dx*dx + dy*dy
			weight := 1
			if dist < maxR*maxR {
				weight = 3
			} else if dist > (maxR*1.4)*(maxR*1.4) {
				continue
			}
			totalWeighted += weight
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			if isSkinTone(uint8(r>>8), uint8(g>>8), uint8(bl>>8)) {
				skinWeighted += weight
			}
		}
	}
	if totalWeighted == 0 {
		return false
	}
	return float64(skinWeighted)/float64(totalWeighted) > 0.018
}

func isSkinTone(r, g, b uint8) bool {
	if r > 95 && g > 40 && b > 20 {
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
		if maxC-minC > 15 && absInt(int(r)-int(g)) > 15 && r > g && r > b {
			return true
		}
	}
	_, cb, cr := rgbToYCbCr(r, g, b)
	return cb >= 77 && cb <= 127 && cr >= 133 && cr <= 173
}

func rgbToYCbCr(r, g, b uint8) (y, cb, cr uint8) {
	yf := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	cbf := -0.169*float64(r) - 0.331*float64(g) + 0.500*float64(b) + 128
	crf := 0.500*float64(r) - 0.419*float64(g) - 0.081*float64(b) + 128
	return uint8(yf + 0.5), uint8(cbf + 0.5), uint8(crf + 0.5)
}
