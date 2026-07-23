package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"bytes"
)

// PaletteInkJoy holds legacy physical ink RGB (use PaletteInkJoyDisplay for dither).
var PaletteInkJoy = [6][3]float64{
	{30, 30, 30},
	{149, 162, 165},
	{166, 165, 17},
	{121, 23, 17},
	{0, 76, 136},
	{46, 91, 65},
}

// PaletteInkJoySend (P1) — pure sRGB primaries (verify on frame).
var PaletteInkJoySend = [6][3]float64{
	{0, 0, 0},
	{255, 255, 255},
	{255, 255, 0},
	{255, 0, 0},
	{0, 0, 255},
	{0, 255, 0},
}

// PaletteInkJoyDisplay (P2) — on-panel Stucki targets (IMG_0158 primaries, click calibration).
var PaletteInkJoyDisplay = [6][3]float64{
	{42, 33, 38},
	{206, 218, 213},
	{213, 206, 84},
	{115, 36, 26},
	{47, 98, 176},
	{97, 134, 113},
}

// PaletteSamsungSend (P1) — pure sRGB primaries written to PNG.
var PaletteSamsungSend = [6][3]float64{
	{0, 0, 0},         // #000000 black
	{255, 255, 255},   // #FFFFFF white
	{255, 255, 0},     // #FFFF00 yellow
	{255, 0, 0},       // #FF0000 red
	{0, 0, 255},       // #0000FF blue
	{0, 255, 0},       // #00FF00 green
}

// PaletteSamsungDisplay (P2) — on-panel Stucki targets (IMG_0111 primaries, right sample).
var PaletteSamsungDisplay = [6][3]float64{
	{35, 35, 45},
	{176, 186, 195},
	{195, 183, 12},
	{121, 6, 0},
	{0, 74, 159},
	{50, 105, 98},
}

// PaletteSamsung is the send palette (alias kept for callers that only need P1).
var PaletteSamsung = PaletteSamsungSend

const samsungW, samsungH = 2560, 1440

// stuckiPreDitherNoise breaks up visible banding in smooth gradients (sky, water)
// before error diffusion. Deterministic per pixel so output is stable for caching.
const stuckiPreDitherNoise = 3.0

// inkjoyPortraitPreDitherNoise is gentler than Samsung's gradient noise; wipes already
// provide sub-tone smoothing on InkJoy, so heavy pre-dither fights the lo-byte pattern.
const inkjoyPortraitPreDitherNoise = 2.0

// stuckiEdgePreserve limits how much quantization error crosses strong luminance
// edges (lightning vs cloud). Kept moderate — high values posterize soft face
// gradients into peach/teal islands with sharp borders.
const stuckiEdgePreserve = 0.40

// stuckiThresholdModStrength is Zhou–Fang threshold amplitude in percent-of-span
// units (12 → ±0.24 around 0.5 at full midtone weight).
const stuckiThresholdModStrength = 12.0

// stuckiPeachThresholdExtra adds extra threshold jitter on skin so peach
// midtones mix at a finer spatial scale instead of locking into large
// same-mix regions with harsh boundaries between them.
const stuckiPeachThresholdExtra = 0.28

// stuckiAntiRunBias is the max soft threshold nudge toward the second-nearest
// ink when the causal neighborhood is dominated by the first-nearest.
const stuckiAntiRunBias = 0.28

// stuckiAntiRunRadius is how far (in pixels) already-written neighbors participate
// in the anti-run tally — roughly a 5×5 causal window, not a resolution reduction.
const stuckiAntiRunRadius = 3

// stuckiMaxMonoRun: in peach/skin, after this many consecutive same-ink pixels
// (image-space), prefer the alternate of {best, second}. Kept mild — aggressive
// forcing created teal/peach vitiligo regions with sharp borders.
const stuckiMaxMonoRun = 3

const (
	stuckiEdgeSoft = 6.0  // below: full error diffusion (face soft gradients live here)
	stuckiEdgeHard = 28.0 // above: minimum transfer across the edge
)

// stuckiOptions selects error-diffusion tuning for StuckiTwoPalette.
type stuckiOptions struct {
	Kernel             ditherKernel
	Serpentine         bool
	EdgePreserve       float64
	PreDither          bool
	PreDitherStrength  float64
	DynamicRange       bool
	ThresholdMod       float64 // 0 = off; else Zhou–Fang midtone threshold jitter (Stucki only)
	AntiRunBias        float64 // 0 = off; else max thr shift when local ink dominates (Stucki only)
}

type ditherKernel int

const (
	ditherKernelStucki ditherKernel = iota
	ditherKernelFloydSteinberg
)

func stuckiOptionsSamsung(pipe ColorPipeline) stuckiOptions {
	return stuckiOptions{
		Kernel:       ditherKernelStucki,
		Serpentine:   true,
		EdgePreserve: stuckiEdgePreserve,
		PreDither:    true,
		DynamicRange: pipe.LABDynamicRangeEnabled,
		ThresholdMod: stuckiThresholdModStrength,
		AntiRunBias:  stuckiAntiRunBias,
	}
}

// stuckiOptionsInkJoy trials Floyd–Steinberg on the same 6-ink display palette
// (swatch bake-off preferred FS over Stucki+knobs for gradient continuity).
func stuckiOptionsInkJoy(pipe ColorPipeline) stuckiOptions {
	return stuckiOptions{
		Kernel:       ditherKernelFloydSteinberg,
		Serpentine:   true,
		PreDither:    true,
		DynamicRange: pipe.LABDynamicRangeEnabled,
	}
}

// hiBytes maps palette index 0-5 to the hi byte values used in the .bin format.
var hiBytes = [6]byte{0x01, 0x02, 0x03, 0x04, 0x06, 0x07}
var hiByteToIdx = map[byte]int{0x01: 0, 0x02: 1, 0x03: 2, 0x04: 3, 0x06: 4, 0x07: 5}

// NativeEncode encodes hi and lo grids (top-to-bottom) as a native paletted PNG.
// Palette index = hiIndex * 32 + lo/8; palette RGB = (hiByte, loVal, 0).
func NativeEncode(hi, lo [][]byte) image.Image {
	h := len(hi)
	if h == 0 {
		return image.NewPaletted(image.Rect(0, 0, 0, 0), nativePalette())
	}
	w := len(hi[0])
	pal := nativePalette()
	img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	for y := range h {
		for x := range w {
			hiIdx := hiByteToIdx[hi[y][x]]
			loStep := int(lo[y][x]) / 8
			img.SetColorIndex(x, y, uint8(hiIdx*32+loStep))
		}
	}
	return img
}

// NativeDecode decodes a native paletted PNG back to hi/lo grids (top-to-bottom).
func NativeDecode(img image.Image) (hi, lo [][]byte) {
	b := img.Bounds()
	h, w := b.Dy(), b.Dx()
	hi = make([][]byte, h)
	lo = make([][]byte, h)
	pal, ok := img.(*image.Paletted)
	if !ok {
		// Convert to paletted via palette
		for y := range h {
			hi[y] = make([]byte, w)
			lo[y] = make([]byte, w)
		}
		return
	}
	palette := pal.Palette
	for y := range h {
		hi[y] = make([]byte, w)
		lo[y] = make([]byte, w)
		for x := range w {
			idx := pal.ColorIndexAt(x, y)
			if int(idx) < len(palette) {
				r, g, _, _ := palette[idx].RGBA()
				hi[y][x] = byte(r >> 8) // R channel = hi byte value
				lo[y][x] = byte(g >> 8) // G channel = lo byte value
			}
		}
	}
	return
}

