package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/pierrec/lz4/v4"
)

// InkJoy 28.5" (H:3) encoder — produces an IJI3 RAW8|LZ4 playback container
// for the 2160x3060, single-plane 8bpp panel, reusing the hub's shared color
// pipeline (StuckiTwoPalette: LAB tone-map + ink-affinity + dither) so photos
// get the same tone handling as the 10" path. This file is hardware-agnostic
// (it never tests dev.HardwareModel) — the decision to call it is gated by
// body.InkJoy28, set bridge-side in bridgeDeliverInkJoyImage, mirroring the
// body.NativeDims convention.
//
// The panel scans two source rows at once. Read as big-endian 16-bit words, the
// raw plane is a plain row-major 2160x1530 grid: each word holds a vertical
// pixel pair, upper row in the MSB, with X flipped (panel origin is top-right):
//
//	word[i*2160 + c] = src[2i][2159-c]<<8 | src[2i+1][2159-c]
//
// (Read as 2160-byte rows this looked like a "dual-segment" zip — left half of
// a row pair in byte row 2i, right half in 2i+1 — which is the same bytes; see
// inkjoy28_scan_test.go, which pins the two descriptions together.)
const (
	ij28W = 2160
	ij28H = 3060
)

// ij28OverlayScale is the overlay size boost for the 28.5" panel: it cancels the
// engine's ~0.78x diagonal shrink so overlay text renders at ~Samsung's absolute
// size (scale ≈ 1.0) rather than proportionally tiny on this large panel. Tunable.
const ij28OverlayScale = 1.28

// paletteInkJoy28Display is the measured 8-ink appearance of the 28.5" (H:3)
// panel, white-balanced off the panel's own white and with black de-skied (the
// chart was shot under cool overcast light). Order follows the hub's palette-
// index convention — indices 0-5 are the shared 6-ink layout the affinity/DR
// code keys off (0=black,1=white,2=yellow,3=red,4=blue,5=green); 6,7 append the
// two inks the 10" lacks but the 28.5" has (goldenrod=code 0x00, orange=0x05).
// Measured 2026-10-21 from a photographed 8-band calibration chart (IMG_0387).
// The glass reflects the room/sky, which lightens and desaturates whatever it
// lands on, so each band is read from its darkest 10% of 40x8 median tiles —
// the least-glare part — not the band median (2026-10-22 re-read: green was
// pulled toward olive by a reflection across half its band; black kept neutral).
var paletteInkJoy28Display = [8][3]float64{
	{44, 46, 49},    // 0 black
	{211, 211, 211}, // 1 white
	{222, 188, 62},  // 2 yellow
	{138, 2, 6},     // 3 red
	{22, 78, 162},   // 4 blue
	{56, 88, 67},    // 5 green
	{225, 212, 148}, // 6 pale goldenrod
	{164, 68, 42},   // 7 orange
}

// hiBytes8 maps a paletteInkJoy28Display index to the panel ink code (low 3
// bits). Mirrors hiBytes for 0-5, then goldenrod=0x00 and orange=0x05.
var hiBytes8 = [8]byte{0x01, 0x02, 0x03, 0x04, 0x06, 0x07, 0x00, 0x05}

// ServeInkJoy28IJI3 encodes album image id as an IJI3 blob for the 28.5" panel,
// applying the device's optional per-device ink palette override. The panel is
// natively portrait: portrait=false means the frame is hung landscape, so the
// photo is cropped √2:1 and rotated 90° CCW onto the panel (same convention as
// the 10"'s portrait rotate, and as drawWeatherOverlay's portrait flag).
func (s *ImageStore) ServeInkJoy28IJI3(id string, portrait bool, colorOverride []PaletteRGB, transition string) ([]byte, error) {
	return s.serveInkJoy28IJI3(id, portrait, nil, colorOverride, transition)
}

