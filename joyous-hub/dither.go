package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"bytes"
)

// PaletteInkJoy holds the 6 InkJoy ink colors in RGB float64 (reverse-engineered from ISFR-lite).
var PaletteInkJoy = [6][3]float64{
	{30, 30, 30},
	{149, 162, 165},
	{166, 165, 17},
	{121, 23, 17},
	{0, 76, 136},
	{46, 91, 65},
}

// PaletteSamsung holds sRGB monitor equivalents for Samsung EM32DX (2560×1440).
var PaletteSamsung = [6][3]float64{
	{0, 0, 0},
	{255, 255, 255},
	{255, 235, 0},
	{154, 0, 0},
	{0, 36, 154},
	{20, 85, 16},
}

const samsungW, samsungH = 2560, 1440

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
func StuckiDither(img image.Image, palette [6][3]float64) [][]byte {
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

	const A, B, C, D = 8.0 / 42, 4.0 / 42, 2.0 / 42, 1.0 / 42

	out := make([][]byte, h)
	for y := range h {
		out[y] = make([]byte, w)
		by := y + 2
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

			addErr := func(ry, rx int, wt float64) {
				if ry >= h+4 || rx < 0 || rx >= w+4 {
					return
				}
				base := rx * 3
				buf[ry][base] += er * wt
				buf[ry][base+1] += eg * wt
				buf[ry][base+2] += eb * wt
			}
			xc := x + 2
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
	}
	return out
}

// LABEnhance applies LAB chroma boost and highlight rolloff before dithering.
// strength=1.0 matches observed server processing (chroma ×1.3, highlight rolloff).
func LABEnhance(img image.Image, strength float64) image.Image {
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r8, g8, b8, a8 := img.At(x, y).RGBA()
			rgb := [3]float64{float64(r8>>8) / 255.0, float64(g8>>8) / 255.0, float64(b8>>8) / 255.0}
			lab := srgbToLAB(rgb)

			// Chroma boost
			chroma := 1.0 + 0.3*strength
			lab[1] *= chroma
			lab[2] *= chroma

			// Highlight rolloff: smoothstep on L>75
			L := lab[0]
			t := (L - 75.0) / 25.0
			if t < 0 {
				t = 0
			} else if t > 1 {
				t = 1
			}
			rolloff := t * t * (3.0 - 2.0*t)
			lab[0] = L - rolloff*20.0*strength

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
