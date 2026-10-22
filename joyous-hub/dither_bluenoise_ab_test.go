package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

// BN=<dir with mumdad_hq.jpg [+ color.json]> go test -run TestBlueNoiseAB .
//
// A/B of white- vs blue-noise dither through the production colour pipeline,
// once per panel palette. Writes, per panel and noise mode: the full photo,
// a 1:1 crop, and a smooth neutral gradient (grain vs fine texture shows best
// there). Fewer inks (the 10"'s 6) means coarser mixes, so noise structure is
// easiest to judge on the most restrictive palette.
func TestBlueNoiseAB(t *testing.T) {
	dir := os.Getenv("BN")
	if dir == "" {
		t.Skip("set BN=<dir>")
	}
	raw, err := os.ReadFile(dir + "/mumdad_hq.jpg")
	if err != nil {
		t.Fatal(err)
	}
	tdir := t.TempDir()
	if cj, e := os.ReadFile(dir + "/color.json"); e == nil {
		os.WriteFile(tdir+"/color.json", cj, 0644)
	}
	store := NewImageStore(tdir)
	store.SetColorStore(NewColorStore(tdir))
	pipe := store.colorPipelineForMetaOverride(ImageMeta{}, nil)
	src, err := decodeAnyImage(raw)
	if err != nil {
		t.Fatal(err)
	}

	blueNoiseOnce.Do(buildBlueNoiseTile)
	tile := image.NewGray(image.Rect(0, 0, blueNoiseN*8, blueNoiseN*8))
	for y := 0; y < blueNoiseN*8; y++ {
		for x := 0; x < blueNoiseN*8; x++ {
			tile.SetGray(x, y, color.Gray{uint8(blueNoiseTile[(y/8)*blueNoiseN+(x/8)] * 255)})
		}
	}
	savePNG(t, dir+"/bluenoise_tile.png", tile)

	for _, p := range []struct {
		name    string
		palette [][3]float64
		w, h    int // portrait panel pixels
	}{
		{"10", pipe.InkJoyDisplay[:], frameH, frameW},
		{"28", paletteInkJoy28Display[:], ij28W, ij28H},
	} {
		photo := resizeHQ(centerCropToAspect(src, float64(p.w)/float64(p.h)), p.w, p.h)
		crop := image.Rect(p.w/2-250, p.h/2-250, p.w/2+250, p.h/2+250)
		for _, m := range []struct {
			name  string
			white bool
		}{{"white", true}, {"blue", false}} {
			ditherNoise = blueNoiseSample
			if m.white {
				ditherNoise = whiteNoiseSample
			}
			idx := StuckiTwoPalette(photo, p.palette, pipe, false, stuckiOptionsInkJoy(pipe))
			t.Logf("%s\" %s: %s", p.name, m.name, inkShares(idx, len(p.palette)))
			prefix := dir + "/ab" + p.name + "_" + m.name
			savePNG(t, prefix+"_full.png", paintIndices(idx, p.palette, image.Rect(0, 0, p.w, p.h)))
			savePNG(t, prefix+"_crop.png", paintIndices(idx, p.palette, crop))
			gidx := StuckiTwoPalette(neutralGradient(600), p.palette, pipe, false, stuckiOptionsInkJoy(pipe))
			savePNG(t, prefix+"_grad.png", paintIndices(gidx, p.palette, image.Rect(0, 0, 600, 600)))
		}
	}
	ditherNoise = blueNoiseSample
	t.Logf("wrote ab{10,28}_{white,blue}_{full,crop,grad}.png, bluenoise_tile.png to %s", dir)
}

// whiteNoiseSample is the pre-blue-noise jitter (an xorshift hash), kept for A/B.
func whiteNoiseSample(x, y, c int) float64 {
	n := uint32(x*73856093 ^ y*19349663 ^ c*83492791)
	n ^= n << 13
	n ^= n >> 17
	n ^= n << 5
	return float64(n%1000)/499.5 - 1.0
}

// neutralGradient is an n×n vertical grey ramp, 90→210.
func neutralGradient(n int) image.Image {
	g := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		v := uint8(90 + 120*y/n)
		for x := 0; x < n; x++ {
			g.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	return g
}

// paintIndices renders region r of a palette-index grid in the palette's colours.
func paintIndices(idx [][]byte, palette [][3]float64, r image.Rectangle) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := palette[idx[y][x]]
			out.Set(x-r.Min.X, y-r.Min.Y, color.RGBA{uint8(c[0]), uint8(c[1]), uint8(c[2]), 255})
		}
	}
	return out
}

// inkShares formats each palette index's share of the grid.
func inkShares(idx [][]byte, n int) string {
	cnt := make([]int, n)
	tot := 0
	for _, row := range idx {
		for _, v := range row {
			cnt[v]++
			tot++
		}
	}
	s := ""
	for i, c := range cnt {
		s += fmt.Sprintf("ink%d=%.2f%% ", i, 100*float64(c)/float64(tot))
	}
	return s
}

func savePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
