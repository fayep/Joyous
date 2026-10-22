package main

import (
	"encoding/binary"
	"fmt"
	"image"
	"os"

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
// The panel scan is a tiled, dual-segment portrait layout, solved empirically
// (see memory project-inkjoy-playlist-json). To render a clean, full-res image
// the buffer must be authored as: flip the source in X (panel pixel origin is
// top-right), then for each source row-pair (r0=2i, r1=2i+1) emit two buffer
// rows — the left half (cols 0..1079) column-zipped into buffer row 2i and the
// right half (cols 1080..2159) column-zipped into buffer row 2i+1:
//
//	buf[2i][2c]   = src[r0][c]        buf[2i][2c+1]   = src[r1][c]
//	buf[2i+1][2c] = src[r0][1080+c]   buf[2i+1][2c+1] = src[r1][1080+c]
const (
	ij28W = 2160
	ij28H = 3060
)

// paletteInkJoy28Display is the measured 8-ink appearance of the 28.5" (H:3)
// panel, white-balanced off the panel's own white and with black de-skied (the
// chart was shot under cool overcast light). Order follows the hub's palette-
// index convention — indices 0-5 are the shared 6-ink layout the affinity/DR
// code keys off (0=black,1=white,2=yellow,3=red,4=blue,5=green); 6,7 append the
// two inks the 10" lacks but the 28.5" has (goldenrod=code 0x00, orange=0x05).
// Measured 2026-10-21 from a photographed 8-band calibration chart.
var paletteInkJoy28Display = [8][3]float64{
	{47, 49, 52},    // 0 black
	{211, 211, 211}, // 1 white
	{222, 188, 62},  // 2 yellow
	{138, 2, 6},     // 3 red
	{22, 78, 162},   // 4 blue
	{86, 92, 68},    // 5 green
	{225, 212, 148}, // 6 pale goldenrod
	{164, 68, 42},   // 7 orange
}

// hiBytes8 maps a paletteInkJoy28Display index to the panel ink code (low 3
// bits). Mirrors hiBytes for 0-5, then goldenrod=0x00 and orange=0x05.
var hiBytes8 = [8]byte{0x01, 0x02, 0x03, 0x04, 0x06, 0x07, 0x00, 0x05}

// ServeInkJoy28IJI3 encodes album image id as an IJI3 blob for the 28.5" panel,
// applying the device's optional per-device ink palette override.
func (s *ImageStore) ServeInkJoy28IJI3(id string, colorOverride *[6]PaletteRGB) ([]byte, error) {
	return s.serveInkJoy28IJI3(id, nil, colorOverride)
}

// ServeInkJoy28IJI3Overlay is ServeInkJoy28IJI3 with a server-side overlay
// composited onto the full-res 2160x3060 RGB image before dithering
// (mutate-then-dither, like the Samsung/10" paths). Running the mutate at the
// panel's native resolution lets the overlay engine's diagonal-based scaling
// proportion the text/icons to this panel automatically. The unoverlaid photo
// encode is unaffected and stays separately cached — the bridge keys its play
// cache by overlay token, so the clean render remains under its own filename.
func (s *ImageStore) ServeInkJoy28IJI3Overlay(id string, mutate overlayFrameMutator, colorOverride *[6]PaletteRGB) ([]byte, error) {
	return s.serveInkJoy28IJI3(id, mutate, colorOverride)
}

// serveInkJoy28IJI3 is the shared decode→crop→resize→(overlay)→dither→IJI3 path.
// mutate is nil for the plain photo path; when set it runs on the upright
// 2160x3060 RGB image just before the dither.
func (s *ImageStore) serveInkJoy28IJI3(id string, mutate overlayFrameMutator, colorOverride *[6]PaletteRGB) ([]byte, error) {
	meta, err := s.readMeta(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(s.rawPath(id))
	if err != nil {
		return nil, fmt.Errorf("raw file missing for %s", id)
	}
	img, err := decodeAnyImage(raw)
	if err != nil {
		return nil, err
	}
	img = applyRotateOverride(img, meta.RotateOverride)
	// The 28.5" panel is natively portrait and its aspect is 1:√2, so use the
	// matching album crop; fall back to a straight resize if none is stored.
	if crop, ok := meta.Crops["1:√2"]; ok && crop.W > 0 && crop.H > 0 {
		img = applyCrop(img, crop)
	}
	img = resizeHQ(img, ij28W, ij28H)
	if mutate != nil {
		// flatRGB=false: the 28.5" photo path has no flat-calibration-wipe
		// concept (that's a 10" hi/lo detail), so the overlay always composites.
		img, err = mutate(img, false)
		if err != nil {
			return nil, err
		}
	}
	return encodeInkJoy28IJI3FromRGBA(img, s.colorPipelineForMetaOverride(meta, colorOverride))
}

// encodeInkJoy28IJI3FromRGBA runs the shared color pipeline, applies the panel
// scan transform, and wraps the result as IJI3.
func encodeInkJoy28IJI3FromRGBA(img image.Image, pipe ColorPipeline) ([]byte, error) {
	// Dither against the measured 8-ink 28.5" palette (not pipe.InkJoyDisplay,
	// which is the 10"'s 6-ink set). StuckiTwoPalette returns indices into this
	// palette; the LAB dynamic-range fit uses its white (index 1, a real ~L*85
	// grey) as the ceiling, and goldenrod/orange (6,7) widen the dither gamut.
	indices := StuckiTwoPalette(img, paletteInkJoy28Display[:], pipe, false, stuckiOptionsInkJoy(pipe))
	if len(indices) != ij28H || len(indices[0]) != ij28W {
		return nil, fmt.Errorf("inkjoy28: dither grid %dx%d, want %dx%d",
			len(indices[0]), len(indices), ij28W, ij28H)
	}
	const half = ij28W / 2 // 1080
	buf := make([]byte, ij28W*ij28H)
	// pix reads index at (row, col) with X flipped (panel origin top-right).
	pix := func(r, c int) byte { return hiBytes8[indices[r][ij28W-1-c]] }
	for i := 0; i < ij28H/2; i++ {
		r0, r1 := 2*i, 2*i+1
		b0, b1 := (2*i)*ij28W, (2*i+1)*ij28W
		for c := 0; c < half; c++ {
			buf[b0+2*c] = pix(r0, c)
			buf[b0+2*c+1] = pix(r1, c)
			buf[b1+2*c] = pix(r0, half+c)
			buf[b1+2*c+1] = pix(r1, half+c)
		}
	}
	return writeInkJoy28IJI3(buf)
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
