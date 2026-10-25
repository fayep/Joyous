package main

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestInkAffinityRayPullsDarkSkinOffGreen(t *testing.T) {
	// Dad-like ruddy shadow — primary/mix affinity often lands on 3R1G.
	img := solidRGB(90, 55, 40)
	src := srgbToLAB(pixelRGB01(img.At(0, 0)))
	out := ApplyInkAffinityRay(img, PaletteInkJoyDisplay[:], 1.0, false)
	after := srgbToLAB(pixelRGB01(out.At(0, 0)))
	green := srgbToLAB(paletteRGB01(PaletteInkJoyDisplay[5]))
	if labDeltaE76(after, green) < labDeltaE76(src, green)-1 {
		t.Fatalf("moved toward green ink, src ΔE=%.2f after ΔE=%.2f",
			labDeltaE76(src, green), labDeltaE76(after, green))
	}
	if math.Abs(after[0]-src[0]) > 4 {
		t.Fatalf("L* should stay put: src=%.1f after=%.1f", src[0], after[0])
	}
}

func TestInkAffinityRayKeepsSunlitSkinChroma(t *testing.T) {
	// Mum's sunlit cheek — old R–W project landed on 3W1R (C* ~5) and blew white.
	img := solidRGB(220, 185, 165)
	src := srgbToLAB(pixelRGB01(img.At(0, 0)))
	after := inkAffinityRayTargetLAB(src, displayPaletteLAB(PaletteInkJoyDisplay[:]), defaultRayLGuide(), rayRoleFace)
	srcC := math.Hypot(src[1], src[2])
	afterC := math.Hypot(after[1], after[2])
	if afterC < 14 {
		t.Fatalf("sunlit skin chroma collapsed (white blotch): src C*=%.1f after C*=%.1f L*=%.1f", srcC, afterC, after[0])
	}
	if afterC+1 < srcC*0.7 {
		t.Fatalf("lost too much cheek chroma: src C*=%.1f after C*=%.1f", srcC, afterC)
	}
	if after[0] > src[0]-6 {
		t.Fatalf("expected highlight squash, src L*=%.1f after L*=%.1f", src[0], after[0])
	}
}

func TestInkAffinityRaySquashesCoolForehead(t *testing.T) {
	lab := [3]float64{83, 3.3, -7.8}
	guide := rayLGuide{skinLo: 26, skinMode: 36, skinHi: 46}
	target := inkAffinityRayTargetLAB(lab, displayPaletteLAB(PaletteInkJoyDisplay[:]), guide, rayRoleFace)
	if target[0] >= lab[0]-8 {
		t.Fatalf("highlight tail should ease down, got L*=%.1f", target[0])
	}
	if math.Abs(target[0]-guide.skinHi) < 0.2 {
		t.Fatalf("hard clip flattens the face into a mask, got L*=%.1f hi=%.0f", target[0], guide.skinHi)
	}
}

func TestInkAffinityRayMergesHighlightTailToSkinMode(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 24, 16))
	dark := color.RGBA{90, 55, 40, 255}
	bright := color.RGBA{220, 185, 165, 255}
	for y := 0; y < 16; y++ {
		for x := 0; x < 24; x++ {
			if x >= 9 && x < 15 && y >= 4 && y < 12 {
				img.Set(x, y, bright)
			} else {
				img.Set(x, y, dark)
			}
		}
	}
	field := buildRayField(img)
	guide := buildRayLGuide(img, field, buildRayFaceMask(img, field))
	if guide.skinMode > 50 {
		t.Fatalf("mode should follow the dark face mass, got %.0f", guide.skinMode)
	}
	out := ApplyInkAffinityRay(img, PaletteInkJoyDisplay[:], 1.0, false)
	after := srgbToLAB(pixelRGB01(out.At(12, 8)))
	brightSrc := srgbToLAB(pixelRGB01(img.At(12, 8)))
	if after[0] >= brightSrc[0]-4 {
		t.Fatalf("bright tail should ease down, src=%.1f after=%.1f", brightSrc[0], after[0])
	}
	darkAfter := srgbToLAB(pixelRGB01(out.At(2, 8)))
	if after[0] <= darkAfter[0]+8 {
		t.Fatalf("highlight and shadow collapsed (mask): dark=%.1f bright=%.1f", darkAfter[0], after[0])
	}
}

