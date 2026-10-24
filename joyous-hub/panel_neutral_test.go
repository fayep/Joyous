package main

import (
	"image"
	"image/color"
	"testing"
)

// A neutral highlight at the Samsung's white L* must not be dithered with
// yellow to cancel the panel white's blue tint.
func TestPanelNeutralKeepsYellowOutOfWhites(t *testing.T) {
	pal := PaletteSamsungDisplay[:]
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{R: 186, G: 186, B: 186, A: 255})
		}
	}
	countYellow := func(src image.Image) int {
		n := 0
		for _, row := range StuckiDither(src, pal, stuckiOptions{Serpentine: true}) {
			for _, v := range row {
				if v == 2 {
					n++
				}
			}
		}
		return n
	}
	if before := countYellow(img); before == 0 {
		t.Fatal("fixture should show the yellow cast without adaptation")
	}
	if after := countYellow(adaptToPanelNeutral(img, pal)); after != 0 {
		t.Fatalf("adapted neutral still dithered %d yellow pixels", after)
	}
}
