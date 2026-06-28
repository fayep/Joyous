package main

import (
	"image"
	_ "image/jpeg"
	"math"
	"os"
	"testing"
)

func TestSnowdonAnalyzeRegions(t *testing.T) {
	path := os.Getenv("HOME") + "/Downloads/Snowdon.jpg"
	f, err := os.Open(path)
	if err != nil {
		t.Skip(path)
	}
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	regions := map[string]func(x, y int) bool{
		"sky": func(x, y int) bool { return y < h/3 },
		"snow": func(x, y int) bool {
			return y >= h/4 && y < h/2 && x > w/3 && x < 2*w/3
		},
		"reflection": func(x, y int) bool {
			return y >= int(0.55*float64(h)) && y < int(0.75*float64(h)) && x > w/4 && x < 3*w/4
		},
	}
	for name, fn := range regions {
		var sumL, sumA, sumB, sumC, sumW float64
		n := 0
		const step = 8
		for y := b.Min.Y; y < b.Max.Y; y += step {
			for x := b.Min.X; x < b.Max.X; x += step {
				if !fn(x-b.Min.X, y-b.Min.Y) {
					continue
				}
				r8, g8, b8, _ := img.At(x, y).RGBA()
				lab := srgbToLAB([3]float64{float64(r8>>8) / 255, float64(g8>>8) / 255, float64(b8>>8) / 255})
				sumL += lab[0]
				sumA += lab[1]
				sumB += lab[2]
				sumC += mathHypot(lab[1], lab[2])
				sumW += skyBlueWeight(lab)
				n++
			}
		}
		fnF := float64(n)
		t.Logf("%s: L=%.1f a*=%.1f b*=%.1f C*=%.1f blueWeight=%.2f",
			name, sumL/fnF, sumA/fnF, sumB/fnF, sumC/fnF, sumW/fnF)
	}
}

func mathHypot(a, b float64) float64 {
	return math.Sqrt(a*a + b*b)
}

func TestApplyLABHighlightTone(t *testing.T) {
	skyLab := srgbToLAB([3]float64{151.0 / 255, 182.0 / 255, 240.0 / 255})
	snowLab := srgbToLAB([3]float64{245.0 / 255, 248.0 / 255, 252.0 / 255})
	reflLab := srgbToLAB([3]float64{82.0 / 255, 105.0 / 255, 147.0 / 255})

	sky := applyLABHighlightTone(skyLab, 1)
	snow := applyLABHighlightTone(snowLab, 1)
	refl := applyLABHighlightTone(reflLab, 1)

	if sky >= skyLab[0]-2 {
		t.Fatalf("sky should be compressed: got L=%.1f from %.1f", sky, skyLab[0])
	}
	if snow < snowLab[0]-3 {
		t.Fatalf("snow should stay bright: got L=%.1f from %.1f", snow, snowLab[0])
	}
	if refl >= sky {
		t.Fatalf("reflection L=%.1f should stay below sky L=%.1f", refl, sky)
	}
	if skyBlueWeight(skyLab) < 0.5 {
		t.Fatalf("sky blue weight=%.2f want high", skyBlueWeight(skyLab))
	}
	if highlightSkyWeight(snowLab) > 0.05 {
		t.Fatalf("snow sky weight=%.2f want ~0", highlightSkyWeight(snowLab))
	}
}

func TestSnowdonHighlightToneMap(t *testing.T) {
	path := os.Getenv("HOME") + "/Downloads/Snowdon.jpg"
	f, err := os.Open(path)
	if err != nil {
		t.Skip(path)
	}
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}

	pipe := ColorPipeline{
		LABHighlightEnabled:  true,
		LABHighlightStrength: 1,
	}
	out := ApplyLABProcessing(img, pipe, PaletteSamsungDisplay, false)

	sky := regionPaletteStats(out, img, func(x, y, w, h int) bool { return y < h/3 })
	refl := regionPaletteStats(out, img, func(x, y, w, h int) bool {
		return y >= int(0.55*float64(h)) && y < int(0.75*float64(h)) && x > w/4 && x < 3*w/4
	})
	rawRefl := regionPaletteStats(img, img, func(x, y, w, h int) bool {
		return y >= int(0.55*float64(h)) && y < int(0.75*float64(h)) && x > w/4 && x < 3*w/4
	})
	snow := regionPaletteStats(out, img, func(x, y, w, h int) bool {
		return y >= h/4 && y < h/2 && x > w/3 && x < 2*w/3
	})
	rawSnow := regionPaletteStats(img, img, func(x, y, w, h int) bool {
		return y >= h/4 && y < h/2 && x > w/3 && x < 2*w/3
	})

	t.Logf("raw reflection: L=%.1f white=%.1f%% blue=%.1f%%", rawRefl.meanL, rawRefl.whitePct, rawRefl.bluePct)
	t.Logf("processed sky: L=%.1f white=%.1f%% blue=%.1f%%", sky.meanL, sky.whitePct, sky.bluePct)
	t.Logf("processed reflection: L=%.1f white=%.1f%% blue=%.1f%%", refl.meanL, refl.whitePct, refl.bluePct)
	t.Logf("raw snow: L=%.1f white=%.1f%%", rawSnow.meanL, rawSnow.whitePct)
	t.Logf("processed snow: L=%.1f white=%.1f%%", snow.meanL, snow.whitePct)

	if sky.whitePct > 40 {
		t.Fatalf("sky still too white: %.1f%%", sky.whitePct)
	}
	if sky.bluePct < 10 {
		t.Fatalf("sky lost blue: %.1f%%", sky.bluePct)
	}
	if refl.meanL >= sky.meanL {
		t.Fatalf("reflection should be darker than sky")
	}
	if refl.meanL >= rawRefl.meanL {
		t.Fatalf("reflection should darken vs original")
	}
	if snow.meanL < rawSnow.meanL-8 {
		t.Fatalf("snow contrast crushed: L %.1f -> %.1f", rawSnow.meanL, snow.meanL)
	}
}

type regionStats struct {
	meanL    float64
	whitePct float64
	bluePct  float64
}

func regionPaletteStats(img, boundsImg image.Image, fn func(x, y, w, h int) bool) regionStats {
	b := boundsImg.Bounds()
	w, h := b.Dx(), b.Dy()
	var sumL float64
	counts := [6]int{}
	n := 0
	const step = 8
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			x0, y0 := x-b.Min.X, y-b.Min.Y
			if !fn(x0, y0, w, h) {
				continue
			}
			r8, g8, b8, _ := img.At(x, y).RGBA()
			rgb := [3]float64{float64(r8 >> 8), float64(g8 >> 8), float64(b8 >> 8)}
			idx := nearestColor(rgb, PaletteSamsungDisplay)
			counts[idx]++
			lab := srgbToLAB([3]float64{rgb[0] / 255, rgb[1] / 255, rgb[2] / 255})
			sumL += lab[0]
			n++
		}
	}
	fnF := float64(n)
	return regionStats{
		meanL:    sumL / fnF,
		whitePct: 100 * float64(counts[1]) / fnF,
		bluePct:  100 * float64(counts[4]) / fnF,
	}
}
