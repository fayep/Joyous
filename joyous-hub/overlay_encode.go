package main

import (
	"image"
	"image/color"
)

// encodeInkJoyBinWithOverlay dithers the photo first, draws overlay on the dithered preview,
// then pins overlay hi bytes to P2 inks without LAB processing (lo wipe grid unchanged).
func encodeInkJoyBinWithOverlay(photo image.Image, flatRGB bool, pipe ColorPipeline, flatWipe [][]byte, wipeSelection string, mutate overlayFrameMutator) ([]byte, error) {
	if mutate == nil {
		return encodeInkJoyBinFromRGBA(photo, flatRGB, pipe, flatWipe, wipeSelection)
	}
	if flatRGB {
		composited, err := mutate(photo, true)
		if err != nil {
			return nil, err
		}
		if flatWipe == nil {
			flatWipe = calibrationWipeGrid()
		}
		hi, lo := snapToPaletteWithWipe(composited, flatWipe)
		return ToBin(hi, lo), nil
	}

	indices := StuckiTwoPalette(photo, pipe.InkJoyDisplay, pipe, false, stuckiOptionsInkJoy(pipe))
	hi := indicesToHi(indices)
	lo := resolveWipeGridForEncode(photo, wipeSelection)

	base := renderHiToImage(hi, pipe.InkJoyDisplay)
	composited, err := mutate(base, false)
	if err != nil {
		return nil, err
	}
	pinOverlayPixels(hi, base, composited, pipe.InkJoyDisplay)
	return ToBin(hi, lo), nil
}

func pinOverlayPixels(hi [][]byte, before, after image.Image, display [6][3]float64) {
	b := before.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if rgbaEqual(before.At(x, y), after.At(x, y)) {
				continue
			}
			ar, ag, ab, _ := after.At(x, y).RGBA()
			idx := nearestColor([3]float64{float64(ar >> 8), float64(ag >> 8), float64(ab >> 8)}, display)
			hi[y][x] = hiBytes[idx]
		}
	}
}

func encodeSamsungPNGWithOverlay(photo image.Image, mutate func(image.Image) image.Image, pipe ColorPipeline) ([]byte, error) {
	indices := StuckiTwoPalette(photo, pipe.SamsungDisplay, pipe, false, stuckiOptionsSamsung(pipe))
	base := renderIndicesToRGB(indices, pipe.SamsungSend)
	if mutate == nil {
		return encodePNG(base), nil
	}
	composited := imageToRGBA(mutate(base))
	pinSamsungOverlayPixels(imageToRGBA(base), composited, pipe.SamsungSend)
	return encodePNG(composited), nil
}

func pinSamsungOverlayPixels(before, after *image.RGBA, send [6][3]float64) {
	b := before.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if before.RGBAAt(x, y) == after.RGBAAt(x, y) {
				continue
			}
			c := after.RGBAAt(x, y)
			idx := nearestColor([3]float64{float64(c.R), float64(c.G), float64(c.B)}, send)
			p := send[idx]
			after.SetRGBA(x, y, color.RGBA{R: uint8(p[0]), G: uint8(p[1]), B: uint8(p[2]), A: 255})
		}
	}
}

func rgbaEqual(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