// ToBin interleaves hi/lo grids into a raw .bin byte slice (bottom-to-top row order).
// rotate90CCW rotates an image 90° counter-clockwise.
// Input w×h → output h×w.
func rotate90CCW(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := range h {
		for x := range w {
			// 90° CCW: dst(y, w-1-x) = src(x, y)
			dst.Set(y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func ToBin(hi, lo [][]byte) []byte {
	h := len(hi)
	if h == 0 {
		return []byte{}
	}
	w := len(hi[0])
	out := make([]byte, w*h*2)
	i := 0
	for row := h - 1; row >= 0; row-- { // bottom-to-top
		for x := range w {
			out[i] = hi[row][x]
			out[i+1] = lo[row][x]
			i += 2
		}
	}
	return out
}

// FromBin splits a raw .bin byte slice into hi/lo grids (top-to-bottom, display order).
func FromBin(bin []byte, w, h int) (hi, lo [][]byte) {
	hi = make([][]byte, h)
	lo = make([][]byte, h)
	for y := range h {
		hi[y] = make([]byte, w)
		lo[y] = make([]byte, w)
	}
	// Bin is bottom-to-top; iterate in reverse to fill display-order (top-to-bottom).
	i := 0
	for row := h - 1; row >= 0; row-- {
		for x := range w {
			hi[row][x] = bin[i]
			lo[row][x] = bin[i+1]
			i += 2
		}
	}
	return
}

// wipeStepAt maps a 0..1 position along an axis to lo bytes 0,8,…,248 (31 steps).
func wipeStepAt(pos float64) byte {
	if pos < 0 {
		pos = 0
	}
	if pos > 1 {
		pos = 1
	}
	step := int(pos*30 + 0.5)
	if step > 30 {
		step = 30
	}
	if step == 30 {
		return 248
	}
	return byte(step * 8)
}

const wipeStepCount = 31

// wipeStepIndexToByte maps step index 0..30 to lo bytes 0,8,…,248.
func wipeStepIndexToByte(i int) byte {
	i %= wipeStepCount
	if i < 0 {
		i += wipeStepCount
	}
	if i >= wipeStepCount-1 {
		return 248
	}
	return byte(i * 8)
}

// wipeStepIndexFromByte returns the step index for a lo byte.
func wipeStepIndexFromByte(lo byte) int {
	if lo >= 248 {
		return wipeStepCount - 1
	}
	return int(lo / 8)
}

// MakeHorizontalLineWipe assigns one lo step per row (constant across width).
// stride must be coprime with wipeStepCount so rows 0..30 cycle all 31 steps;
// adjacent rows land on non-consecutive step indices (stride 14 for 31 steps).
func MakeHorizontalLineWipe(w, h, stride int) [][]byte {
	if stride <= 0 {
		stride = 14
	}
	grid := make([][]byte, h)
	for y := range h {
		idx := (y % wipeStepCount) * stride % wipeStepCount
		lo := wipeStepIndexToByte(idx)
		grid[y] = make([]byte, w)
		for x := range w {
			grid[y][x] = lo
		}
	}
	return grid
}

// MakeVerticalSweepWipe generates a left-to-right curtain: each column shares one lo
// step (vertical band), earliest at x=0 and latest at x=w-1.
func MakeVerticalSweepWipe(w, h int) [][]byte {
	grid := make([][]byte, h)
	denom := float64(w - 1)
	if w <= 1 {
		denom = 1
	}
	for y := range h {
		grid[y] = make([]byte, w)
		for x := range w {
			grid[y][x] = wipeStepAt(float64(x) / denom)
		}
	}
	return grid
}

// MakeLoLadderWipe maps a cols×rows grid of lo octets (0–7, 8–15, …, 248–255) with
// bandsPerCell horizontal bands stepping lo by 1 top-to-bottom inside each block.
// Default 4×8×8 tiles the full byte range 0…255.
func MakeLoLadderWipe(w, h, cols, rows, bandsPerCell int) [][]byte {
	if cols < 1 {
		cols = 4
	}
	if rows < 1 {
		rows = 8
	}
	if bandsPerCell < 1 {
		bandsPerCell = 8
	}
	cellW := w / cols
	cellH := h / rows
	if cellW < 1 {
		cellW = 1
	}
	if cellH < 1 {
		cellH = 1
	}
	grid := make([][]byte, h)
	for y := range h {
		grid[y] = make([]byte, w)
		row := y / cellH
		if row >= rows {
			row = rows - 1
		}
		band := y % cellH * bandsPerCell / cellH
		if band >= bandsPerCell {
			band = bandsPerCell - 1
		}
		for x := range w {
			col := x / cellW
			if col >= cols {
				col = cols - 1
			}
			idx := row*cols + col
			v := idx*bandsPerCell + band
			if v > 255 {
				v = 255
			}
			grid[y][x] = byte(v)
		}
	}
	return grid
}

// buildLoLadderPrimariesGrids fills a black field (hi=black, lo=248) with a cols×rows
// lattice. Each cell has bandsPerCell lo bands vertically and primaryCols ink columns
// horizontally (six primaries by default).
func buildLoLadderPrimariesGrids(w, h, cols, rows, bandsPerCell, primaryCols int) (hi, lo [][]byte) {
	if cols < 1 {
		cols = 4
	}
	if rows < 1 {
		rows = 8
	}
	if bandsPerCell < 1 {
		bandsPerCell = 8
	}
	if primaryCols < 1 {
		primaryCols = 6
	}
	const marginX, marginY = 48, 48
	gridW := w - 2*marginX
	gridH := h - 2*marginY
	if gridW < cols {
		gridW = w
	}
	if gridH < rows {
		gridH = h
	}
	cellW := gridW / cols
	cellH := gridH / rows
	if cellW < 1 {
		cellW = 1
	}
	if cellH < 1 {
		cellH = 1
	}
	hi = make([][]byte, h)
	lo = make([][]byte, h)
	for y := range h {
		hi[y] = make([]byte, w)
		lo[y] = make([]byte, w)
		for x := range w {
			hi[y][x] = hiBytes[0]
			lo[y][x] = 248
		}
	}
	for y := range h {
		for x := range w {
			mx, my := marginX, marginY
			if gridW == w {
				mx = 0
			}
			if gridH == h {
				my = 0
			}
			if x < mx || x >= mx+gridW || y < my || y >= my+gridH {
				continue
			}
			gx := x - mx
			gy := y - my
			col := gx / cellW
			row := gy / cellH
			if col >= cols || row >= rows {
				continue
			}
			cellX0 := col * cellW
			cellY0 := row * cellH
			localX := gx - cellX0
			localY := gy - cellY0
			band := localY * bandsPerCell / cellH
			if band >= bandsPerCell {
				band = bandsPerCell - 1
			}
			primary := localX * primaryCols / cellW
			if primary >= primaryCols {
				primary = primaryCols - 1
			}
			idx := row*cols + col
			v := idx*bandsPerCell + band
			if v > 255 {
				v = 255
			}
			hi[y][x] = hiBytes[primary]
			lo[y][x] = byte(v)
		}
	}
	return hi, lo
}

// BuildBlackUniform248Bin is a full-frame black image at terminal lo=248.
func BuildBlackUniform248Bin(w, h int) []byte {
	hi := make([][]byte, h)
	lo := make([][]byte, h)
	for y := range h {
		hi[y] = make([]byte, w)
		lo[y] = make([]byte, w)
		for x := range w {
			hi[y][x] = hiBytes[0]
			lo[y][x] = 248
		}
	}
	return ToBin(hi, lo)
}

// BuildLoLadderPrimariesBin is the InkJoy calibration .bin: black surround at lo=248
// with 8×4 cells of six primaries × lo 0…255.
func BuildLoLadderPrimariesBin(w, h int) []byte {
	hi, lo := buildLoLadderPrimariesGrids(w, h, 8, 4, 8, 6)
	return ToBin(hi, lo)
}

// MakeLuminanceLoWipe builds a topo-style lo grid from pre-DR L*.
// Bright regions start early (low lo); steep luminance ramps use more distinct lo steps.
func MakeLuminanceLoWipe(img image.Image) [][]byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	inLoL, inHiL := luminancePercentiles(img, 0.01, 0.99)
	if inHiL-inLoL < 0.5 {
		inHiL = inLoL + 0.5
	}

	lum := buildLABLGrid(img, w, h)
	grad := labGradientMagnitude(lum)
	flatG, steepG := labGradientPercentiles(grad, 0.20, 0.80)

	grid := make([][]byte, h)
	for y := 0; y < h; y++ {
		grid[y] = make([]byte, w)
		for x := 0; x < w; x++ {
			grid[y][x] = luminanceTopoLo(lum[y][x], grad[y][x], inLoL, inHiL, flatG, steepG)
		}
	}
	return grid
}

func buildLABLGrid(img image.Image, w, h int) [][]float64 {
	b := img.Bounds()
	lum := make([][]float64, h)
	for y := range h {
		lum[y] = make([]float64, w)
		for x := range w {
			lum[y][x] = pixelLABL(img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return lum
}

func pixelLABL(c color.Color) float64 {
	r8, g8, b8, _ := c.RGBA()
	rgb := [3]float64{float64(r8>>8) / 255.0, float64(g8>>8) / 255.0, float64(b8>>8) / 255.0}
	return srgbToLAB(rgb)[0]
}

func labGradientMagnitude(lum [][]float64) [][]float64 {
	h, w := len(lum), len(lum[0])
	grad := make([][]float64, h)
	for y := 0; y < h; y++ {
		grad[y] = make([]float64, w)
		for x := 0; x < w; x++ {
			L := lum[y][x]
			var maxD float64
			if x > 0 {
				maxD = math.Max(maxD, math.Abs(L-lum[y][x-1]))
			}
			if x+1 < w {
				maxD = math.Max(maxD, math.Abs(L-lum[y][x+1]))
			}
			if y > 0 {
				maxD = math.Max(maxD, math.Abs(L-lum[y-1][x]))
			}
			if y+1 < h {
				maxD = math.Max(maxD, math.Abs(L-lum[y+1][x]))
			}
			grad[y][x] = maxD
		}
	}
	return grad
}

func labGradientPercentiles(grad [][]float64, loPct, hiPct float64) (flat, steep float64) {
	const bins = 501
	hist := make([]int, bins)
	total := 0
	maxG := 0.0
	for _, row := range grad {
		for _, g := range row {
			if g > maxG {
				maxG = g
			}
		}
	}
	if maxG <= 0 {
		return 0, 1
	}
	scale := float64(bins-1) / maxG
	for _, row := range grad {
		for _, g := range row {
			bin := int(g * scale)
			if bin < 0 {
				bin = 0
			} else if bin >= bins {
				bin = bins - 1
			}
			hist[bin]++
			total++
		}
	}
	if total == 0 {
		return 0, 1
	}
	loCount := int(float64(total) * loPct)
	hiCount := int(float64(total) * hiPct)
	if hiCount >= total {
		hiCount = total - 1
	}
	cum := 0
	loBin, hiBin := 0, bins-1
	loFound := false
	for i, c := range hist {
		cum += c
		if !loFound && cum > loCount {
			loBin = i
			loFound = true
		}
		if cum >= hiCount {
			hiBin = i
			break
		}
	}
	return float64(loBin) / scale, float64(hiBin) / scale
}

func luminanceTopoLo(L, grad, inLo, inHi, flatG, steepG float64) byte {
	if inHi <= inLo {
		return 124
	}
	t := 1 - clamp01((L-inLo)/(inHi-inLo))
	g := 0.0
	if steepG > flatG {
		g = clamp01((grad - flatG) / (steepG - flatG))
	} else {
		g = 1
	}

	nSteps := 1 + int(math.Round(g*255))
	var q float64
	if nSteps > 1 {
		q = math.Round(t*float64(nSteps-1)) / float64(nSteps-1)
	} else {
		q = t
	}
	return quantizeLoByte(int(math.Round(q * 255)))
}

func clamp01(t float64) float64 {
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return t
}

func quantizeLoByte(idx int) byte {
	if idx < 0 {
		idx = 0
	} else if idx > 255 {
		idx = 255
	}
	if idx >= 248 {
		return 248
	}
	coarse := (idx / 8) * 8
	fine := idx % 8
	return byte(coarse + fine)
}

// MakeUniformWipe sets the same lo step on every pixel (full-panel transition, no spatial pattern).
func MakeUniformWipe(w, h int, lo byte) [][]byte {
	grid := make([][]byte, h)
	row := make([]byte, w)
	for x := range w {
		row[x] = lo
	}
	for y := range h {
		grid[y] = append([]byte(nil), row...)
	}
	return grid
}

// MakeCheckerboardWipe alternates lo timing on a tile grid (even tiles loA, odd loB).
// offsetX/offsetY shift the lo phase (half-tile offset crosses the hi checkerboard).
func MakeCheckerboardWipe(w, h, tileW, tileH, offsetX, offsetY int, loA, loB byte) [][]byte {
	if tileW < 1 {
		tileW = 1
	}
	if tileH < 1 {
		tileH = 1
	}
	grid := make([][]byte, h)
	for y := range h {
		grid[y] = make([]byte, w)
		ty := (y + offsetY) / tileH
		for x := range w {
			tx := (x + offsetX) / tileW
			if (tx+ty)%2 == 0 {
				grid[y][x] = loA
			} else {
				grid[y][x] = loB
			}
		}
	}
	return grid
}

// MakeClockWipe generates the lo-byte clock wipe pattern for a w×h frame.
// Square clock wipe: pixels sweep outward from center clockwise, quantized to 31 steps (0,8,…,248).
func MakeClockWipe(w, h int) [][]byte {
	grid := make([][]byte, h)
	cy := float64(h-1) / 2.0
	cx := float64(w-1) / 2.0
	maxD := math.Sqrt(cy*cy + cx*cx)

	for y := range h {
		grid[y] = make([]byte, w)
		dy := float64(y) - cy
		for x := range w {
			dx := float64(x) - cx
			angle := math.Atan2(dx, -dy)
			if angle < 0 {
				angle += 2 * math.Pi
			}
			dist := math.Sqrt(dy*dy+dx*dx) / maxD
			order := math.Mod(angle/(2*math.Pi)+dist*0.01, 1.0)
			step := int(order * 31)
			if step >= 31 {
				step = 30
			}
			grid[y][x] = byte(step * 8)
		}
	}
	return grid
}

// UniqueColors counts the number of distinct RGB colors in the image.
func UniqueColors(img image.Image) int {
	seen := make(map[uint32]struct{})
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bv, _ := img.At(x, y).RGBA()
			key := (r>>8)<<16 | (g>>8)<<8 | (bv >> 8)
			seen[uint32(key)] = struct{}{}
		}
	}
	return len(seen)
}

// StuckiDither applies Stucki error diffusion to img using the given palette.
// Returns a grid of palette indices (0-5).
// Kernel weights: A=8/42, B=4/42, C=2/42, D=1/42.
//
//	. . * A B
//	C B A B C
//	D C B C D
func StuckiDither(img image.Image, palette [6][3]float64, opts stuckiOptions) [][]byte {
	src := asRGBA(img)
	b := src.Bounds()
	h, w := b.Dy(), b.Dx()

	buf := make([][]float64, h+4)
	for y := range h + 4 {
		buf[y] = make([]float64, (w+4)*3)
	}
	var lum [][]float64
	if opts.EdgePreserve > 0 {
		lum = make([][]float64, h)
		for y := range h {
			lum[y] = make([]float64, w)
		}
	}
	// One Pix pass: load error buffer, source RGB for peach gating, optional luminance.
	srcRGB := make([][][3]float64, h)
	for y := range h {
		srcRGB[y] = make([][3]float64, w)
		row := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]
		for x := range w {
			i := x * 4
			r := float64(row[i])
			g := float64(row[i+1])
			bv := float64(row[i+2])
			srcRGB[y][x] = [3]float64{r, g, bv}
			base := (x + 2) * 3
			buf[y+2][base] = r
			buf[y+2][base+1] = g
			buf[y+2][base+2] = bv
			if lum != nil {
				lum[y][x] = 0.2126*r + 0.7152*g + 0.0722*bv
			}
		}
	}

	out := make([][]byte, h)
	for y := range h {
		out[y] = make([]byte, w)
		for x := range w {
			out[y][x] = 0xFF // not yet written (ink 0 is a real palette index)
		}
		by := y + 2
		rtl := opts.Serpentine && y&1 == 1
		step := func(x int) {
			bx := (x + 2) * 3
			pr := clamp255(buf[by][bx])
			pg := clamp255(buf[by][bx+1])
			pb := clamp255(buf[by][bx+2])
			rgb := [3]float64{pr, pg, pb}
			idx := nearestColorThresholdMod(rgb, srcRGB[y][x], palette, x, y, opts.ThresholdMod, out, rtl, opts.AntiRunBias)
			out[y][x] = byte(idx)
			stuckiSpreadError(buf, lum, h, w, by, x+2, x, y,
				pr-palette[idx][0], pg-palette[idx][1], pb-palette[idx][2],
				rtl, opts.EdgePreserve)
		}
		if rtl {
			for x := w - 1; x >= 0; x-- {
				step(x)
			}
			continue
		}
		for x := range w {
			step(x)
		}
	}
	return out
}

func stuckiSpreadError(buf [][]float64, lum [][]float64, h, w, by, xc, cx, cy int, er, eg, eb float64, rtl bool, edgePreserve float64) {
	const A, B, C, D = 8.0 / 42, 4.0 / 42, 2.0 / 42, 1.0 / 42
	centerLum := 0.0
	if lum != nil {
		centerLum = lum[cy][cx]
	}
	addErr := func(ry, rx int, wt float64) {
		if ry >= h+4 || rx < 0 || rx >= w+4 {
			return
		}
		if lum != nil && edgePreserve > 0 {
			nx, ny := rx-2, ry-2
			if nx >= 0 && nx < w && ny >= 0 && ny < h {
				wt *= stuckiEdgeAttenuation(centerLum, lum[ny][nx], edgePreserve)
			}
		}
		base := rx * 3
		buf[ry][base] += er * wt
		buf[ry][base+1] += eg * wt
		buf[ry][base+2] += eb * wt
	}
	if rtl {
		addErr(by, xc-1, A)
		addErr(by, xc-2, B)
		addErr(by+1, xc+2, C)
		addErr(by+1, xc+1, B)
		addErr(by+1, xc, A)
		addErr(by+1, xc-1, B)
		addErr(by+1, xc-2, C)
		addErr(by+2, xc+2, D)
		addErr(by+2, xc+1, C)
		addErr(by+2, xc, B)
		addErr(by+2, xc-1, C)
		addErr(by+2, xc-2, D)
		return
	}
	addErr(by, xc+1, A)
	addErr(by, xc+2, B)
	addErr(by+1, xc-2, C)
	addErr(by+1, xc-1, B)
	addErr(by+1, xc, A)
	addErr(by+1, xc+1, B)
	addErr(by+1, xc+2, C)
	addErr(by+2, xc-2, D)
	addErr(by+2, xc-1, C)
	addErr(by+2, xc, B)
	addErr(by+2, xc+1, C)
	addErr(by+2, xc+2, D)
}

func buildLuminanceGrid(img image.Image, h, w int) [][]float64 {
	src := asRGBA(img)
	b := src.Bounds()
	lum := make([][]float64, h)
	for y := range h {
		lum[y] = make([]float64, w)
		row := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]
		for x := range w {
			i := x * 4
			lum[y][x] = pixelLuminanceRGB(row[i], row[i+1], row[i+2])
		}
	}
	return lum
}

func pixelLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return pixelLuminanceRGB(uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

func pixelLuminanceRGB(r, g, b uint8) float64 {
	return 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)
}

// asRGBA returns img if it is already *image.RGBA; otherwise a drawn copy.
func asRGBA(img image.Image) *image.RGBA {
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba
	}
	return imageToRGBA(img)
}

// stuckiEdgeAttenuation scales error diffusion across an edge. Smooth neighbors
// get full transfer; hard edges (lightning, cloud boundary) get much less.
func stuckiEdgeAttenuation(lumA, lumB, preserve float64) float64 {
	if preserve <= 0 {
		return 1
	}
	diff := math.Abs(lumA - lumB)
	if diff <= stuckiEdgeSoft {
		return 1
	}
	if diff >= stuckiEdgeHard {
		return 1 - preserve
	}
	t := (diff - stuckiEdgeSoft) / (stuckiEdgeHard - stuckiEdgeSoft)
	return 1 - preserve*t
}

// StuckiTwoPalette runs error diffusion in displayPalette (P2) space after shared
// LAB/portrait/pre-dither preprocessing. Kernel is Stucki or Floyd–Steinberg.
// Map indices to send values with RenderIndicesToRGB (Samsung P1) or indicesToHi (InkJoy).
func StuckiTwoPalette(img image.Image, displayPalette [6][3]float64, pipe ColorPipeline, flatRGB bool, opts stuckiOptions) [][]byte {
	src := img
	if shouldApplyLABProcessing(pipe, img, flatRGB, opts.DynamicRange) {
		src = ApplyLABProcessing(img, pipe, displayPalette, opts.DynamicRange)
	}
	if shouldApplyInkAffinity(pipe, src, flatRGB) {
		src = ApplyInkAffinity(src, displayPalette, pipe.LABInkAffinityStrength, pipe.PortraitEnhance)
	}
	if shouldApplyInkAffinityMix(pipe, src, flatRGB) {
		src = ApplyInkAffinityMix(src, displayPalette, pipe.LABInkAffinityMixStrength, pipe.PortraitEnhance)
	}
	if pipe.PortraitEnhance && pipe.PortraitStrength > 0 && !flatRGB {
		src = ApplySkinToneProcessing(src, pipe.PortraitStrength)
	}
	if opts.PreDither && !flatRGB {
		noise := stuckiPreDitherNoise
		if opts.PreDitherStrength > 0 {
			noise = opts.PreDitherStrength
		}
		if noise > 0 {
			src = applyPreDitherNoise(src, noise)
		}
	}
	switch opts.Kernel {
	case ditherKernelFloydSteinberg:
		return FloydSteinbergDither(src, displayPalette)
	default:
		return StuckiDither(src, displayPalette, opts)
	}
}

// FloydSteinbergDither quantizes to palette with Floyd–Steinberg error diffusion
// (serpentine). Same 6-ink palette as Stucki — kernel only.
func FloydSteinbergDither(img image.Image, palette [6][3]float64) [][]byte {
	return errorDiffuseDither(img, palette, true, []errTap{
		{0, 1, 7 / 16.0},
		{1, -1, 3 / 16.0},
		{1, 0, 5 / 16.0},
		{1, 1, 1 / 16.0},
	})
}

// AtkinsonDither is a lighter-diffusion competitor (often grainier, less muddy).
func AtkinsonDither(img image.Image, palette [6][3]float64) [][]byte {
	return errorDiffuseDither(img, palette, true, []errTap{
		{0, 1, 1 / 8.0},
		{0, 2, 1 / 8.0},
		{1, -1, 1 / 8.0},
		{1, 0, 1 / 8.0},
		{1, 1, 1 / 8.0},
		{2, 0, 1 / 8.0},
	})
}

type errTap struct {
	dy, dx int
	w      float64
}

func errorDiffuseDither(img image.Image, palette [6][3]float64, serpentine bool, taps []errTap) [][]byte {
	src := asRGBA(img)
	b := src.Bounds()
	h, w := b.Dy(), b.Dx()
	buf := make([][]float64, h)
	for y := range h {
		buf[y] = make([]float64, w*3)
		row := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]
		for x := range w {
			i := x * 4
			buf[y][x*3] = float64(row[i])
			buf[y][x*3+1] = float64(row[i+1])
			buf[y][x*3+2] = float64(row[i+2])
		}
	}
	out := make([][]byte, h)
	for y := range h {
		out[y] = make([]byte, w)
		rtl := serpentine && y&1 == 1
		step := func(x int) {
			i := x * 3
			rgb := [3]float64{clamp255(buf[y][i]), clamp255(buf[y][i+1]), clamp255(buf[y][i+2])}
			idx := nearestColor(rgb, palette)
			out[y][x] = byte(idx)
			er := rgb[0] - palette[idx][0]
			eg := rgb[1] - palette[idx][1]
			eb := rgb[2] - palette[idx][2]
			for _, t := range taps {
				nx := x + t.dx
				if rtl {
					nx = x - t.dx
				}
				ny := y + t.dy
				if ny < 0 || ny >= h || nx < 0 || nx >= w {
					continue
				}
				j := nx * 3
				buf[ny][j] += er * t.w
				buf[ny][j+1] += eg * t.w
				buf[ny][j+2] += eb * t.w
			}
		}
		if rtl {
			for x := w - 1; x >= 0; x-- {
				step(x)
			}
			continue
		}
		for x := range w {
			step(x)
		}
	}
	return out
}