func TestInkAffinityRayCoolFaceShadowIsNotBlueRay(t *testing.T) {
	// Measured on Mum & Dad: cool forehead / shadow, a* still warm.
	cases := [][3]float64{
		{74, 3.2, -9.2}, // Mum sunlit forehead + sky bounce
		{55, 3.6, -7.3}, // Dad shadow side
		{83, 3.3, -7.8},
	}
	for _, lab := range cases {
		if isPaleSkyLAB(lab) {
			t.Fatalf("cool face classified as sky: %v", lab)
		}
		if !isSkinRayLAB(lab) {
			t.Fatalf("cool face should be skin: %v", lab)
		}
		target := inkAffinityRayTargetLAB(lab, displayPaletteLAB(PaletteInkJoyDisplay[:]), defaultRayLGuide(), rayRoleFace)
		if lab[0] > 58 {
			if target[0] >= lab[0]-6 {
				t.Fatalf("lit half should come down, lab=%v target=%v", lab, target)
			}
			if target[2] < 0 {
				t.Fatalf("lit half went cool/blue: lab=%v target=%v", lab, target)
			}
			continue
		}
		if math.Hypot(target[1]-lab[1], target[2]-lab[2]) > 2 {
			t.Fatalf("shadow half chroma should stay, lab=%v target=%v", lab, target)
		}
	}
}

func TestInkAffinityRayDaylightFaceIsNotSky(t *testing.T) {
	// Outdoor forehead with sky fill: high L*, a* still warm, b* slightly cool.
	img := solidRGB(228, 198, 190)
	src := srgbToLAB(pixelRGB01(img.At(0, 0)))
	if isPaleSkyLAB(src) {
		t.Fatalf("daylight face classified as pale sky: lab=%v", src)
	}
	if !isSkinRayLAB(src) {
		t.Fatalf("daylight face should be skin: lab=%v", src)
	}
	after := inkAffinityRayTargetLAB(src, displayPaletteLAB(PaletteInkJoyDisplay[:]), defaultRayLGuide(), rayRoleFace)
	if after[2] < 0 {
		t.Fatalf("face went cool/blue: src=%v after=%v", src, after)
	}
	if after[0] >= src[0]-4 {
		t.Fatalf("expected highlight squash, src L*=%.1f after L*=%.1f", src[0], after[0])
	}
}

func TestInkAffinityRayLeavesPaleSkyAlone(t *testing.T) {
	// Haze is already high-L white. Dusting it blue is what painted the
	// sunlit half of a face when that LAB leaked off the mask.
	img := solidRGB(230, 236, 242)
	src := srgbToLAB(pixelRGB01(img.At(0, 0)))
	if !isPaleSkyLAB(src) {
		t.Fatalf("fixture should be pale sky, lab=%v", src)
	}
	out := ApplyInkAffinityRay(img, PaletteInkJoyDisplay[:], 1.0, false)
	after := srgbToLAB(pixelRGB01(out.At(0, 0)))
	if after[2] < src[2]-1 {
		t.Fatalf("must not blue haze (or a leaked forehead): src b*=%.2f after=%.2f", src[2], after[2])
	}
	if math.Abs(after[0]-src[0]) > 4 {
		t.Fatalf("L* should stay put: src=%.1f after=%.1f", src[0], after[0])
	}
}

