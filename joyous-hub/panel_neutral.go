package main

import "image"

// adaptToPanelNeutral moves the image's neutral axis onto the panel's own
// black..white axis, the way the eye adapts to paper white. Without it the
// dither treats the panel white's tint as an error to cancel: the Samsung's
// white is slightly blue (b*≈-6) and its yellow nearly as light (L*73 vs 75),
// so DR-fitted highlights, which land at white's L*, picked up yellow dots for
// free and neutral clouds rendered as pale goldenrod patches.
func adaptToPanelNeutral(img image.Image, displayPalette [][3]float64) image.Image {
	if len(displayPalette) < 2 {
		return img
	}
	black := srgbToLAB(unitRGB(displayPalette[0]))
	white := srgbToLAB(unitRGB(displayPalette[1]))
	span := white[0] - black[0]
	if span <= 0 {
		return img
	}
	return labMapImage(img, func(_, _ int, lab *[3]float64) bool {
		t := (lab[0] - black[0]) / span
		t = max(0, min(1, t))
		lab[1] += black[1] + t*(white[1]-black[1])
		lab[2] += black[2] + t*(white[2]-black[2])
		return true
	})
}

func unitRGB(rgb255 [3]float64) [3]float64 {
	return [3]float64{rgb255[0] / 255, rgb255[1] / 255, rgb255[2] / 255}
}
