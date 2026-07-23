package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// ditherAlgo is a named competing encoder for swatch bake-offs.
type ditherAlgo struct {
	Name string
	Run  func(img image.Image, palette [6][3]float64) [][]byte
}

// ditherSwatch is one gradient (or solid) stimulus for visual comparison.
type ditherSwatch struct {
	Name string
	Img  *image.RGBA
}

func makeLinearGradient(w, h int, c0, c1 color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			t := float64(x) / float64(w-1)
			img.Set(x, y, color.RGBA{
				R: uint8(float64(c0.R) + t*float64(int(c1.R)-int(c0.R))),
				G: uint8(float64(c0.G) + t*float64(int(c1.G)-int(c0.G))),
				B: uint8(float64(c0.B) + t*float64(int(c1.B)-int(c0.B))),
				A: 255,
			})
		}
	}
	return img
}

func makeVerticalGradient(w, h int, c0, c1 color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h-1)
		c := color.RGBA{
			R: uint8(float64(c0.R) + t*float64(int(c1.R)-int(c0.R))),
			G: uint8(float64(c0.G) + t*float64(int(c1.G)-int(c0.G))),
			B: uint8(float64(c0.B) + t*float64(int(c1.B)-int(c0.B))),
			A: 255,
		}
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func makeSolid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	return img
}

// defaultDitherSwatches are the bake-off stimuli: skin/peach ramps, gray, and
// saturated primaries that usually expose contouring vs grain tradeoffs.
func defaultDitherSwatches(w, h int) []ditherSwatch {
	peach := color.RGBA{R: 210, G: 160, B: 140, A: 255}
	peachDeep := color.RGBA{R: 180, G: 120, B: 100, A: 255}
	peachHi := color.RGBA{R: 245, G: 220, B: 210, A: 255}
	white := color.RGBA{R: 240, G: 240, B: 240, A: 255}
	black := color.RGBA{R: 20, G: 20, B: 20, A: 255}
	red := color.RGBA{R: 180, G: 40, B: 30, A: 255}
	yellow := color.RGBA{R: 220, G: 200, B: 60, A: 255}
	sky := color.RGBA{R: 120, G: 160, B: 210, A: 255}
	return []ditherSwatch{
		{Name: "peach→white", Img: makeLinearGradient(w, h, peach, white)},
		{Name: "peachDeep→peachHi", Img: makeLinearGradient(w, h, peachDeep, peachHi)},
		{Name: "solid peach", Img: makeSolid(w, h, peach)},
		{Name: "black→white", Img: makeLinearGradient(w, h, black, white)},
		{Name: "red→yellow", Img: makeLinearGradient(w, h, red, yellow)},
		{Name: "sky→white", Img: makeLinearGradient(w, h, sky, white)},
		{Name: "peach vert", Img: makeVerticalGradient(w, h, peachDeep, peachHi)},
	}
}

func stuckiAlgo(name string, opts stuckiOptions) ditherAlgo {
	return ditherAlgo{
		Name: name,
		Run: func(img image.Image, palette [6][3]float64) [][]byte {
			src := img
			if opts.PreDither {
				noise := stuckiPreDitherNoise
				if opts.PreDitherStrength > 0 {
					noise = opts.PreDitherStrength
				}
				src = applyPreDitherNoise(src, noise)
			}
			return StuckiDither(src, palette, opts)
		},
	}
}

// competingDitherAlgos is the bake-off lineup. Keep names short for sheet labels.
func competingDitherAlgos() []ditherAlgo {
	classic := stuckiOptions{Serpentine: true}
	thrOnly := stuckiOptions{
		Serpentine:   true,
		ThresholdMod: stuckiThresholdModStrength,
	}
	edgeOnly := stuckiOptions{
		Serpentine:   true,
		EdgePreserve: stuckiEdgePreserve,
		PreDither:    true,
	}
	current := stuckiOptionsSamsung(ColorPipeline{})
	softEdge := current
	softEdge.EdgePreserve = 0.2
	noAnti := current
	noAnti.AntiRunBias = 0
	return []ditherAlgo{
		stuckiAlgo("stucki-classic", classic),
		stuckiAlgo("stucki-thr", thrOnly),
		stuckiAlgo("stucki-edge+pre", edgeOnly),
		stuckiAlgo("stucki-current", current),
		stuckiAlgo("stucki-softEdge", softEdge),
		stuckiAlgo("stucki-noAnti", noAnti),
		{Name: "floyd-steinberg", Run: FloydSteinbergDither},
		{Name: "atkinson", Run: AtkinsonDither},
		{Name: "inkjoy-prod-FS", Run: func(img image.Image, palette [6][3]float64) [][]byte {
			return StuckiTwoPalette(img, palette, ColorPipeline{}, false, stuckiOptionsInkJoy(ColorPipeline{}))
		}},
	}
}