func TestInkAffinityRayLeavesFoliageGreen(t *testing.T) {
	img := solidRGB(70, 110, 55)
	src := srgbToLAB(pixelRGB01(img.At(0, 0)))
	if !isFoliageLAB(src) {
		t.Fatalf("fixture should be foliage, lab=%v", src)
	}
	out := ApplyInkAffinityRay(img, PaletteInkJoyDisplay[:], 1.0, false)
	after := srgbToLAB(pixelRGB01(out.At(0, 0)))
	if after[1] >= 0 {
		t.Fatalf("foliage should stay on the green side of a*, got %.2f", after[1])
	}
}

func TestInkAffinityRayFairLowAMergesInFaceField(t *testing.T) {
	// Measured on Mum & Dad: sunlit cheek in a face cell, a* below the global gate.
	lab := [3]float64{63, 1.5, 4.1}
	if isPaleSkyLAB(lab) {
		t.Fatal("fair cheek must not be pale sky")
	}
	if !isFaceSkinLAB(lab) {
		t.Fatal("face-field fair cheek should be skin")
	}
	guide := rayLGuide{skinLo: 25, skinMode: 35, skinHi: 45}
	target := inkAffinityRayTargetLAB(lab, displayPaletteLAB(PaletteInkJoyDisplay[:]), guide, rayRoleFace)
	if target[0] > 62 {
		t.Fatalf("fair cheek should land in the flesh band, got L*=%.1f", target[0])
	}
	if target[0] < 40 {
		t.Fatalf("fair cheek crushed into the sockets, got L*=%.1f", target[0])
	}
	if target[2] < 0 {
		t.Fatalf("must not sky-dust a face-field cheek: %v", target)
	}
}

func TestInkAffinityRayPreservesFaceShading(t *testing.T) {
	guide := rayLGuide{skinLo: 25, skinMode: 35, skinHi: 47}
	pal := displayPaletteLAB(PaletteInkJoyDisplay[:])
	shadow := inkAffinityRayTargetLAB([3]float64{28, 18, 16}, pal, guide, rayRoleFace)
	lit := inkAffinityRayTargetLAB([3]float64{78, 8, 10}, pal, guide, rayRoleFace)
	if lit[0]-shadow[0] < 8 {
		t.Fatalf("face shading collapsed (Phantom mask): shadow L*=%.1f lit L*=%.1f", shadow[0], lit[0])
	}
	if lit[0] > 70 {
		t.Fatalf("lit cheek still in the white-paint band: L*=%.1f", lit[0])
	}
	if s := labStops(shadow[0], lit[0]); s < 1.5 || s > 5.5 {
		t.Fatalf("face shading should be a few stops, not a cliff: %.1f stops (shadow L*=%.1f lit L*=%.1f)", s, shadow[0], lit[0])
	}
}

func TestInkAffinityRayFaceRangeIsThreeToFiveStops(t *testing.T) {
	guide := rayLGuide{skinLo: 26, skinMode: 40, skinHi: 50}
	lo := mergeSkinL(22, guide)
	hi := mergeSkinL(92, guide)
	s := labStops(lo, hi)
	if s < 2.0 || s > 5.2 {
		t.Fatalf("face map should be a few stops, got %.2f (L* %.1f–%.1f)", s, lo, hi)
	}
	if hi > 72 {
		t.Fatalf("highlight end still in chalk-white: L*=%.1f", hi)
	}
	if mergeSkinL(50, guide) <= mergeSkinL(40, guide) {
		t.Fatal("rank order must be kept (smooth, not a step)")
	}
}