func applyPreDitherNoise(img image.Image, strength float64) *image.RGBA {
	src := asRGBA(img)
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewRGBA(b)

	// Luminance once (Pix), then gradient weight is neighbor lookups in that grid.
	lum := make([]float64, w*h)
	for y := 0; y < h; y++ {
		row := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]
		base := y * w
		for x := 0; x < w; x++ {
			i := x * 4
			lum[base+x] = pixelLuminanceRGB(row[i], row[i+1], row[i+2])
		}
	}

	for y := 0; y < h; y++ {
		srcRow := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]
		dstRow := out.Pix[out.PixOffset(b.Min.X, b.Min.Y+y):]
		py := b.Min.Y + y
		for x := 0; x < w; x++ {
			px := b.Min.X + x
			i := x * 4
			wt := preDitherGradientWeightLum(lum, w, h, x, y)
			r := float64(srcRow[i]) + preDitherNoiseSample(px, py, 0)*strength*wt
			g := float64(srcRow[i+1]) + preDitherNoiseSample(px, py, 1)*strength*wt
			bl := float64(srcRow[i+2]) + preDitherNoiseSample(px, py, 2)*strength*wt
			dstRow[i] = uint8(clamp255(r))
			dstRow[i+1] = uint8(clamp255(g))
			dstRow[i+2] = uint8(clamp255(bl))
			dstRow[i+3] = srcRow[i+3]
		}
	}
	return out
}