func indicesToDisplayRGBA(idx [][]byte, palette [6][3]float64) *image.RGBA {
	h := len(idx)
	if h == 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	w := len(idx[0])
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := palette[idx[y][x]]
			out.Set(x, y, color.RGBA{uint8(c[0]), uint8(c[1]), uint8(c[2]), 255})
		}
	}
	return out
}

func swatchMaxMonoRun(idx [][]byte) int {
	maxRun := 0
	for y := range idx {
		run := 1
		for x := 1; x < len(idx[y]); x++ {
			if idx[y][x] == idx[y][x-1] {
				run++
				if run > maxRun {
					maxRun = run
				}
			} else {
				run = 1
			}
		}
	}
	return maxRun
}

func swatchHighDomFrac(idx [][]byte) float64 {
	h := len(idx)
	if h < 5 {
		return 0
	}
	w := len(idx[0])
	if w < 5 {
		return 0
	}
	high, n := 0, 0
	for y := 2; y < h-2; y++ {
		for x := 2; x < w-2; x++ {
			n++
			ink := idx[y][x]
			c := 0
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					if idx[y+dy][x+dx] == ink {
						c++
					}
				}
			}
			if c >= 18 {
				high++
			}
		}
	}
	if n == 0 {
		return 0
	}
	return float64(high) / float64(n)
}

func drawLabel(dst *image.RGBA, x, y int, s string) {
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(color.RGBA{R: 240, G: 240, B: 240, A: 255}),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(x, y),
	}
	// Shadow for readability on bright swatches.
	d2 := *d
	d2.Src = image.NewUniform(color.RGBA{A: 200})
	d2.Dot = fixed.P(x+1, y+1)
	d2.DrawString(s)
	d.DrawString(s)
}

// RenderDitherSwatchSheet builds a contact sheet: rows = swatches, cols = algorithms.
// Leftmost column is the undithered source gradient.
func RenderDitherSwatchSheet(swatches []ditherSwatch, algos []ditherAlgo, palette [6][3]float64) *image.RGBA {
	if len(swatches) == 0 || len(algos) == 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	const (
		labelH = 16
		gap    = 4
		pad    = 8
	)
	cellW := swatches[0].Img.Bounds().Dx()
	cellH := swatches[0].Img.Bounds().Dy()
	cols := 1 + len(algos) // source + algos
	rows := len(swatches)
	sheetW := pad*2 + cols*cellW + (cols-1)*gap
	sheetH := pad*2 + rows*(cellH+labelH) + (rows-1)*gap
	sheet := image.NewRGBA(image.Rect(0, 0, sheetW, sheetH))
	draw.Draw(sheet, sheet.Bounds(), &image.Uniform{C: color.RGBA{R: 24, G: 24, B: 28, A: 255}}, image.Point{}, draw.Src)

	for r, sw := range swatches {
		y0 := pad + r*(cellH+labelH+gap)
		// Source column.
		x0 := pad
		draw.Draw(sheet, image.Rect(x0, y0+labelH, x0+cellW, y0+labelH+cellH), sw.Img, image.Point{}, draw.Src)
		drawLabel(sheet, x0+2, y0+12, "src · "+sw.Name)

		for c, algo := range algos {
			x := pad + (c+1)*(cellW+gap)
			idx := algo.Run(sw.Img, palette)
			tile := indicesToDisplayRGBA(idx, palette)
			draw.Draw(sheet, image.Rect(x, y0+labelH, x+cellW, y0+labelH+cellH), tile, image.Point{}, draw.Src)
			run := swatchMaxMonoRun(idx)
			dom := swatchHighDomFrac(idx)
			label := fmt.Sprintf("%s  run=%d dom=%.0f%%", algo.Name, run, 100*dom)
			if len(label) > 42 {
				label = label[:42]
			}
			drawLabel(sheet, x+2, y0+12, label)
		}
	}
	return sheet
}

// peachSwatchScore is a rough automated ranking: lower is better (less mono
// dominance on the solid-peach and peach→white ramps).
func peachSwatchScore(algo ditherAlgo, palette [6][3]float64) float64 {
	peach := color.RGBA{R: 210, G: 160, B: 140, A: 255}
	white := color.RGBA{R: 240, G: 240, B: 240, A: 255}
	solid := makeSolid(160, 48, peach)
	ramp := makeLinearGradient(160, 48, peach, white)
	s0 := swatchHighDomFrac(algo.Run(solid, palette))
	s1 := swatchHighDomFrac(algo.Run(ramp, palette))
	r0 := float64(swatchMaxMonoRun(algo.Run(solid, palette))) / 160.0
	return s0 + s1 + 0.25*math.Min(r0, 1)
}