func TestInkAffinityRaySkyBesideHeadKeepsL(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 80, 60))
	sky := color.RGBA{230, 236, 242, 255}
	face := color.RGBA{90, 55, 40, 255}
	forehead := color.RGBA{220, 185, 165, 255}
	ground := color.RGBA{70, 90, 50, 255}
	for y := 0; y < 60; y++ {
		for x := 0; x < 80; x++ {
			switch {
			case y < 28:
				img.Set(x, y, sky)
			case y < 36 && x >= 30 && x < 50:
				img.Set(x, y, forehead)
			case y >= 36 && x >= 28 && x < 52:
				img.Set(x, y, face)
			default:
				img.Set(x, y, ground)
			}
		}
	}
	out := ApplyInkAffinityRay(img, PaletteInkJoyDisplay[:], 1.0, false)
	src := srgbToLAB(pixelRGB01(img.At(8, 18)))
	after := srgbToLAB(pixelRGB01(out.At(8, 18)))
	if after[0] < src[0]-6 {
		t.Fatalf("sky beside the head became a face mask: src L*=%.1f after=%.1f", src[0], after[0])
	}
}

func TestInkAffinityRayCloudToneIsNotFace(t *testing.T) {
	// Same RGB as the sunlit-cheek fixture, but a sky-sized field — not a face.
	img := image.NewRGBA(image.Rect(0, 0, 80, 60))
	cloud := color.RGBA{220, 185, 165, 255}
	for y := 0; y < 60; y++ {
		for x := 0; x < 80; x++ {
			img.Set(x, y, cloud)
		}
	}
	src := srgbToLAB(pixelRGB01(img.At(40, 30)))
	if buildRayField(img).at(40, 30) != rayRoleSky {
		t.Fatal("warm cloud field should be sky, not face")
	}
	out := ApplyInkAffinityRay(img, PaletteInkJoyDisplay[:], 1.0, false)
	after := srgbToLAB(pixelRGB01(out.At(40, 30)))
	if after[0] < src[0]-6 {
		t.Fatalf("cloud field must keep L* (not face-merge): src=%.1f after=%.1f", src[0], after[0])
	}
}

func TestInkAffinityRayFaceHighlightNotCloud(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 80, 60))
	sky := color.RGBA{230, 236, 242, 255}
	face := color.RGBA{90, 55, 40, 255}
	forehead := color.RGBA{220, 185, 165, 255}
	ground := color.RGBA{70, 90, 50, 255}
	for y := 0; y < 60; y++ {
		for x := 0; x < 80; x++ {
			switch {
			case y < 28:
				img.Set(x, y, sky)
			case y < 36 && x >= 30 && x < 50:
				img.Set(x, y, forehead)
			case y >= 36 && x >= 28 && x < 52:
				img.Set(x, y, face)
			default:
				img.Set(x, y, ground)
			}
		}
	}
	field := buildRayField(img)
	if field.at(40, 10) != rayRoleSky {
		t.Fatalf("upper haze should be sky, got %d", field.at(40, 10))
	}
	if field.at(40, 48) != rayRoleFace {
		t.Fatalf("midtone cluster should be face, got %d", field.at(40, 48))
	}
	if field.at(40, 32) != rayRoleFace {
		t.Fatalf("forehead touching the face should be face, got %d", field.at(40, 32))
	}
	out := ApplyInkAffinityRay(img, PaletteInkJoyDisplay[:], 1.0, false)
	skyAfter := srgbToLAB(pixelRGB01(out.At(40, 10)))
	skySrc := srgbToLAB(pixelRGB01(img.At(40, 10)))
	if math.Abs(skyAfter[0]-skySrc[0]) > 4 {
		t.Fatalf("sky L* should stay: src=%.1f after=%.1f", skySrc[0], skyAfter[0])
	}
	foreAfter := srgbToLAB(pixelRGB01(out.At(40, 32)))
	foreSrc := srgbToLAB(pixelRGB01(img.At(40, 32)))
	if foreAfter[0] > foreSrc[0]-6 {
		t.Fatalf("face highlight should merge, src L*=%.1f after=%.1f", foreSrc[0], foreAfter[0])
	}
}