// preDitherGradientWeight is 0 on flat regions and sharp edges, peaking on smooth
// gradients where Stucki banding is most visible (sky, water, soft shadows).
func preDitherGradientWeight(img image.Image, x, y int) float64 {
	src := asRGBA(img)
	b := src.Bounds()
	// x,y are absolute image coords (same as historical At-based API).
	lx, ly := x-b.Min.X, y-b.Min.Y
	w, h := b.Dx(), b.Dy()
	if lx < 0 || ly < 0 || lx >= w || ly >= h {
		return 0
	}
	lum := make([]float64, w*h)
	for yy := 0; yy < h; yy++ {
		row := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+yy):]
		base := yy * w
		for xx := 0; xx < w; xx++ {
			i := xx * 4
			lum[base+xx] = pixelLuminanceRGB(row[i], row[i+1], row[i+2])
		}
	}
	return preDitherGradientWeightLum(lum, w, h, lx, ly)
}

func preDitherGradientWeightLum(lum []float64, w, h, x, y int) float64 {
	const flatCutoff = 0.75
	const fullGrad = 10.0
	const edgeCutoff = 28.0

	c := lum[y*w+x]
	var spread float64
	for _, o := range [][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}} {
		nx, ny := x+o[0], y+o[1]
		if nx < 0 || nx >= w || ny < 0 || ny >= h {
			continue
		}
		d := math.Abs(lum[ny*w+nx] - c)
		if d > spread {
			spread = d
		}
	}
	if spread <= flatCutoff || spread >= edgeCutoff {
		return 0
	}
	wt := (spread - flatCutoff) / (fullGrad - flatCutoff)
	if wt > 1 {
		return 1
	}
	if wt < 0 {
		return 0
	}
	return wt
}

