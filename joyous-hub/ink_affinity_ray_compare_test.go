package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestRayCompareMumDadOutput(t *testing.T) {
	srcPath := "/tmp/eink-ray/d059.jpg"
	f, err := os.Open(srcPath)
	if err != nil {
		t.Skip(err)
	}
	src, err := jpeg.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	src = resizeToFrame(src)

	offPipe := ColorPipeline{}
	onPipe := ColorPipeline{LABInkAffinityRayEnabled: true, LABInkAffinityRayStrength: 1}
	opts := stuckiOptionsInkJoy(onPipe)

	offIdx := StuckiTwoPalette(src, PaletteInkJoyDisplay[:], offPipe, false, opts)
	onIdx := StuckiTwoPalette(src, PaletteInkJoyDisplay[:], onPipe, false, opts)
	offImg := renderIndicesToRGB(offIdx, PaletteInkJoyDisplay[:])
	onImg := renderIndicesToRGB(onIdx, PaletteInkJoyDisplay[:])

	dir := "/tmp/eink-ray/compare"
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	mustWritePNG(t, filepath.Join(dir, "off.png"), offImg)
	mustWritePNG(t, filepath.Join(dir, "on.png"), onImg)
	mustWritePNG(t, filepath.Join(dir, "faces.png"), sideBySide(
		cropRect(offImg, 280, 380, 1040, 520),
		cropRect(onImg, 280, 380, 1040, 520),
	))
	mustWritePNG(t, filepath.Join(dir, "sky.png"), sideBySide(
		cropRect(offImg, 0, 0, 1600, 360),
		cropRect(onImg, 0, 0, 1600, 360),
	))
	mustWritePNG(t, filepath.Join(dir, "mum.png"), sideBySide(
		cropRect(offImg, 280, 540, 520, 520),
		cropRect(onImg, 280, 540, 520, 520),
	))
	mustWritePNG(t, filepath.Join(dir, "dad.png"), sideBySide(
		cropRect(offImg, 820, 540, 500, 520),
		cropRect(onImg, 820, 540, 500, 520),
	))

	pre := ApplyInkAffinityRay(src, PaletteInkJoyDisplay[:], 1.0, false)
	mustWritePNG(t, filepath.Join(dir, "predither-faces.png"), sideBySide(
		cropRect(src, 280, 520, 1040, 620),
		cropRect(pre, 280, 520, 1040, 620),
	))
	mustWritePNG(t, filepath.Join(dir, "predither-mum.png"), sideBySide(
		cropRect(src, 300, 380, 420, 420),
		cropRect(pre, 300, 380, 420, 420),
	))
	mustWritePNG(t, filepath.Join(dir, "dither-mum-face.png"), sideBySide(
		cropRect(offImg, 300, 380, 420, 420),
		cropRect(onImg, 300, 380, 420, 420),
	))
	mustWritePNG(t, filepath.Join(dir, "dither-mum-head.png"), sideBySide(
		cropRect(offImg, 300, 380, 420, 420),
		cropRect(onImg, 300, 380, 420, 420),
	))
	mustWritePNG(t, filepath.Join(dir, "dither-dad-face.png"), sideBySide(
		cropRect(offImg, 880, 560, 380, 400),
		cropRect(onImg, 880, 560, 380, 400),
	))

	field := buildRayField(src)
	mask := buildRayFaceMask(src, field)
	mustWritePNG(t, filepath.Join(dir, "face-mask.png"), maskOverlay(src, mask, 280, 360, 560, 520))
	mustWritePNG(t, filepath.Join(dir, "face-mask-bin.png"), maskBinary(mask, 280, 360, 560, 520))
	t.Logf("predither mum face L* %s", faceLStat(src, pre, mask, 280, 360, 520, 500))
	t.Logf("predither dad face L* %s", faceLStat(src, pre, mask, 820, 360, 500, 500))
	t.Logf("predither mum stops %s", faceStopStat(pre, mask, 280, 360, 520, 500))
	t.Logf("mask mum forehead %s", maskFrac(mask, 360, 390, 200, 90))
	t.Logf("mask mum jacket  %s", maskFrac(mask, 300, 680, 280, 160))
	t.Logf("mask dad forehead %s", maskFrac(mask, 900, 400, 200, 90))

	names := []string{"K", "W", "Y", "R", "B", "G"}
	t.Logf("mask mum  off %s", inkHistMask(offIdx, mask, 280, 360, 520, 500, names))
	t.Logf("mask mum   on %s", inkHistMask(onIdx, mask, 280, 360, 520, 500, names))
	t.Logf("mask dad  off %s", inkHistMask(offIdx, mask, 820, 360, 500, 500, names))
	t.Logf("mask dad   on %s", inkHistMask(onIdx, mask, 820, 360, 500, 500, names))
	t.Logf("sky crop       off %s", inkHist(offIdx, 0, 0, 1600, 360, names))
	t.Logf("sky crop        on %s", inkHist(onIdx, 0, 0, 1600, 360, names))

	probes := []struct {
		name string
		x, y int
	}{
		{"mum-forehead", 470, 430},
		{"mum-cheek", 500, 520},
		{"mum-chin", 490, 620},
		{"mum-collar", 480, 680},
		{"mum-jacket", 420, 760},
		{"mum-hair", 430, 400},
		{"mum-sky", 360, 360},
		{"mum-sky-left", 340, 410},
		{"mum-sky-halo", 380, 400},
		{"trees-left", 300, 520},
		{"dad-forehead", 1020, 450},
		{"dad-cheek", 1040, 560},
		{"dad-jacket", 1100, 780},
	}
	for _, p := range probes {
		lab := pixelLABAt(src, p.x, p.y)
		after := pixelLABAt(pre, p.x, p.y)
		t.Logf("%s (%d,%d) L=%.0f a=%.1f b=%.1f C=%.1f role=%d seed=%v chalk=%v skin=%v walk=%v mask=%v w=%.2f → L=%.0f a=%.1f b=%.1f C=%.1f",
			p.name, p.x, p.y, lab[0], lab[1], lab[2], math.Hypot(lab[1], lab[2]),
			field.at(p.x, p.y), isRayFleshSeedLAB(lab), isChalkFaceLAB(lab), isFaceSkinLAB(lab),
			isFaceWalkableLAB(lab, field.at(p.x, p.y)), mask.at(p.x, p.y), mask.weight(p.x, p.y),
			after[0], after[1], after[2], math.Hypot(after[1], after[2]))
	}
}