func TestInkAffinityRayMaskKeepsJacketOutAndForeheadIn(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 80, 80))
	sky := color.RGBA{230, 236, 242, 255}
	forehead := color.RGBA{228, 198, 190, 255} // cool sunlit chalk
	face := color.RGBA{90, 55, 40, 255}
	jacket := color.RGBA{176, 178, 182, 255}
	for y := 0; y < 80; y++ {
		for x := 0; x < 80; x++ {
			switch {
			case y < 22:
				img.Set(x, y, sky)
			case y < 34 && x >= 28 && x < 52:
				img.Set(x, y, forehead)
			case y < 50 && x >= 26 && x < 54:
				img.Set(x, y, face)
			case y >= 50 && x >= 18 && x < 62:
				img.Set(x, y, jacket)
			default:
				img.Set(x, y, sky)
			}
		}
	}
	field := buildRayField(img)
	mask := buildRayFaceMask(img, field)
	if !mask.at(40, 28) {
		t.Fatal("sunlit forehead must be in the face mask")
	}
	if mask.at(40, 62) {
		t.Fatal("grey jacket must stay out of the face mask")
	}
	out := ApplyInkAffinityRay(img, PaletteInkJoyDisplay[:], 1.0, false)
	foreSrc := srgbToLAB(pixelRGB01(img.At(40, 28)))
	foreAfter := srgbToLAB(pixelRGB01(out.At(40, 28)))
	if foreAfter[0] > foreSrc[0]-8 {
		t.Fatalf("forehead still chalk: src L*=%.1f after=%.1f", foreSrc[0], foreAfter[0])
	}
	if foreAfter[2] < foreSrc[2]-1 {
		t.Fatalf("lit forehead turned blue: src b*=%.1f after=%.1f", foreSrc[2], foreAfter[2])
	}
	srcC := math.Hypot(foreSrc[1], foreSrc[2])
	afterC := math.Hypot(foreAfter[1], foreAfter[2])
	if afterC > srcC+4 {
		t.Fatalf("lit forehead chroma was amped: src C*=%.1f after=%.1f", srcC, afterC)
	}
	jSrc := srgbToLAB(pixelRGB01(img.At(40, 62)))
	jAfter := srgbToLAB(pixelRGB01(out.At(40, 62)))
	if math.Hypot(jAfter[1]-jSrc[1], jAfter[2]-jSrc[2]) > 6 {
		t.Fatalf("jacket was recolored as skin: src=%v after=%v", jSrc, jAfter)
	}
}

func TestInkAffinityRayPreservesNeutralGrey(t *testing.T) {
	img := solidRGB(128, 128, 128)
	out := ApplyInkAffinityRay(img, PaletteInkJoyDisplay[:], 1.0, false)
	r8, g8, b8, _ := out.At(0, 0).RGBA()
	if abs(int(r8>>8)-128) > 10 || abs(int(g8>>8)-128) > 10 || abs(int(b8>>8)-128) > 10 {
		t.Fatalf("grey shifted too far: %d,%d,%d", r8>>8, g8>>8, b8>>8)
	}
}

func TestShouldApplyInkAffinityRay(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 20), uint8(y * 20), 128, 255})
		}
	}
	pipe := ColorPipeline{LABInkAffinityRayEnabled: true, LABInkAffinityRayStrength: 1, PeopleLikely: true}
	if !shouldApplyInkAffinityRay(pipe, img, false) {
		t.Fatal("expected ray on for multi-color image")
	}
	pipe.PeopleLikely = false
	if shouldApplyInkAffinityRay(pipe, img, false) {
		t.Fatal("expected off when no people were detected")
	}
	pipe.PeopleLikely = true
	if shouldApplyInkAffinityRay(pipe, img, true) {
		t.Fatal("expected off for flat RGB")
	}
	pipe.LABInkAffinityRayEnabled = false
	if shouldApplyInkAffinityRay(pipe, img, false) {
		t.Fatal("expected off when disabled")
	}
}

func solidRGB(r, g, b uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: r, G: g, B: b, A: 255})
	return img
}