func preDitherNoiseSample(x, y, c int) float64 {
	n := uint32(x*73856093 ^ y*19349663 ^ c*83492791)
	n ^= n << 13
	n ^= n >> 17
	n ^= n << 5
	return float64(n%1000)/499.5 - 1.0
}

// RenderIndicesToRGB writes sendPalette (P1) RGB for each ink index.
func RenderIndicesToRGB(indices [][]byte, sendPalette [6][3]float64) image.Image {
	return renderIndicesToRGB(indices, sendPalette)
}

// RemapSamsungSendPNGToDisplay rewrites a P1 send PNG to P2 display colors for hub preview.
func RemapSamsungSendPNGToDisplay(pngData []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r8, g8, b8, a8 := img.At(x, y).RGBA()
			r, g, bv := uint8(r8>>8), uint8(g8>>8), uint8(b8>>8)
			idx := samsungSendIndexForRGB(r, g, bv)
			p := PaletteSamsungDisplay[idx]
			out.SetRGBA(x, y, color.RGBA{
				R: uint8(p[0]), G: uint8(p[1]), B: uint8(p[2]), A: uint8(a8 >> 8),
			})
		}
	}
	return encodePNG(out), nil
}

func samsungSendIndexForRGB(r, g, b uint8) int {
	for i, c := range PaletteSamsungSend {
		if uint8(c[0]) == r && uint8(c[1]) == g && uint8(c[2]) == b {
			return i
		}
	}
	return nearestColor([3]float64{float64(r), float64(g), float64(b)}, PaletteSamsungSend)
}