// ServeInkJoy28IJI3Overlay is ServeInkJoy28IJI3 with a server-side overlay
// composited onto the full-res 2160x3060 RGB image before dithering
// (mutate-then-dither, like the Samsung/10" paths). Running the mutate at the
// panel's native resolution lets the overlay engine's diagonal-based scaling
// proportion the text/icons to this panel automatically. The unoverlaid photo
// encode is unaffected and stays separately cached — the bridge keys its play
// cache by overlay token, so the clean render remains under its own filename.
// The mutate receives the panel-native 2160x3060 image; for a landscape frame
// that is the photo rotated CCW, so the mutator must rotate its overlay to match.
func (s *ImageStore) ServeInkJoy28IJI3Overlay(id string, portrait bool, mutate overlayFrameMutator, colorOverride []PaletteRGB, transition string) ([]byte, error) {
	return s.serveInkJoy28IJI3(id, portrait, mutate, colorOverride, transition)
}

// serveInkJoy28IJI3 produces the IJI3 for the photo, optionally with an overlay.
//
// The photo dither (the expensive part) is computed once and cached as an ink-
// index grid. The clean path scans+wraps that grid directly. The overlay path
// does NOT re-dither: the overlay marks are flat, absolute colours (black-
// outlined white text — no translucent box in outline style), so they need no
// error diffusion. We rebuild the dithered photo's display colours, let the
// existing drawer stamp the overlay onto them, then map back to the nearest ink.
// Non-overlay pixels are already exact palette colours, so they map to
// themselves (untouched); only the overlay marks change, landing as crisp flat
// black/white ink. Reusing the cached base dither makes overlay renders fast.
func (s *ImageStore) serveInkJoy28IJI3(id string, portrait bool, mutate overlayFrameMutator, colorOverride []PaletteRGB, transition string) ([]byte, error) {
	render, err := s.inkjoy28Render(id, portrait, colorOverride)
	if err != nil {
		return nil, err
	}
	base, steps := render.idx, s.inkjoy28Steps(render.lum, transition)
	if mutate == nil {
		return scanWrapInkJoy28(base, steps)
	}
	pal := inkjoy28Palette(colorOverride)
	display := inkjoy28IndicesToDisplayRGBA(base, pal)
	// flatRGB=false: the 28.5" path has no flat-calibration-wipe concept.
	overlaid, err := mutate(display, false)
	if err != nil {
		return nil, err
	}
	return scanWrapInkJoy28(inkjoy28StampOverlay(base, overlaid, pal), steps)
}

// inkjoy28BaseIndices returns the dithered 8-ink index grid for the photo, so
// overlay/date re-renders reuse the dither instead of repeating it. Grids are
// cached in memory and on disk (beside the 10"'s .bin cache, so ClearBinCache
// drops them too), keyed by everything the dither reads: the source file, its
// crop/rotation, orientation, the colour override and the full colour
// pipeline — so a settings or photo change can't be served a stale render,
// and a bridge restart or deploy doesn't force a re-dither.
func (s *ImageStore) inkjoy28BaseIndices(id string, portrait bool, colorOverride []PaletteRGB) ([][]byte, error) {
	r, err := s.inkjoy28Render(id, portrait, colorOverride)
	return r.idx, err
}

// ij28Render is a cached 28.5" render: the dithered palette-index grid and the
// photo's luminance transition steps (luminanceSteps28), both panel-native.
type ij28Render struct {
	idx, lum [][]byte
}

