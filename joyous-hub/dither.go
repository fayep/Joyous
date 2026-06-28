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

// PaletteInkJoyDisplay (P2) — on-panel Stucki targets (IMG_0110 primaries; green uses legacy
// physical ink so mid-tone browns dither to red/yellow/black, not measured sage green).
var PaletteInkJoyDisplay = [6][3]float64{
	{71, 38, 47},
	{214, 215, 201},
	{222, 205, 0},
	{164, 15, 5},
	{30, 106, 188},
	{46, 91, 65},
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

// stuckiEdgePreserve limits how much quantization error crosses strong luminance
// edges (lightning vs cloud, tonal steps in gray clouds). 0 = no attenuation.
const stuckiEdgePreserve = 0.72

const (
	stuckiEdgeSoft = 3.0  // below: full error diffusion
	stuckiEdgeHard = 22.0 // above: minimum transfer across the edge
)

// stuckiOptions selects Stucki tuning; InkJoy uses the same diffusion quality as Samsung.
type stuckiOptions struct {
	Serpentine         bool
	EdgePreserve       float64
	PreDither          bool
	PreDitherStrength  float64
	DynamicRange       bool
}

func stuckiOptionsSamsung(pipe ColorPipeline) stuckiOptions {
	return stuckiOptions{
		Serpentine:   true,
		EdgePreserve: stuckiEdgePreserve,
		PreDither:    true,
		DynamicRange: pipe.LABDynamicRangeEnabled && !pipe.PortraitEnhance,
	}
}

func stuckiOptionsInkJoy(pipe ColorPipeline) stuckiOptions {
	return stuckiOptionsSamsung(pipe)
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
	b := img.Bounds()
	h, w := b.Dy(), b.Dx()

	buf := make([][]float64, h+4)
	for y := range h + 4 {
		buf[y] = make([]float64, (w+4)*3)
	}
	for y := range h {
		for x := range w {
			r, g, bv, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			base := (x + 2) * 3
			buf[y+2][base] = float64(r >> 8)
			buf[y+2][base+1] = float64(g >> 8)
			buf[y+2][base+2] = float64(bv >> 8)
		}
	}
	var lum [][]float64
	if opts.EdgePreserve > 0 {
		lum = buildLuminanceGrid(img, h, w)
	}

	out := make([][]byte, h)
	for y := range h {
		out[y] = make([]byte, w)
		by := y + 2
		rtl := opts.Serpentine && y&1 == 1
		if rtl {
			for x := w - 1; x >= 0; x-- {
				bx := (x + 2) * 3
				pr := clamp255(buf[by][bx])
				pg := clamp255(buf[by][bx+1])
				pb := clamp255(buf[by][bx+2])

				idx := nearestColor([3]float64{pr, pg, pb}, palette)
				out[y][x] = byte(idx)

				er := pr - palette[idx][0]
				eg := pg - palette[idx][1]
				eb := pb - palette[idx][2]
				stuckiSpreadError(buf, lum, h, w, by, x+2, x, y, er, eg, eb, true, opts.EdgePreserve)
			}
			continue
		}
		for x := range w {
			bx := (x + 2) * 3
			pr := clamp255(buf[by][bx])
			pg := clamp255(buf[by][bx+1])
			pb := clamp255(buf[by][bx+2])

			idx := nearestColor([3]float64{pr, pg, pb}, palette)
			out[y][x] = byte(idx)

			er := pr - palette[idx][0]
			eg := pg - palette[idx][1]
			eb := pb - palette[idx][2]
			stuckiSpreadError(buf, lum, h, w, by, x+2, x, y, er, eg, eb, false, opts.EdgePreserve)
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
	b := img.Bounds()
	lum := make([][]float64, h)
	for y := range h {
		lum[y] = make([]float64, w)
		for x := range w {
			lum[y][x] = pixelLuminance(img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return lum
}

func pixelLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return 0.2126*float64(r>>8) + 0.7152*float64(g>>8) + 0.0722*float64(b>>8)
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

// StuckiTwoPalette runs Stucki error diffusion in displayPalette (P2) space.
// Map indices to send values with RenderIndicesToRGB (Samsung P1) or indicesToHi (InkJoy).
func StuckiTwoPalette(img image.Image, displayPalette [6][3]float64, pipe ColorPipeline, flatRGB bool, opts stuckiOptions) [][]byte {
	src := img
	if shouldApplyLABProcessing(pipe, img, flatRGB, opts.DynamicRange) {
		src = ApplyLABProcessing(img, pipe, displayPalette, opts.DynamicRange)
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
	return StuckiDither(src, displayPalette, opts)
}

func applyPreDitherNoise(img image.Image, strength float64) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r8, g8, b8, a8 := img.At(x, y).RGBA()
			w := preDitherGradientWeight(img, x, y)
			r := float64(r8>>8) + preDitherNoiseSample(x, y, 0)*strength*w
			g := float64(g8>>8) + preDitherNoiseSample(x, y, 1)*strength*w
			bl := float64(b8>>8) + preDitherNoiseSample(x, y, 2)*strength*w
			out.SetRGBA(x, y, color.RGBA{
				R: uint8(clamp255(r)),
				G: uint8(clamp255(g)),
				B: uint8(clamp255(bl)),
				A: uint8(a8 >> 8),
			})
		}
	}
	return out
}

// preDitherGradientWeight is 0 on flat regions and sharp edges, peaking on smooth
// gradients where Stucki banding is most visible (sky, water, soft shadows).
func preDitherGradientWeight(img image.Image, x, y int) float64 {
	const flatCutoff = 0.75
	const fullGrad = 10.0
	const edgeCutoff = 28.0

	lum := func(px, py int) float64 {
		return pixelLuminance(img.At(px, py))
	}
	c := lum(x, y)
	b := img.Bounds()
	var spread float64
	for _, o := range [][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}} {
		nx, ny := x+o[0], y+o[1]
		if nx < b.Min.X || nx >= b.Max.X || ny < b.Min.Y || ny >= b.Max.Y {
			continue
		}
		d := math.Abs(lum(nx, ny) - c)
		if d > spread {
			spread = d
		}
	}
	if spread <= flatCutoff || spread >= edgeCutoff {
		return 0
	}
	w := (spread - flatCutoff) / (fullGrad - flatCutoff)
	if w > 1 {
		return 1
	}
	if w < 0 {
		return 0
	}
	return w
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