// ApplyLABProcessing applies optional LAB chroma, highlight rolloff, and/or shadow lift before dithering.
// displayPalette sets the black/white L* targets for dynamic-range fitting (P2 inks for the target frame).
func ApplyLABProcessing(img image.Image, pipe ColorPipeline, displayPalette [6][3]float64, fitDynamicRange bool) image.Image {
	working := img
	if fitDynamicRange && pipe.LABDynamicRangeStops > 0 {
		outLoL, outHiL := displayPaletteDRRange(displayPalette)
		working = applyLABDynamicRangeFit(working, pipe, outLoL, outHiL)
	}
	if !pipe.LABChromaEnabled && !pipe.LABHighlightEnabled && !pipe.LABShadowEnabled {
		return working
	}

	b := working.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r8, g8, b8, a8 := working.At(x, y).RGBA()
			rgb := [3]float64{float64(r8>>8) / 255.0, float64(g8>>8) / 255.0, float64(b8>>8) / 255.0}
			lab := srgbToLAB(rgb)

			if pipe.LABChromaEnabled && pipe.LABChromaStrength > 0 {
				chroma := 1.0 + 0.3*pipe.LABChromaStrength
				lab[1] *= chroma
				lab[2] *= chroma
			}

			if pipe.LABHighlightEnabled && pipe.LABHighlightStrength > 0 {
				lab[0] = applyLABHighlightTone(lab, pipe.LABHighlightStrength)
			}

			if pipe.LABShadowEnabled && pipe.LABShadowStrength > 0 {
				L := lab[0]
				t := (25.0 - L) / 25.0
				if t < 0 {
					t = 0
				} else if t > 1 {
					t = 1
				}
				lift := t * t * (3.0 - 2.0*t)
				lift = shadowLiftOnSkin(lab, lift, pipe.PortraitEnhance)
				lab[0] = L + lift*20.0*pipe.LABShadowStrength
			}

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

// applyLABDynamicRangeFit compresses scene luminance into a log range of N stops,
// anchored at 1st/99th percentile L* and mapped to the panel black/white range.
func applyLABDynamicRangeFit(img image.Image, pipe ColorPipeline, outLoL, outHiL float64) image.Image {
	b := img.Bounds()
	inLoL, inHiL := luminancePercentiles(img, 0.01, 0.99)
	if inHiL-inLoL < 0.5 {
		inHiL = inLoL + 0.5
	}
	stops := pipe.LABDynamicRangeStops
	if stops <= 0 {
		stops = 4
	}
	if outHiL <= outLoL {
		outLoL, outHiL = 12, 75
	}

	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r8, g8, b8, a8 := img.At(x, y).RGBA()
			rgb := [3]float64{float64(r8>>8) / 255.0, float64(g8>>8) / 255.0, float64(b8>>8) / 255.0}
			lab := srgbToLAB(rgb)
			lab[0] = mapLABDynamicRange(lab[0], inLoL, inHiL, outLoL, outHiL, stops)
			mapped := labToSRGB(lab)
			out.SetRGBA(x, y, color.RGBA{
				R: floatTo8(mapped[0]),
				G: floatTo8(mapped[1]),
				B: floatTo8(mapped[2]),
				A: uint8(a8 >> 8),
			})
		}
	}
	return out
}

func luminancePercentiles(img image.Image, loPct, hiPct float64) (loL, hiL float64) {
	const scale = 10
	const bins = 1001
	hist := make([]int, bins)
	b := img.Bounds()
	total := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r8, g8, b8, _ := img.At(x, y).RGBA()
			rgb := [3]float64{float64(r8>>8) / 255.0, float64(g8>>8) / 255.0, float64(b8>>8) / 255.0}
			L := srgbToLAB(rgb)[0]
			bin := int(L * scale)
			if bin < 0 {
				bin = 0
			} else if bin >= bins {
				bin = bins - 1
			}
			hist[bin]++
			total++
		}
	}
	if total == 0 {
		return 0, 100
	}
	loCount := int(float64(total) * loPct)
	hiCount := int(float64(total) * hiPct)
	if hiCount >= total {
		hiCount = total - 1
	}
	cum := 0
	loBin, hiBin := 0, bins-1
	loFound := false
	for i, c := range hist {
		cum += c
		if !loFound && cum > loCount {
			loBin = i
			loFound = true
		}
		if cum >= hiCount {
			hiBin = i
			break
		}
	}
	return float64(loBin) / scale, float64(hiBin) / scale
}

func mapLABDynamicRange(L, inLoL, inHiL, outLoL, outHiL, stops float64) float64 {
	inLoY := labLToLinearY(inLoL)
	inHiY := labLToLinearY(inHiL)
	if inHiY <= inLoY*1.001 {
		inHiY = inLoY * 1.001
	}
	outLoY := labLToLinearY(outLoL)
	outHiY := labLToLinearY(outHiL)
	outSpanY := outLoY * math.Pow(2, stops)
	if outSpanY > outHiY {
		outSpanY = outHiY
	}
	if outSpanY <= outLoY*1.001 {
		outSpanY = outLoY * 1.001
	}

	Y := labLToLinearY(L)
	logInLo := math.Log2(inLoY)
	logInHi := math.Log2(inHiY)
	denom := logInHi - logInLo
	if denom < 1e-6 {
		return outLoL + (outHiL-outLoL)*(L-inLoL)/(inHiL-inLoL)
	}
	t := (math.Log2(math.Max(Y, inLoY)) - logInLo) / denom
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	outY := outLoY * math.Pow(outSpanY/outLoY, t)
	return linearYToLabL(outY)
}

func labLToLinearY(L float64) float64 {
	fy := (L + 16.0) / 116.0
	if fy <= 0 {
		return 1e-8
	}
	return fy * fy * fy
}

func linearYToLabL(Y float64) float64 {
	if Y <= 0 {
		return 0
	}
	return 116.0*math.Cbrt(Y) - 16.0
}

// displayPaletteDRRange maps scene DR to neutral gray lightness at each ink's luminance
// (avoids chromatic black/white L* skewing neutrals toward green on InkJoy).
func displayPaletteDRRange(pal [6][3]float64) (lo, hi float64) {
	lo = neutralGrayLABL(pal[0])
	hi = neutralGrayLABL(pal[1])
	if hi <= lo {
		return 12, 75
	}
	return lo, hi
}

func neutralGrayLABL(rgb255 [3]float64) float64 {
	lin := [3]float64{
		srgbUnitToLinear(rgb255[0] / 255),
		srgbUnitToLinear(rgb255[1] / 255),
		srgbUnitToLinear(rgb255[2] / 255),
	}
	Y := 0.2126*lin[0] + 0.7152*lin[1] + 0.0722*lin[2]
	return linearYToLabL(Y)
}

func srgbUnitToLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func labLSpanStops(loL, hiL float64) float64 {
	loY := labLToLinearY(loL)
	hiY := labLToLinearY(hiL)
	if loY <= 0 || hiY <= loY {
		return 0
	}
	return math.Log2(hiY / loY)
}

// applyLABHighlightTone compresses bright blue skies so they dither with color instead
// of blowing to white, then darkens blue reflections slightly. Neutral or mid-bright
// highlights (snow, mountain rock) are left alone — only L≥58 blues get sky compression.
func applyLABHighlightTone(lab [3]float64, strength float64) float64 {
	if strength <= 0 {
		return lab[0]
	}
	out := lab[0]
	orig := lab[0]

	if sky := highlightSkyWeight(lab); sky > 0 {
		effective := strength * sky
		const knee = 48.0
		if out > knee {
			excess := out - knee
			keep := 1.0 - 1.0*effective
			if keep < 0.08 {
				keep = 0.08
			}
			out = knee + excess*keep
		}
		if orig > 58 {
			t := (orig - 58.0) / 36.0
			if t > 1 {
				t = 1
			}
			shade := t * t * (3.0 - 2.0*t)
			out -= shade * 11.0 * effective
		}
	}

	if water := highlightWaterWeight(lab); water > 0 {
		effective := strength * water
		t := (orig - 34.0) / 18.0
		if t > 1 {
			t = 1
		}
		reflShade := t * (1.0 - t) * 4.0
		out -= reflShade * effective
	}

	if out < 0 {
		return 0
	}
	return out
}

// highlightSkyWeight selects bright blue sky (high L, strong negative b*).
func highlightSkyWeight(lab [3]float64) float64 {
	L, a, b := lab[0], lab[1], lab[2]
	if L < 58 {
		return 0
	}
	chroma := math.Hypot(a, b)
	if chroma < 8 || b >= -10 {
		return 0
	}
	blue := -b / 30.0
	if blue > 1 {
		blue = 1
	}
	warm := a / 25.0
	if warm < 0 {
		warm = 0
	}
	if warm > 1 {
		warm = 1
	}
	w := blue * (1.0 - warm*0.5)
	chromaW := (chroma - 8.0) / 30.0
	if chromaW > 1 {
		chromaW = 1
	}
	w *= chromaW
	if w < 0 {
		return 0
	}
	if w > 1 {
		return 1
	}
	return w
}

// highlightWaterWeight selects blue reflections and lower sky tones (L≈34–52).
func highlightWaterWeight(lab [3]float64) float64 {
	L, a, b := lab[0], lab[1], lab[2]
	if L < 34 || L > 52 {
		return 0
	}
	if b >= -5 {
		return 0
	}
	chroma := math.Hypot(a, b)
	if chroma < 8 {
		return 0
	}
	blue := -b / 25.0
	if blue > 1 {
		blue = 1
	}
	return blue * 0.85
}

// skyBlueWeight is the max of sky and water weights (for analysis/logging).
func skyBlueWeight(lab [3]float64) float64 {
	s := highlightSkyWeight(lab)
	w := highlightWaterWeight(lab)
	if w > s {
		return w
	}
	return s
}

// ── helpers ──────────────────────────────────────────────────────────────────

func nearestColor(rgb [3]float64, palette [6][3]float64) int {
	best, bestDist := 0, math.MaxFloat64
	for i, c := range palette {
		dr := rgb[0] - c[0]
		dg := rgb[1] - c[1]
		db := rgb[2] - c[2]
		d := dr*dr + dg*dg + db*db
		if d < bestDist {
			bestDist = d
			best = i
		}
	}
	return best
}

// nearestColorThresholdMod is a multi-level Zhou–Fang threshold modulation:
// choose between the two nearest inks by comparing the projection along their
// RGB axis to a midtone-jittered threshold (0.5 ± noise). When antiRunBias > 0,
// already-written neighbors further nudge toward the second ink, and a hard
// run cap forces a break once a mono streak gets long. Peach/skin gating uses
// srcRGB (original pixel), not the error-diffused sample — otherwise anti-run
// disables itself inside yellow face blotches. Diffusion error still uses the
// unmodulated sample (caller responsibility).
func nearestColorThresholdMod(rgb, srcRGB [3]float64, palette [6][3]float64, x, y int, strength float64, out [][]byte, rtl bool, antiRunBias float64) int {
	if strength <= 0 && antiRunBias <= 0 {
		return nearestColor(rgb, palette)
	}
	best, second := 0, -1
	bestD, secondD := math.MaxFloat64, math.MaxFloat64
	for i, c := range palette {
		dr := rgb[0] - c[0]
		dg := rgb[1] - c[1]
		db := rgb[2] - c[2]
		d := dr*dr + dg*dg + db*db
		if d < bestD {
			second, secondD = best, bestD
			best, bestD = i, d
			continue
		}
		if d < secondD {
			second, secondD = i, d
		}
	}
	if second < 0 || secondD >= math.MaxFloat64/2 {
		return best
	}
	srcLum := 0.2126*srcRGB[0] + 0.7152*srcRGB[1] + 0.0722*srcRGB[2]
	mid := stuckiMidtoneModWeight(srcLum)
	peach := stuckiPeachWeight(srcRGB)
	if mid < 0.04 && antiRunBias <= 0 {
		return best
	}
	b := palette[best]
	s := palette[second]
	vx, vy, vz := s[0]-b[0], s[1]-b[1], s[2]-b[2]
	len2 := vx*vx + vy*vy + vz*vz
	if len2 < 1 {
		return best
	}
	// t = 0 at best ink, 1 at second-nearest.
	t := ((rgb[0]-b[0])*vx + (rgb[1]-b[1])*vy + (rgb[2]-b[2])*vz) / len2

	thr := 0.5
	if strength > 0 && mid > 0 {
		amp := (strength / 50.0) * mid
		if peach > 0.02 {
			amp += stuckiPeachThresholdExtra * peach
		}
		thr += preDitherNoiseSample(x, y, 3) * amp
	}
	if antiRunBias > 0 && out != nil && peach > 0.02 {
		dom := stuckiCausalInkDominance(out, x, y, best, stuckiAntiRunRadius, rtl)
		run := stuckiScanMonoRun(out, x, y, best, rtl)
		if v := stuckiVertMonoRun(out, x, y, best); v > run {
			run = v
		}
		runFrac := float64(run) / float64(stuckiMaxMonoRun)
		if runFrac > 1 {
			runFrac = 1
		}
		push := dom
		if runFrac > push {
			push = runFrac
		}
		thr -= antiRunBias * push * peach
		if thr < 0.2 {
			thr = 0.2
		}
	}
	cand := best
	if t >= thr {
		cand = second
	}
	// Mild hard break only for long mono runs of the nearest ink — soft bias
	// handles most island pressure without carving teal/peach region borders.
	if antiRunBias > 0 && out != nil && peach > 0.08 && cand == best {
		run := stuckiImageMonoRun(out, x, y, best)
		if run >= stuckiMaxMonoRun {
			return second
		}
	}
	return cand
}

// stuckiImageMonoRun is the longer of: horizontal run across already-written
// neighbors on this row (both directions — whichever side exists), and vertical
// run above. This matches blotches as seen on the frame, not just scan order.
func stuckiImageMonoRun(out [][]byte, x, y, ink int) int {
	hRun := stuckiScanMonoRun(out, x, y, ink, false) // left
	if r := stuckiScanMonoRun(out, x, y, ink, true); r > hRun {
		hRun = r // right (written on RTL rows)
	}
	vRun := stuckiVertMonoRun(out, x, y, ink)
	if vRun > hRun {
		return vRun
	}
	return hRun
}