func (s *ImageStore) inkjoy28Render(id string, portrait bool, colorOverride []PaletteRGB) (ij28Render, error) {
	meta, err := s.readMeta(id)
	if err != nil {
		return ij28Render{}, err
	}
	pipe := s.colorPipelineForMetaOverride(meta, colorOverride)
	st, err := os.Stat(s.rawPath(id))
	if err != nil {
		return ij28Render{}, fmt.Errorf("raw file missing for %s", id)
	}
	key := inkjoy28RenderKey(id, portrait, colorOverride, pipe, meta, st)
	if v, ok := inkjoy28IdxCache.get(key); ok {
		return v, nil
	}
	diskPath := filepath.Join(s.cacheDir(), id+".ij28-"+key[:16])
	if v, err := readInkJoy28Render(diskPath); err == nil {
		inkjoy28IdxCache.put(key, v)
		return v, nil
	}
	raw, err := os.ReadFile(s.rawPath(id))
	if err != nil {
		return ij28Render{}, fmt.Errorf("raw file missing for %s", id)
	}
	img, err := decodeAnyImage(raw)
	if err != nil {
		return ij28Render{}, err
	}
	img = inkjoy28PanelImage(img, meta, portrait)
	// Dither against the 8-ink 28.5" palette (not pipe.InkJoyDisplay, the 10"'s
	// 6-ink set); goldenrod/orange (6,7) widen the dither gamut.
	indices := StuckiTwoPalette(img, inkjoy28Palette(colorOverride), pipe, false, stuckiOptionsInkJoy(pipe))
	if len(indices) != ij28H || len(indices[0]) != ij28W {
		return ij28Render{}, fmt.Errorf("inkjoy28: dither grid %dx%d, want %dx%d",
			len(indices[0]), len(indices), ij28W, ij28H)
	}
	r := ij28Render{idx: indices, lum: luminanceSteps28(img)}
	inkjoy28IdxCache.put(key, r)
	if err := writeInkJoy28Render(diskPath, r); err != nil {
		log.Printf("inkjoy28: disk cache write %s: %v", diskPath, err)
	}
	return r, nil
}

// inkjoy28RenderKey hashes every input of the 28.5" dither.
func inkjoy28RenderKey(id string, portrait bool, colorOverride []PaletteRGB, pipe ColorPipeline, meta ImageMeta, src os.FileInfo) string {
	cropKey := "1:√2"
	if !portrait {
		cropKey = "√2:1"
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("v2|%s|p=%t|%s|%#v|crop=%#v|rot=%v|src=%d@%d",
		id, portrait, inkjoy28OverrideKey(colorOverride), pipe,
		meta.Crops[cropKey], meta.RotateOverride, src.Size(), src.ModTime().UnixNano())))
	return hex.EncodeToString(sum[:])
}

// writeInkJoy28Render stores a render as an IJI3 container of the unscanned
// plane (LZ4 + CRC), written atomically. Each byte packs like the panel's:
// palette index in the low 3 bits, luminance step in the upper 5.
func writeInkJoy28Render(path string, r ij28Render) error {
	plane := make([]byte, 0, ij28W*ij28H)
	for y, row := range r.idx {
		for x, v := range row {
			plane = append(plane, v&7|r.lum[y][x]<<3)
		}
	}
	b, err := writeInkJoy28IJI3(plane)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readInkJoy28Render(path string) (ij28Render, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ij28Render{}, err
	}
	w, h, _, plane, err := parseIJI3(b)
	if err != nil {
		return ij28Render{}, err
	}
	if w != ij28W || h != ij28H || len(plane) != ij28W*ij28H {
		return ij28Render{}, fmt.Errorf("inkjoy28: cached grid %dx%d", w, h)
	}
	r := ij28Render{idx: make([][]byte, ij28H), lum: make([][]byte, ij28H)}
	for y := range r.idx {
		src := plane[y*ij28W : (y+1)*ij28W]
		r.idx[y], r.lum[y] = make([]byte, ij28W), make([]byte, ij28W)
		for x, v := range src {
			r.idx[y][x], r.lum[y][x] = v&7, v>>3
		}
	}
	return r, nil
}

