package main

import (
	"image"
	"image/color"
	"math"
)

// skinToneWeight returns 0–1 for how likely a LAB sample is human skin (Fitzpatrick I–VI).
func skinToneWeight(lab [3]float64) float64 {
	L, a, b := lab[0], lab[1], lab[2]
	if L < 10 || L > 92 {
		return 0
	}
	chroma := math.Hypot(a, b)
	if chroma < 3 {
		return 0
	}
	// Skin hues in a–b plane (~25°–75° from a* axis).
	hue := math.Atan2(b, a)
	if hue < 0.35 || hue > 1.35 {
		return 0
	}
	if a < 1 {
		return 0
	}
	// Accept wide a/b ratios so deep brown and fair pink both match.
	if b < 2 || b/a > 2.8 {
		return 0
	}
	// Dark skin: lower chroma still counts if hue is right.
	chromaW := (chroma - 3) / 28
	if chromaW > 1 {
		chromaW = 1
	}
	if chromaW < 0 {
		chromaW = 0
	}
	// Full weight across typical face L*; taper only at extremes.
	lW := 1.0
	if L < 18 {
		lW = (L - 10) / 8
	} else if L > 85 {
		lW = (92 - L) / 7
	}
	w := chromaW * lW
	if w < 0 {
		return 0
	}
	if w > 1 {
		return 1
	}
	return w
}

// ApplySkinToneProcessing warms skin shadows and preserves depth on people photos.
// Applied after global LAB steps; reduces green in shadow skin without lifting toward white.
func ApplySkinToneProcessing(img image.Image, strength float64) image.Image {
	if strength <= 0 {
		return img
	}
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r8, g8, b8, a8 := img.At(x, y).RGBA()
			rgb := [3]float64{float64(r8>>8) / 255.0, float64(g8>>8) / 255.0, float64(b8>>8) / 255.0}
			lab := srgbToLAB(rgb)
			w := skinToneWeight(lab) * strength
			if w <= 0 {
				out.SetRGBA(x, y, color.RGBA{uint8(r8 >> 8), uint8(g8 >> 8), uint8(b8 >> 8), uint8(a8 >> 8)})
				continue
			}
			L, a, bv := lab[0], lab[1], lab[2]
			origL := L

			// Warm shadows: more red, less green (counteracts sage ink in Stucki mixes).
			if L < 52 {
				t := (52 - L) / 52
				if t > 1 {
					t = 1
				}
				a += w * t * 5.0
				bv -= w * t * 3.5
			}
			// Mid skin: gentle warmth without brightening.
			if L >= 32 && L <= 72 {
				a += w * 1.2
				bv -= w * 0.6
			}
			// Preserve depth on darker skin — resist prior shadow lift / DR flattening.
			if origL < 44 && L > origL+1.5 {
				L = origL + (L-origL)*0.25
			}

			lab[0], lab[1], lab[2] = L, a, bv
			enhanced := labToSRGB(lab)
			out.SetRGBA(x, y, color.RGBA{
				R: floatTo8(enhanced[0]),
				G: floatTo8(enhanced[1]),
				B: floatTo8(enhanced[2]),
				A: uint8(a8 >> 8),
			})
		}
	}
	return out
}

func shadowLiftOnSkin(lab [3]float64, lift float64, portrait bool) float64 {
	if !portrait || lift <= 0 {
		return lift
	}
	sw := skinToneWeight(lab)
	if sw <= 0 {
		return lift
	}
	return lift * (1 - sw*0.9)
}
