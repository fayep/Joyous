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
	"strconv"
	"strings"
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

// calibrationWipeGrid returns the vertical left-to-right sweep for primaries charts.
func calibrationWipeGrid() [][]byte {
	if g := wipeGridByName("wipe_vertical_sweep.png"); g != nil {
		return g
	}
	return MakeVerticalSweepWipe(frameW, frameH)
}

func calibrationBlendWipeGrid() [][]byte {
	if g := wipeGridByName("wipe_blend.png"); g != nil {
		return g
	}
	return calibrationWipeGrid()
}

// calibrationWhiteWipeGrid primes with the spatial blend wipe (no row/column timing ladder).
func calibrationWhiteWipeGrid() [][]byte {
	return calibrationBlendWipeGrid()
}

// calibrationGreenWipeGrid uses the same blend wipe for a uniform green field.
func calibrationGreenWipeGrid() [][]byte {
	return calibrationBlendWipeGrid()
}

// calibrationGreenPetalsWipeGrid uses the petals spatial wipe on a uniform green field.
func calibrationGreenPetalsWipeGrid() [][]byte {
	if g := wipeGridByName("wipe_petals.png"); g != nil {
		return g
	}
	return calibrationWipeGrid()
}

// calibrationGreenUniformWipeGrid holds every pixel at lo=248 (terminal wipe step).
func calibrationGreenUniformWipeGrid() [][]byte {
	return MakeUniformWipe(frameW, frameH, 248)
}

// flatCalibrationWipeGrid picks a fixed wipe for flat calibration PNGs by filename.
func flatCalibrationWipeGrid(name string) [][]byte {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "white") || strings.Contains(lower, "warm") {
		return calibrationWhiteWipeGrid()
	}
	if strings.Contains(lower, "petals") {
		return calibrationGreenPetalsWipeGrid()
	}
	if strings.Contains(lower, "uniform") {
		return calibrationGreenUniformWipeGrid()
	}
	if strings.Contains(lower, "green") {
		return calibrationGreenWipeGrid()
	}
	if isFlatCalibrationName(name) {
		return calibrationWipeGrid()
	}
	return nil
}

func wipeGridByName(name string) [][]byte {
	loadEmbeddedWipes()
	for i, n := range embeddedWipeNames {
		if n == name {
			return embeddedWipes[i]
		}
	}
	return nil
}

const inkJoyGreenHi = 0x07

const (
	WipeUniform248   = "uniform248" // legacy alias for uniform:248
	WipeUniformPrefix = "uniform:"
	WipeRandom       = "random"
	WipeLuminance    = "luminance" // per-image topo lo grid from pre-DR L* (baked at encode)
)

// DefaultInkJoyWipe is the lo-byte pattern for panel refresh (whole-frame block fill).
const DefaultInkJoyWipe = WipeUniform248

// WipeChoice is one selectable InkJoy refresh transition.
type WipeChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// WipeUniformLevel documents one of the 31 consistent lo-byte levels.
type WipeUniformLevel struct {
	ID   string `json:"id"`
	Step int    `json:"step"`
	Lo   int    `json:"lo"`
}

func normalizeInkJoyWipe(selection string) string {
	selection = strings.TrimSpace(selection)
	if selection == "" {
		return WipeUniform248
	}
	if selection == WipeUniform248 {
		return formatUniformWipeID(248)
	}
	if lo, ok := parseUniformWipeLo(selection); ok {
		return formatUniformWipeID(lo)
	}
	return selection
}

func formatUniformWipeID(lo byte) string {
	return fmt.Sprintf("%s%d", WipeUniformPrefix, lo)
}

func parseUniformWipeLo(selection string) (byte, bool) {
	if selection == WipeUniform248 {
		return 248, true
	}
	if !strings.HasPrefix(selection, WipeUniformPrefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(selection, WipeUniformPrefix))
	if err != nil || n < 0 || n > 248 {
		return 0, false
	}
	return normalizeWipeLoByte(byte(n)), true
}

func normalizeWipeLoByte(lo byte) byte {
	if lo >= 248 {
		return 248
	}
	return byte((int(lo) / 8) * 8)
}

func inkJoyWipeUniformLevels() []WipeUniformLevel {
	levels := make([]WipeUniformLevel, wipeStepCount)
	for step := range wipeStepCount {
		lo := wipeStepIndexToByte(step)
		levels[step] = WipeUniformLevel{
			ID:   formatUniformWipeID(lo),
			Step: step,
			Lo:   int(lo),
		}
	}
	return levels
}

func inkJoyWipeChoices() []WipeChoice {
	loadEmbeddedWipes()
	out := []WipeChoice{
		{ID: WipeUniformPrefix + "picker", Label: "Uniform lo (consistent — use slider below)"},
		{ID: WipeLuminance, Label: "Luminance topo (pre-DR)"},
		{ID: WipeRandom, Label: "Random pattern"},
	}
	for _, name := range embeddedWipeNames {
		out = append(out, WipeChoice{ID: name, Label: wipeDisplayName(name)})
	}
	return out
}

func wipeDisplayName(filename string) string {
	base := strings.TrimSuffix(filename, ".png")
	base = strings.TrimPrefix(base, "wipe_")
	return strings.ReplaceAll(base, "_", " ")
}

// isImageDerivedInkJoyWipe reports wipes whose lo grid is baked per image at encode time.
func isImageDerivedInkJoyWipe(selection string) bool {
	return normalizeInkJoyWipe(selection) == WipeLuminance
}

// resolveWipeGrid returns the lo grid for a configured InkJoy refresh transition.
func resolveWipeGrid(selection string) [][]byte {
	selection = normalizeInkJoyWipe(selection)
	if selection == WipeRandom {
		return randomWipeGrid()
	}
	if lo, ok := parseUniformWipeLo(selection); ok {
		return MakeUniformWipe(frameW, frameH, lo)
	}
	if g := wipeGridByName(selection); g != nil {
		return g
	}
	return MakeUniformWipe(frameW, frameH, 248)
}

// resolveWipeGridForEncode returns the lo grid to bake into a converted .bin.
// Luminance wipe uses scene L* from img before DR compression.
func resolveWipeGridForEncode(img image.Image, selection string) [][]byte {
	selection = normalizeInkJoyWipe(selection)
	if selection == WipeLuminance {
		return MakeLuminanceLoWipe(img)
	}
	return resolveWipeGrid(selection)
}

func applyInkJoyWipe(bin []byte, selection string) {
	applyLoToBin(bin, resolveWipeGrid(selection))
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

func validateInkJoyWipe(selection string) error {
	selection = normalizeInkJoyWipe(selection)
	if selection == WipeRandom || selection == WipeLuminance {
		return nil
	}
	if _, ok := parseUniformWipeLo(selection); ok {
		return nil
	}
	if wipeGridByName(selection) != nil {
		return nil
	}
	return fmt.Errorf("unknown inkjoy_wipe %q", selection)
}