// inkjoy28PanelImage crops, sizes and orients a decoded photo onto the natively
// portrait 2160x3060 panel. It uses the album crop matching the frame's
// orientation (1:√2 portrait, √2:1 landscape); with none stored it center-crops
// to the panel aspect rather than squashing the whole photo into it. Landscape
// content is rotated 90° CCW onto the panel.
func inkjoy28PanelImage(img image.Image, meta ImageMeta, portrait bool) image.Image {
	img = applyRotateOverride(img, meta.RotateOverride)
	cropKey, w, h := "1:√2", ij28W, ij28H
	if !portrait {
		cropKey, w, h = "√2:1", ij28H, ij28W
	}
	if crop, ok := meta.Crops[cropKey]; ok && crop.W > 0 && crop.H > 0 {
		img = applyCrop(img, crop)
	} else {
		img = centerCropToAspect(img, float64(w)/float64(h))
	}
	img = resizeTo(img, w, h)
	if !portrait {
		img = rotate90CCW(img)
	}
	return img
}

// inkjoy28Palette is the 28.5" dither palette: the device's override when it
// holds all 8 inks, else the measured paletteInkJoy28Display. A 6-ink override
// (saved before overrides followed the panel's ink count) is ignored, as it
// always was here.
func inkjoy28Palette(override []PaletteRGB) [][3]float64 {
	if len(override) == len(paletteInkJoy28Display) {
		return paletteFromRGBs(override)
	}
	return paletteInkJoy28Display[:]
}

// inkjoy28IndicesToDisplayRGBA paints each ink index back to its display colour in pal.
func inkjoy28IndicesToDisplayRGBA(indices [][]byte, pal [][3]float64) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, ij28W, ij28H))
	for y := 0; y < ij28H; y++ {
		row := indices[y]
		for x := 0; x < ij28W; x++ {
			c := pal[row[x]]
			o := out.PixOffset(x, y)
			out.Pix[o] = uint8(c[0])
			out.Pix[o+1] = uint8(c[1])
			out.Pix[o+2] = uint8(c[2])
			out.Pix[o+3] = 255
		}
	}
	return out
}

// inkjoy28StampOverlay lays the overlay's marks onto the dithered photo as
// black/white ink only. Each pixel keeps its photo ink unless the overlaid colour
// is nearer to black or white, so the photo dither is never touched and soft
// text edges / the drop shadow can't snap to a third ink (yellow, orange, ...).
func inkjoy28StampOverlay(base [][]byte, overlaid image.Image, pal [][3]float64) [][]byte {
	const black, white = 0, 1 // palette indices
	rgba := asRGBA(overlaid)
	b := rgba.Bounds()
	indices := make([][]byte, ij28H)
	for y := 0; y < ij28H; y++ {
		indices[y] = make([]byte, ij28W)
		for x := 0; x < ij28W; x++ {
			o := rgba.PixOffset(b.Min.X+x, b.Min.Y+y)
			rgb := [3]float64{float64(rgba.Pix[o]), float64(rgba.Pix[o+1]), float64(rgba.Pix[o+2])}
			photo := base[y][x]
			candidates := [][3]float64{pal[photo], pal[black], pal[white]}
			indices[y][x] = [3]byte{photo, black, white}[nearestColor(rgb, candidates)]
		}
	}
	return indices
}

// scanWrapInkJoy28 applies the panel scan transform to an ink-index grid and
// wraps it as IJI3. (Split from the dither so both the clean and overlay paths
// share the scan without re-dithering.)
func scanWrapInkJoy28(indices, steps [][]byte) ([]byte, error) {
	plane, err := packInkJoy28PlaneSteps(indices, steps)
	if err != nil {
		return nil, err
	}
	return writeInkJoy28IJI3(plane)
}

// packInkJoy28Plane lays an upright ink-index grid out as the panel's raw plane:
// big-endian u16 words, one per vertical pixel pair, row-major, X flipped.
func packInkJoy28Plane(indices [][]byte) ([]byte, error) {
	return packInkJoy28PlaneSteps(indices, nil)
}

