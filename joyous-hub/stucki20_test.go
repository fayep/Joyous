package main

import (
	"image"
	"image/color"
	"testing"
)

func TestStucki20MixtureCount(t *testing.T) {
	labs := buildStucki20Labs(PaletteInkJoyDisplay)
	if len(labs) < 120 || len(labs) > 130 {
		t.Fatalf("InkJoy stucki 2×2 mixtures: got %d, want ~126", len(labs))
	}
	sam := buildStucki20Labs(PaletteSamsungDisplay)
	if len(sam) < 120 || len(sam) > 130 {
		t.Fatalf("Samsung stucki 2×2 mixtures: got %d, want ~126", len(sam))
	}
}

func TestStucki20LabsCached(t *testing.T) {
	a := stucki20Labs(PaletteInkJoyDisplay)
	b := stucki20Labs(PaletteInkJoyDisplay)
	if len(a) != len(b) {
		t.Fatal("cache returned different lengths")
	}
}

func TestInkAffinityMixTargetsBrownNotGreenPrimary(t *testing.T) {
	palLabs := displayPaletteLAB(PaletteInkJoyDisplay)
	mixLabs := stucki20Labs(PaletteInkJoyDisplay)

	// Warm brown mid-tone — primary hue affinity leans to sage green; mix should be warmer.
	brown := srgbToLAB([3]float64{95.0 / 255, 58.0 / 255, 42.0 / 255})
	primaryTarget := inkAffinityTargetLAB(brown, palLabs)
	mixTarget := nearestStucki20LAB(brown, mixLabs)

	greenIdx := 5
	if primaryTarget[1] <= palLabs[greenIdx][1]-2 {
		t.Fatalf("primary target not green-leaning: a*=%.1f", primaryTarget[1])
	}
	if mixTarget[1] >= primaryTarget[1] {
		t.Fatalf("mix target should be less green than primary (a* %.1f vs %.1f)", mixTarget[1], primaryTarget[1])
	}

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{95, 58, 42, 255})
	primaryOut := ApplyInkAffinity(img, PaletteInkJoyDisplay, 1.0, false)
	mixOut := ApplyInkAffinityMix(img, PaletteInkJoyDisplay, 1.0, false)
	primaryLab := pixelLAB(primaryOut, 0, 0)
	mixLab := pixelLAB(mixOut, 0, 0)
	if mixLab[1] >= primaryLab[1] {
		t.Fatalf("mix should be less green than primary affinity (a* %.1f vs %.1f)", mixLab[1], primaryLab[1])
	}
}

func TestInkAffinityMixReducedOnSkin(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{180, 120, 95, 255})
	full := ApplyInkAffinityMix(img, PaletteSamsungDisplay, 1.0, true)
	none := ApplyInkAffinityMix(img, PaletteSamsungDisplay, 1.0, false)
	if labDeltaE76(pixelLAB(full, 0, 0), pixelLAB(img, 0, 0)) >= labDeltaE76(pixelLAB(none, 0, 0), pixelLAB(img, 0, 0)) {
		t.Fatal("portrait should reduce mix affinity on skin")
	}
}

func pixelLAB(img image.Image, x, y int) [3]float64 {
	r8, g8, b8, _ := img.At(x, y).RGBA()
	return srgbToLAB([3]float64{float64(r8>>8) / 255, float64(g8>>8) / 255, float64(b8>>8) / 255})
}