// stuckiScanMonoRun counts how many consecutive already-written pixels behind
// the scan head match ink (horizontal only). 0xFF marks unwritten.
func stuckiScanMonoRun(out [][]byte, x, y, ink int, rtl bool) int {
	h := len(out)
	if h == 0 || y < 0 || y >= h {
		return 0
	}
	w := len(out[0])
	run := 0
	if rtl {
		for nx := x + 1; nx < w; nx++ {
			v := out[y][nx]
			if v == 0xFF || int(v) != ink {
				break
			}
			run++
			if run >= stuckiMaxMonoRun+2 {
				break
			}
		}
		return run
	}
	for nx := x - 1; nx >= 0; nx-- {
		v := out[y][nx]
		if v == 0xFF || int(v) != ink {
			break
		}
		run++
		if run >= stuckiMaxMonoRun+2 {
			break
		}
	}
	return run
}

// stuckiVertMonoRun counts same-ink pixels directly above (always causal).
func stuckiVertMonoRun(out [][]byte, x, y, ink int) int {
	h := len(out)
	if h == 0 || y <= 0 || x < 0 {
		return 0
	}
	w := len(out[0])
	if x >= w {
		return 0
	}
	run := 0
	for ny := y - 1; ny >= 0; ny-- {
		v := out[ny][x]
		if v == 0xFF || int(v) != ink {
			break
		}
		run++
		if run >= stuckiMaxMonoRun+2 {
			break
		}
	}
	return run
}

// stuckiCausalInkDominance is the fraction of already-written neighbors within
// radius that match ink. Only causal pixels are counted (respects serpentine).
func stuckiCausalInkDominance(out [][]byte, x, y, ink, radius int, rtl bool) float64 {
	h := len(out)
	if h == 0 || radius <= 0 {
		return 0
	}
	w := len(out[0])
	count, total := 0, 0
	for dy := -radius; dy <= 0; dy++ {
		ny := y + dy
		if ny < 0 || ny >= h {
			continue
		}
		for dx := -radius; dx <= radius; dx++ {
			if dy == 0 {
				if rtl {
					// Serpentine RTL: higher x already written.
					if dx <= 0 {
						continue
					}
				} else if dx >= 0 {
					// LTR: lower x already written.
					continue
				}
			}
			nx := x + dx
			if nx < 0 || nx >= w {
				continue
			}
			v := out[ny][nx]
			if v == 0xFF {
				continue
			}
			total++
			if int(v) == ink {
				count++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(count) / float64(total)
}

// stuckiMidtoneModWeight peaks across midtones for mild Zhou–Fang jitter.
func stuckiMidtoneModWeight(lum float64) float64 {
	// Smooth bump: 0 at L≤15 / L≥245, ~1 around L=90–185.
	if lum <= 15 || lum >= 245 {
		return 0
	}
	var t float64
	switch {
	case lum < 90:
		t = (lum - 15) / 75
	case lum > 185:
		t = (245 - lum) / 60
	default:
		return 1
	}
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return t * t * (3 - 2*t)
}

// Peach/skin locus center in LAB (approx RGB 210,160,140). Anti-run uses a
// fairly wide neighborhood around that center so cheek/shadow/highlight peach
// can join blends; weight still drops toward zero off the skin locus.
const (
	stuckiPeachL      = 68.0
	stuckiPeachA      = 20.0
	stuckiPeachB      = 22.0
	stuckiPeachSigmaL = 24.0
	stuckiPeachSigmaC = 20.0
)

// stuckiPeachWeight is ~1 on face peach and falls off as a wide Gaussian in LAB
// so pixels away from the exact center still get blend permission.
func stuckiPeachWeight(rgb [3]float64) float64 {
	lab := srgbToLAB(paletteRGB01(rgb))
	dL := (lab[0] - stuckiPeachL) / stuckiPeachSigmaL
	da := (lab[1] - stuckiPeachA) / stuckiPeachSigmaC
	db := (lab[2] - stuckiPeachB) / stuckiPeachSigmaC
	r2 := dL*dL + da*da + db*db
	w := math.Exp(-r2)
	if w < 0.02 {
		return 0
	}
	return w
}

func clamp255(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func floatTo8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v * 255)
}

var xyzD65 = [3]float64{0.95047, 1.00000, 1.08883}
var rgbToXYZ = [3][3]float64{
	{0.4124564, 0.3575761, 0.1804375},
	{0.2126729, 0.7151522, 0.0721750},
	{0.0193339, 0.1191920, 0.9503041},
}
var xyzToRGB = [3][3]float64{
	{3.2404542, -1.5371385, -0.4985314},
	{-0.9692660, 1.8760108, 0.0415560},
	{0.0556434, -0.2040259, 1.0572252},
}

func srgbToLAB(rgb [3]float64) [3]float64 {
	lin := [3]float64{}
	for i, v := range rgb {
		if v <= 0.04045 {
			lin[i] = v / 12.92
		} else {
			lin[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	xyz := [3]float64{}
	for i := range 3 {
		for j := range 3 {
			xyz[i] += rgbToXYZ[i][j] * lin[j]
		}
		xyz[i] /= xyzD65[i]
	}
	f := [3]float64{}
	for i, v := range xyz {
		if v > 0.008856 {
			f[i] = math.Cbrt(v)
		} else {
			f[i] = 7.787*v + 16.0/116
		}
	}
	return [3]float64{
		116*f[1] - 16,
		500 * (f[0] - f[1]),
		200 * (f[1] - f[2]),
	}
}

func labToSRGB(lab [3]float64) [3]float64 {
	fy := (lab[0] + 16) / 116
	fx := lab[1]/500 + fy
	fz := fy - lab[2]/200
	xyz := [3]float64{}
	for i, f := range [3]float64{fx, fy, fz} {
		if f > 0.206897 {
			xyz[i] = f * f * f
		} else {
			xyz[i] = (f - 16.0/116) / 7.787
		}
		xyz[i] *= xyzD65[i]
	}
	lin := [3]float64{}
	for i := range 3 {
		for j := range 3 {
			lin[i] += xyzToRGB[i][j] * xyz[j]
		}
		if lin[i] < 0 {
			lin[i] = 0
		} else if lin[i] > 1 {
			lin[i] = 1
		}
	}
	out := [3]float64{}
	for i, v := range lin {
		if v <= 0.0031308 {
			out[i] = 12.92 * v
		} else {
			out[i] = 1.055*math.Pow(v, 1.0/2.4) - 0.055
		}
		if out[i] < 0 {
			out[i] = 0
		} else if out[i] > 1 {
			out[i] = 1
		}
	}
	return out
}

// nativePalette builds the 192-entry color.Palette for native PNG encoding.
func nativePalette() color.Palette {
	pal := make(color.Palette, 256)
	for hiIdx, hiVal := range hiBytes {
		for loStep := range 32 {
			loVal := byte(loStep * 8)
			if loStep == 31 {
				loVal = 248
			}
			pal[hiIdx*32+loStep] = color.RGBA{R: hiVal, G: loVal, B: 0, A: 255}
		}
	}
	// Fill remaining entries with black
	for i := 192; i < 256; i++ {
		pal[i] = color.RGBA{A: 255}
	}
	return pal
}

// EncodePNG encodes an image to PNG bytes.
func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}