func maskBinary(mask rayMask, x, y, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for yy := 0; yy < h; yy++ {
		for xx := 0; xx < w; xx++ {
			c := uint8(mask.weight(x+xx, y+yy) * 255)
			dst.Set(xx, yy, color.RGBA{c, 20, 20, 255})
		}
	}
	return dst
}

func maskOverlay(src image.Image, mask rayMask, x, y, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	b := src.Bounds()
	for yy := 0; yy < h; yy++ {
		for xx := 0; xx < w; xx++ {
			px, py := b.Min.X+x+xx, b.Min.Y+y+yy
			r, g, bl, a := src.At(px, py).RGBA()
			if mask.at(px, py) {
				dst.Set(xx, yy, color.RGBA{uint8(r >> 8), uint8(g >> 9), 40, uint8(a >> 8)})
			} else {
				dst.Set(xx, yy, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), uint8(a >> 8)})
			}
		}
	}
	return dst
}

func faceLStat(src, pre image.Image, mask rayMask, x, y, w, h int) string {
	b := src.Bounds()
	var n, hiSrc, hiPre int
	var sumSrc, sumPre float64
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			if !mask.at(b.Min.X+xx, b.Min.Y+yy) {
				continue
			}
			lab := pixelLABAt(src, b.Min.X+xx, b.Min.Y+yy)
			after := pixelLABAt(pre, b.Min.X+xx, b.Min.Y+yy)
			n++
			sumSrc += lab[0]
			sumPre += after[0]
			if lab[0] > 60 {
				hiSrc++
			}
			if after[0] > 55 {
				hiPre++
			}
		}
	}
	if n == 0 {
		return "n=0"
	}
	return fmt.Sprintf("n=%d mean %.1f->%.1f  hi %d->%d", n, sumSrc/float64(n), sumPre/float64(n), hiSrc, hiPre)
}

