package main

import (
	"bytes"
	"crypto/rand"
	"embed"
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"math/big"
	"sort"
	"sync"
)

//go:embed wipes/*.png
var wipePNGs embed.FS

var (
	embeddedWipes     [][][]byte
	embeddedWipeNames []string
	loadWipesOnce     sync.Once
	loadWipesErr      error
)

func loadEmbeddedWipes() {
	loadWipesOnce.Do(func() {
		entries, err := wipePNGs.ReadDir("wipes")
		if err != nil {
			loadWipesErr = err
			return
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			names = append(names, e.Name())
		}
		sort.Strings(names)

		for _, name := range names {
			data, err := wipePNGs.ReadFile("wipes/" + name)
			if err != nil {
				loadWipesErr = fmt.Errorf("read wipe %s: %w", name, err)
				return
			}
			lo, err := wipeFromPNG(data)
			if err != nil {
				loadWipesErr = fmt.Errorf("decode wipe %s: %w", name, err)
				return
			}
			embeddedWipes = append(embeddedWipes, lo)
			embeddedWipeNames = append(embeddedWipeNames, name)
		}
	})
}

// calibrationWipeGrid returns the square box wipe (wipe_box.png) for calibration charts.
func calibrationWipeGrid() [][]byte {
	return wipeGridByName("wipe_box.png")
}

func wipeGridByName(name string) [][]byte {
	loadEmbeddedWipes()
	for i, n := range embeddedWipeNames {
		if n == name {
			return embeddedWipes[i]
		}
	}
	if len(embeddedWipes) > 0 {
		return embeddedWipes[0]
	}
	return MakeClockWipe(frameW, frameH)
}

// randomWipeGrid returns one of the bundled wipe patterns.
// Falls back to MakeClockWipe if embed loading failed.
func randomWipeGrid() [][]byte {
	loadEmbeddedWipes()
	if len(embeddedWipes) == 0 {
		return MakeClockWipe(frameW, frameH)
	}
	n := big.NewInt(int64(len(embeddedWipes)))
	idx, err := rand.Int(rand.Reader, n)
	if err != nil {
		return embeddedWipes[0]
	}
	return embeddedWipes[idx.Int64()]
}

func wipeFromPNG(data []byte) ([][]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	if b.Dx() != frameW || b.Dy() != frameH {
		return nil, fmt.Errorf("wipe PNG size %dx%d, want %dx%d", b.Dx(), b.Dy(), frameW, frameH)
	}
	h, w := b.Dy(), b.Dx()
	lo := make([][]byte, h)
	for y := range h {
		lo[y] = make([]byte, w)
		for x := range w {
			lo[y][x] = grayAt(img, b.Min.X+x, b.Min.Y+y)
		}
	}
	return lo, nil
}

func grayAt(img image.Image, x, y int) byte {
	switch c := img.At(x, y).(type) {
	case color.Gray:
		return c.Y
	case color.Gray16:
		return byte(c.Y >> 8)
	default:
		r, _, _, _ := img.At(x, y).RGBA()
		return byte(r >> 8)
	}
}

// applyLoToBin overwrites lo bytes in a raw .bin (bottom-to-top row order).
func applyLoToBin(bin []byte, lo [][]byte) {
	h := len(lo)
	if h == 0 {
		return
	}
	w := len(lo[0])
	i := 0
	for row := h - 1; row >= 0; row-- {
		for x := range w {
			bin[i+1] = lo[row][x]
			i += 2
		}
	}
}

func applyRandomWipe(bin []byte) {
	applyLoToBin(bin, randomWipeGrid())
}
