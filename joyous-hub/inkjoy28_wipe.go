package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"image"
	"log"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 28.5" refresh transitions. Each IJI3 pixel byte carries the ink code in its
// low 3 bits and a transition step (1..31) in the upper 5 — the cloud never
// sends step 0. Our renders used to leave them 0 (no transition); the
// InkJoy28Wipe colour setting now picks the steps:
//
//	off         every step 0, as before
//	luminance   per pixel from the photo's own L*: black → 1 … white → 31
//	random      a random captured cloud transition (see saveCapturedWipe)
//	captured:N  the captured transition N (wipes-captured/2160x3060/N.png)
const (
	Wipe28Off            = "off"
	Wipe28Luminance      = "luminance"
	Wipe28Random         = "random"
	Wipe28CapturedPrefix = "captured:"
	wipe28MaxStep        = 31
)

func normalizeInkJoy28Wipe(sel string) string {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return Wipe28Off
	}
	return sel
}

func validateInkJoy28Wipe(sel string) error {
	switch sel = normalizeInkJoy28Wipe(sel); {
	case sel == Wipe28Off, sel == Wipe28Luminance, sel == Wipe28Random:
		return nil
	case strings.HasPrefix(sel, Wipe28CapturedPrefix):
		name := strings.TrimPrefix(sel, Wipe28CapturedPrefix)
		if name != "" && !strings.ContainsAny(name, `/\`) && name != "." && name != ".." {
			return nil
		}
	}
	return fmt.Errorf("unknown inkjoy28_wipe %q", sel)
}

func capturedWipe28Dir(dataDir string) string {
	return filepath.Join(dataDir, "wipes-captured", fmt.Sprintf("%dx%d", ij28W, ij28H))
}

// capturedWipe28Names lists the captured 28.5" transitions, sorted.
func capturedWipe28Names(dataDir string) []string {
	entries, _ := os.ReadDir(capturedWipe28Dir(dataDir))
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".png") {
			names = append(names, strings.TrimSuffix(e.Name(), ".png"))
		}
	}
	sort.Strings(names)
	return names
}

func inkJoy28WipeChoices(dataDir string) []WipeChoice {
	out := []WipeChoice{
		{ID: Wipe28Off, Label: "Off (no transition)"},
		{ID: Wipe28Luminance, Label: "Luminance (black first → white last)"},
		{ID: Wipe28Random, Label: "Random captured transition"},
	}
	for _, n := range capturedWipe28Names(dataDir) {
		out = append(out, WipeChoice{ID: Wipe28CapturedPrefix + n, Label: "Captured " + n})
	}
	return out
}

// luminanceSteps28 maps each pixel's L* to a step: 1 (black) … 31 (white).
func luminanceSteps28(img image.Image) [][]byte {
	b := img.Bounds()
	steps := make([][]byte, b.Dy())
	labRowsParallel(img, func(y int, lab [][3]float64) {
		row := make([]byte, len(lab))
		for x, p := range lab {
			l := math.Max(0, math.Min(100, p[0]))
			row[x] = byte(1 + math.Round(l/100*(wipe28MaxStep-1)))
		}
		steps[y-b.Min.Y] = row
	})
	return steps
}

type capturedWipe28 struct {
	mod   time.Time
	steps [][]byte
}

var capturedWipe28Cache sync.Map // path → capturedWipe28

// loadCapturedWipe28 reads a captured transition (gray = step*8) as steps
// 1..31, cached until the file changes.
func loadCapturedWipe28(path string) ([][]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if v, ok := capturedWipe28Cache.Load(path); ok && v.(capturedWipe28).mod.Equal(st.ModTime()) {
		return v.(capturedWipe28).steps, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	if b.Dx() != ij28W || b.Dy() != ij28H {
		return nil, fmt.Errorf("wipe %s is %dx%d, want %dx%d", path, b.Dx(), b.Dy(), ij28W, ij28H)
	}
	steps := make([][]byte, ij28H)
	for y := range steps {
		steps[y] = make([]byte, ij28W)
		for x := range steps[y] {
			steps[y][x] = byte(max(1, min(wipe28MaxStep, int(grayAt(img, b.Min.X+x, b.Min.Y+y))>>3)))
		}
	}
	capturedWipe28Cache.Store(path, capturedWipe28{st.ModTime(), steps})
	return steps, nil
}

func (s *ImageStore) inkjoy28WipeSelection() string {
	if s.colors != nil {
		return normalizeInkJoy28Wipe(s.colors.Config().InkJoy28Wipe)
	}
	return Wipe28Off
}

// inkjoy28Steps resolves the transition steps for one send; nil means all 0.
// lum is the render's luminance steps (luminanceSteps28).
func (s *ImageStore) inkjoy28Steps(lum [][]byte) [][]byte {
	sel := s.inkjoy28WipeSelection()
	name := ""
	switch {
	case sel == Wipe28Luminance:
		return lum
	case sel == Wipe28Random:
		names := capturedWipe28Names(s.dir)
		if len(names) == 0 {
			return nil
		}
		i, err := rand.Int(rand.Reader, big.NewInt(int64(len(names))))
		if err != nil {
			return nil
		}
		name = names[i.Int64()]
	case strings.HasPrefix(sel, Wipe28CapturedPrefix):
		name = strings.TrimPrefix(sel, Wipe28CapturedPrefix)
	default:
		return nil
	}
	steps, err := loadCapturedWipe28(filepath.Join(capturedWipe28Dir(s.dir), name+".png"))
	if err != nil {
		log.Printf("inkjoy28 wipe %q: %v (no transition)", name, err)
		return nil
	}
	return steps
}
