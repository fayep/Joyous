package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
)

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

func writePNGChunk(buf *bytes.Buffer, typ string, data []byte) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	buf.Write(lenBuf[:])
	typAndData := make([]byte, 0, len(typ)+len(data))
	typAndData = append(typAndData, typ...)
	typAndData = append(typAndData, data...)
	buf.Write(typAndData)
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc32.ChecksumIEEE(typAndData))
	buf.Write(crcBuf[:])
}

// EncodeIndexedPNG4 encodes a raw InkJoy .bin (see ToBin/FromBin) as a
// 4-bit indexed PNG matching the firmware's ContentCache contract
// (Firmware/include/panel.h's renderPngBytes, Firmware/src/content_cache.cpp):
// palette index 0-5 = black/white/yellow/red/blue/green, same order as
// hiBytes. The .bin's lo (wipe animation order) byte has no equivalent
// here -- PNG plays are drawn directly by the firmware, no wipe, so it's
// dropped entirely. palette is written into PLTE for PC-side viewers only
// (the firmware decodes with lodepng's color_convert=0 and never reads it) --
// see devicePaletteRGB for the real per-device P1/InkJoyDisplay values.
//
// Hand-rolled rather than Go's encoding/png because that package always
// emits 8-bit-per-pixel palette images regardless of how few colors are in
// the palette (image/png/writer.go's cbP8 path) -- there's no way to make
// it produce the 4-bit-per-pixel output the firmware's lodepng decode
// requires (state.info_png.color.bitdepth == 4).
func EncodeIndexedPNG4(bin []byte, w, h int, palette [6][3]byte) ([]byte, error) {
	if len(bin) != w*h*2 {
		return nil, fmt.Errorf("bin size %d != %d", len(bin), w*h*2)
	}
	hi, _ := FromBin(bin, w, h) // top-to-bottom; lo dropped, see comment above

	rowBytes := (w*4 + 7) / 8
	raw := make([]byte, 0, h*(1+rowBytes))
	for y := 0; y < h; y++ {
		raw = append(raw, 0) // filter type 0 (None)
		row := make([]byte, rowBytes)
		for x := 0; x < w; x++ {
			idx, ok := hiByteToIdx[hi[y][x]]
			if !ok {
				idx = 1 // white -- matches firmware's paletteIndexToColor default
			}
			b := byte(idx & 0x0F)
			if x%2 == 0 {
				row[x/2] = b << 4
			} else {
				row[x/2] |= b
			}
		}
		raw = append(raw, row...)
	}

	var idatBuf bytes.Buffer
	zw, err := zlib.NewWriterLevel(&idatBuf, zlib.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.Write(pngSignature)

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(h))
	ihdr[8] = 4  // bit depth
	ihdr[9] = 3  // color type: palette
	ihdr[10] = 0 // compression method
	ihdr[11] = 0 // filter method
	ihdr[12] = 0 // interlace method
	writePNGChunk(&out, "IHDR", ihdr)

	plte := make([]byte, 0, len(palette)*3)
	for _, c := range palette {
		plte = append(plte, c[0], c[1], c[2])
	}
	writePNGChunk(&out, "PLTE", plte)
	writePNGChunk(&out, "IDAT", idatBuf.Bytes())
	writePNGChunk(&out, "IEND", nil)

	return out.Bytes(), nil
}

// paletteRGBBytesFromFloat converts a ColorPipeline.InkJoyDisplay-style
// palette ([6][3]float64) to clamped byte RGB for a PNG PLTE chunk.
func paletteRGBBytesFromFloat(p [6][3]float64) [6][3]byte {
	var out [6][3]byte
	for i, c := range p {
		for j := 0; j < 3; j++ {
			v := c[j]
			if v < 0 {
				v = 0
			}
			if v > 255 {
				v = 255
			}
			out[i][j] = byte(v + 0.5)
		}
	}
	return out
}

// devicePaletteRGB resolves the real per-device dither-target palette (P1,
// i.e. ColorPipeline.InkJoyDisplay with colorOverride applied) for use as a
// PNG's PLTE chunk -- see EncodeIndexedPNG4's comment on why this is
// cosmetic-only from the firmware's perspective.
func (s *ImageStore) devicePaletteRGB(id string, colorOverride []PaletteRGB) [6][3]byte {
	meta, err := s.readMeta(id)
	if err != nil {
		return paletteRGBBytesFromFloat(s.colorPipeline().InkJoyDisplay)
	}
	pipe := s.colorPipelineForMetaOverride(meta, colorOverride)
	return paletteRGBBytesFromFloat(pipe.InkJoyDisplay)
}

// ContentHash returns the SD-cache content-addressed key for bin (see
// Firmware's content_cache.h) -- a short hex digest, not the full sha256.
// 8 hex chars (4 bytes), not more: this must fit a FAT 8.3 short filename's
// 8-character base-name limit, since the SD card's FatFs build has no
// long-filename support -- a longer name here doesn't cleanly fail, it
// silently breaks 8.3 short-name handling on the device (confirmed the
// hard way, 2026-08-13: a play using a 16-char hash hung on-device with no
// further ack after the initial accept). Deliberately chosen this short
// even beyond the 8.3 limit itself: at 4 bytes, the cache's own file-count
// ceiling (kCacheMaxFiles = 200/orientation, content_cache.cpp) is well
// under the point where hash collisions become a real concern.
func ContentHash(bin []byte) string {
	sum := sha256.Sum256(bin)
	return hex.EncodeToString(sum[:4])
}