func faceStopStat(pre image.Image, mask rayMask, x, y, w, h int) string {
	b := pre.Bounds()
	var ls []float64
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			if mask.weight(b.Min.X+xx, b.Min.Y+yy) < 0.5 {
				continue
			}
			ls = append(ls, pixelLABAt(pre, b.Min.X+xx, b.Min.Y+yy)[0])
		}
	}
	if len(ls) < 16 {
		return "n=0"
	}
	sort.Float64s(ls)
	p10 := ls[len(ls)/10]
	p90 := ls[len(ls)*9/10]
	return fmt.Sprintf("n=%d p10=%.1f p90=%.1f  %.1f stops", len(ls), p10, p90, labStops(p10, p90))
}

func maskFrac(mask rayMask, x, y, w, h int) string {
	n, on := 0, 0
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			n++
			if mask.at(xx, yy) {
				on++
			}
		}
	}
	if n == 0 {
		return "n=0"
	}
	return fmt.Sprintf("n=%d on=%.1f%%", n, 100*float64(on)/float64(n))
}

func inkHistMask(idx [][]byte, mask rayMask, x, y, w, h int, names []string) string {
	var c [6]int
	n := 0
	for yy := y; yy < y+h && yy < len(idx); yy++ {
		for xx := x; xx < x+w && xx < len(idx[yy]); xx++ {
			if !mask.at(xx, yy) {
				continue
			}
			c[idx[yy][xx]]++
			n++
		}
	}
	return fmt.Sprintf("n=%d%s", n, inkFmt(c, n, names))
}

func inkHistSkin(idx [][]byte, src image.Image, field rayField, x, y, w, h int, names []string) string {
	var c [6]int
	n := 0
	b := src.Bounds()
	for yy := y; yy < y+h && yy < len(idx); yy++ {
		for xx := x; xx < x+w && xx < len(idx[yy]); xx++ {
			lab := pixelLABAt(src, b.Min.X+xx, b.Min.Y+yy)
			if field.at(b.Min.X+xx, b.Min.Y+yy) != rayRoleFace || !isFaceSkinLAB(lab) {
				continue
			}
			c[idx[yy][xx]]++
			n++
		}
	}
	return fmt.Sprintf("n=%d%s", n, inkFmt(c, n, names))
}

func inkHist(idx [][]byte, x, y, w, h int, names []string) string {
	var c [6]int
	n := 0
	for yy := y; yy < y+h && yy < len(idx); yy++ {
		for xx := x; xx < x+w && xx < len(idx[yy]); xx++ {
			c[idx[yy][xx]]++
			n++
		}
	}
	return inkFmt(c, n, names)
}

func inkFmt(c [6]int, n int, names []string) string {
	s := ""
	for i, name := range names {
		p := 0.0
		if n > 0 {
			p = 100 * float64(c[i]) / float64(n)
		}
		s += fmt.Sprintf(" %s=%.1f%%", name, p)
	}
	return s
}

func cropRect(src image.Image, x, y, w, h int) image.Image {
	b := src.Bounds()
	r := image.Rect(b.Min.X+x, b.Min.Y+y, b.Min.X+x+w, b.Min.Y+y+h)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
	return dst
}

func sideBySide(a, b image.Image) image.Image {
	ab, bb := a.Bounds(), b.Bounds()
	w, h := ab.Dx()+bb.Dx()+8, ab.Dy()
	if bb.Dy() > h {
		h = bb.Dy()
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, image.Rect(0, 0, ab.Dx(), ab.Dy()), a, ab.Min, draw.Src)
	draw.Draw(dst, image.Rect(ab.Dx()+8, 0, ab.Dx()+8+bb.Dx(), bb.Dy()), b, bb.Min, draw.Src)
	return dst
}

func mustWritePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
}
