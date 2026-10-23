package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"

	"github.com/pierrec/lz4/v4"
)

// IJI3 is the InkJoy playback container: a 16-byte header ("IJI3" + W(BE u16)
// + H(BE u16) + rawSize(LE u32) + CRC(LE u16) + flags(LE u16)) and the plane,
// LZ4-block compressed when flags has iji3FlagLZ4. The cloud renders the
// 28.5" in it (2160x3060 8bpp, flags 0x0005) — the same format
// writeInkJoy28IJI3 produces — and other panels may get it too, so a relayed
// play is checked by its header, not by one panel's byte count.
const (
	iji3HeaderLen = 16
	iji3FlagLZ4   = 0x0001
	iji3Flag8bpp  = 0x0004
	iji3MaxRaw    = 64 << 20
)

func isIJI3(b []byte) bool { return len(b) >= 4 && string(b[:4]) == "IJI3" }

// parseIJI3 validates an IJI3 container (header, decompression, CRC over the
// uncompressed plane) and returns its geometry, flags and raw plane.
func parseIJI3(b []byte) (w, h int, flags uint16, raw []byte, err error) {
	if !isIJI3(b) || len(b) < iji3HeaderLen {
		return 0, 0, 0, nil, fmt.Errorf("iji3: no IJI3 header")
	}
	w = int(binary.BigEndian.Uint16(b[4:6]))
	h = int(binary.BigEndian.Uint16(b[6:8]))
	rawSize := int(binary.LittleEndian.Uint32(b[8:12]))
	crc := binary.LittleEndian.Uint16(b[12:14])
	flags = binary.LittleEndian.Uint16(b[14:16])
	if w == 0 || h == 0 || rawSize == 0 || rawSize > iji3MaxRaw {
		return 0, 0, 0, nil, fmt.Errorf("iji3: bad header %dx%d raw %d", w, h, rawSize)
	}
	body := b[iji3HeaderLen:]
	if flags&iji3FlagLZ4 != 0 {
		raw = make([]byte, rawSize)
		n, err := lz4.UncompressBlock(body, raw)
		if err != nil {
			return 0, 0, 0, nil, fmt.Errorf("iji3: lz4: %w", err)
		}
		if n != rawSize {
			return 0, 0, 0, nil, fmt.Errorf("iji3: lz4 gave %d bytes, header says %d", n, rawSize)
		}
	} else {
		if len(body) != rawSize {
			return 0, 0, 0, nil, fmt.Errorf("iji3: body %d bytes, header says %d", len(body), rawSize)
		}
		raw = body
	}
	if got := crc16CCITT(raw); got != crc {
		return 0, 0, 0, nil, fmt.Errorf("iji3: crc %04x, header says %04x", got, crc)
	}
	return w, h, flags, raw, nil
}

// iji3PreviewJPEG renders an IJI3 play as a display preview. Only the 28.5"
// plane layout is known (see packInkJoy28Plane); other geometries error.
// portrait=false means the frame hangs landscape, so the panel-native image
// is rotated back upright.
func iji3PreviewJPEG(b []byte, portrait bool) ([]byte, error) {
	w, h, flags, raw, err := parseIJI3(b)
	if err != nil {
		return nil, err
	}
	if w != ij28W || h != ij28H || flags&iji3Flag8bpp == 0 || len(raw) != ij28W*ij28H {
		return nil, fmt.Errorf("iji3: no preview decoder for %dx%d flags %#04x", w, h, flags)
	}
	var codeToIndex [8]int
	for i := range codeToIndex {
		codeToIndex[i] = 1 // unknown code → white
	}
	for i, c := range hiBytes8 {
		codeToIndex[c&7] = i
	}
	codes := unpackInkJoy28Plane(raw)
	img := image.NewRGBA(image.Rect(0, 0, ij28W, ij28H))
	for y, row := range codes {
		for x, c := range row {
			p := paletteInkJoy28Display[codeToIndex[c&7]]
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = uint8(p[0]), uint8(p[1]), uint8(p[2]), 255
		}
	}
	var out image.Image = img
	if !portrait {
		out = rotate90(img) // undo inkjoy28PanelImage's CCW rotate
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaleInkJoyDisplayPreview(out), &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// unpackInkJoy28Plane inverts packInkJoy28Plane, returning the upright
// 2160x3060 grid of panel ink codes.
func unpackInkJoy28Plane(plane []byte) [][]byte {
	idx := make([][]byte, ij28H)
	for i := range idx {
		idx[i] = make([]byte, ij28W)
	}
	for i := 0; i < ij28H/2; i++ {
		for c := 0; c < ij28W; c++ {
			w := binary.BigEndian.Uint16(plane[(i*ij28W+c)*2:])
			idx[2*i][ij28W-1-c] = byte(w>>8) & 7
			idx[2*i+1][ij28W-1-c] = byte(w) & 7
		}
	}
	return idx
}