// packInkJoy28PlaneSteps is packInkJoy28Plane with each pixel's refresh
// transition step (1..31, see inkjoy28_wipe.go) in the upper 5 bits of its
// byte; steps nil leaves them 0.
func packInkJoy28PlaneSteps(indices, steps [][]byte) ([]byte, error) {
	if len(indices) != ij28H || len(indices[0]) != ij28W {
		return nil, fmt.Errorf("inkjoy28: grid %dx%d, want %dx%d",
			len(indices[0]), len(indices), ij28W, ij28H)
	}
	if steps != nil && (len(steps) != ij28H || len(steps[0]) != ij28W) {
		return nil, fmt.Errorf("inkjoy28: transition grid %dx%d, want %dx%d",
			len(steps[0]), len(steps), ij28W, ij28H)
	}
	step := func(y, x int) byte {
		if steps == nil {
			return 0
		}
		return steps[y][x] << 3
	}
	plane := make([]byte, ij28W*ij28H)
	for i := 0; i < ij28H/2; i++ {
		upper, lower := indices[2*i], indices[2*i+1]
		row := plane[i*ij28W*2:]
		for c := 0; c < ij28W; c++ {
			x := ij28W - 1 - c
			hi := hiBytes8[upper[x]] | step(2*i, x)
			lo := hiBytes8[lower[x]] | step(2*i+1, x)
			binary.BigEndian.PutUint16(row[2*c:], uint16(hi)<<8|uint16(lo))
		}
	}
	return plane, nil
}

// inkjoy28IdxCache memoizes dithered base index grids (see inkjoy28BaseIndices).
var inkjoy28IdxCache = &inkjoy28IndexCache{m: map[string]ij28Render{}, cap: 4}

type inkjoy28IndexCache struct {
	mu    sync.Mutex
	m     map[string]ij28Render
	order []string
	cap   int
}

func (c *inkjoy28IndexCache) get(key string) (ij28Render, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[key]
	return v, ok
}

func (c *inkjoy28IndexCache) put(key string, v ij28Render) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[key]; !ok {
		c.order = append(c.order, key)
	}
	c.m[key] = v
	for len(c.order) > c.cap {
		delete(c.m, c.order[0])
		c.order = c.order[1:]
	}
}

func inkjoy28OverrideKey(o []PaletteRGB) string {
	if o == nil {
		return "nil"
	}
	return fmt.Sprintf("%v", o)
}

// writeInkJoy28IJI3 wraps a raw 2160x3060 8bpp plane in the IJI3 container:
// 16-byte header ("IJI3" + W(BE u16) + H(BE u16) + rawSize(LE u32) + CRC(LE u16)
// + flags(LE u16, 0x0005 = LZ4|8bpp)) followed by an LZ4 block of the raw plane.
// CRC is CRC-16/CCITT-FALSE over the uncompressed pixels (the frame validates it
// after LZ4-decompressing — see FUN_ram_4202718a / FUN_ram_42025cbe).
func writeInkJoy28IJI3(raw []byte) ([]byte, error) {
	crc := crc16CCITT(raw)
	comp := make([]byte, lz4.CompressBlockBound(len(raw)))
	n, err := lz4.CompressBlockHC(raw, comp, lz4.Level9, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("inkjoy28: lz4 compress: %w", err)
	}
	if n == 0 {
		return nil, fmt.Errorf("inkjoy28: lz4 block incompressible (%d bytes)", len(raw))
	}
	comp = comp[:n]
	out := make([]byte, 16+len(comp))
	copy(out[0:4], "IJI3")
	binary.BigEndian.PutUint16(out[4:6], ij28W)
	binary.BigEndian.PutUint16(out[6:8], ij28H)
	binary.LittleEndian.PutUint32(out[8:12], uint32(len(raw)))
	binary.LittleEndian.PutUint16(out[12:14], crc)
	binary.LittleEndian.PutUint16(out[14:16], 0x0005)
	copy(out[16:], comp)
	return out, nil
}

// crc16CCITT is CRC-16/CCITT-FALSE (poly 0x1021, init 0xFFFF, no reflection,
// no xorout; check value 0x29B1). Matches the frame's IJI3 CRC.
func crc16CCITT(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
