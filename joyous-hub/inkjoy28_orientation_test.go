package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"
)

// halvesImage is a w×h image whose left half is red and right half is blue.
func halvesImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{255, 0, 0, 255}
			if x >= w/2 {
				c = color.RGBA{0, 0, 255, 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func isRed(c color.Color) bool  { r, _, b, _ := c.RGBA(); return r > b }
func isBlue(c color.Color) bool { r, _, b, _ := c.RGBA(); return b > r }

// A landscape frame (portrait=false) must show a landscape photo with its long
// side horizontal: the photo is rotated CCW onto the native-portrait panel, so
// the photo's left half lands on the panel's bottom and its right half on top.
func TestInkJoy28PanelImageLandscapeRotatesOntoPanel(t *testing.T) {
	out := inkjoy28PanelImage(halvesImage(1414, 1000), ImageMeta{}, false)
	b := out.Bounds()
	if b.Dx() != ij28W || b.Dy() != ij28H {
		t.Fatalf("got %dx%d, want panel %dx%d", b.Dx(), b.Dy(), ij28W, ij28H)
	}
	if top := out.At(ij28W/2, ij28H/4); !isBlue(top) {
		t.Errorf("panel top should be the photo's right half (blue), got %v", top)
	}
	if bottom := out.At(ij28W/2, 3*ij28H/4); !isRed(bottom) {
		t.Errorf("panel bottom should be the photo's left half (red), got %v", bottom)
	}
}

// With no stored crop for the frame's orientation, the photo is center-cropped
// to the panel aspect, not squashed: a wide photo on a portrait frame keeps
// only its middle, so both coloured halves still meet at the panel's center.
func TestInkJoy28PanelImageMissingCropCenterCrops(t *testing.T) {
	// Only a landscape crop is stored; a portrait frame must not use it.
	meta := ImageMeta{Crops: map[string]CropRect{"√2:1": {X: 0, Y: 0, W: 0.1, H: 1}}}
	out := inkjoy28PanelImage(halvesImage(3000, 1000), meta, true)
	b := out.Bounds()
	if b.Dx() != ij28W || b.Dy() != ij28H {
		t.Fatalf("got %dx%d, want panel %dx%d", b.Dx(), b.Dy(), ij28W, ij28H)
	}
	if l := out.At(ij28W/4, ij28H/2); !isRed(l) {
		t.Errorf("left of center should be red, got %v", l)
	}
	if r := out.At(3*ij28W/4, ij28H/2); !isBlue(r) {
		t.Errorf("right of center should be blue, got %v", r)
	}
}

// inkCodeToIndex maps a panel ink code back to its paletteInkJoy28Display index.
func inkCodeToIndex(code byte) int {
	for i, c := range hiBytes8 {
		if c == code {
			return i
		}
	}
	return 0
}

// inkjoy28UnscanIndices inverts scanWrapInkJoy28's panel scan, returning the
// upright 2160x3060 grid of panel ink codes.
func inkjoy28UnscanIndices(plane []byte) [][]byte {
	const half = ij28W / 2
	idx := make([][]byte, ij28H)
	for i := range idx {
		idx[i] = make([]byte, ij28W)
	}
	for i := 0; i < ij28H/2; i++ {
		b0, b1 := (2*i)*ij28W, (2*i+1)*ij28W
		for c := 0; c < half; c++ {
			idx[2*i][ij28W-1-c] = plane[b0+2*c] & 7
			idx[2*i+1][ij28W-1-c] = plane[b0+2*c+1] & 7
			idx[2*i][ij28W-1-half-c] = plane[b1+2*c] & 7
			idx[2*i+1][ij28W-1-half-c] = plane[b1+2*c+1] & 7
		}
	}
	return idx
}

// On a landscape frame the overlay is composited after the dither onto the
// rotated panel image, so drawWeatherOverlay's rotate flag must put it on the
// landscape picture's bottom edge — which, rotated CCW onto the panel, is the
// panel's right half — never along the panel's own (sideways) bottom.
func TestServeInkJoy28IJI3OverlayLandscapeLandsOnLandscapeBottom(t *testing.T) {
	initOverlayFonts()
	if overlayFontErr != nil {
		t.Skip(overlayFontErr)
	}
	store := NewImageStore(t.TempDir())
	id := storeSynthImage(t, store, 1527, 1080)
	cfg := defaultOverlayConfig()
	cfg.WeatherStyle = overlayWeatherStyleOutline
	weather := WeatherSnapshot{
		TempC: 20, Condition: "Clear", City: "Testville",
		DisplayDate: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
		Temperature: OverlayTemperature{Current: 20, Min: 14, Max: 22},
	}
	plain, err := store.ServeInkJoy28IJI3(id, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	over, err := store.ServeInkJoy28IJI3Overlay(id, false, func(img image.Image, flatRGB bool) (image.Image, error) {
		return drawWeatherOverlayScaled(img, cfg, weather, "", true, ij28OverlayScale), nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := inkjoy28UnscanIndices(decodeIJI3Plane(t, plain))
	b := inkjoy28UnscanIndices(decodeIJI3Plane(t, over))
	changed, misplaced := 0, 0
	for y := 0; y < ij28H; y++ {
		for x := 0; x < ij28W; x++ {
			if a[y][x] != b[y][x] {
				changed++
				if x < ij28W/2 {
					misplaced++
				}
			}
		}
	}
	if changed == 0 {
		t.Fatal("overlay changed no pixels")
	}
	// The overlay only ever stamps black or white ink onto the photo. (The plane
	// holds panel ink codes: hiBytes8[0]=black, hiBytes8[1]=white.)
	for y := 0; y < ij28H; y++ {
		for x := 0; x < ij28W; x++ {
			if a[y][x] != b[y][x] && b[y][x] != hiBytes8[0] && b[y][x] != hiBytes8[1] {
				t.Fatalf("overlay wrote ink %d at (%d,%d); only black/white allowed", b[y][x], x, y)
			}
		}
	}
	if misplaced != 0 {
		t.Fatalf("%d of %d overlay pixels in the panel's left half (landscape top), want all on the landscape bottom", misplaced, changed)
	}
	if dir := os.Getenv("IJ28_DUMP"); dir != "" {
		up := image.NewRGBA(image.Rect(0, 0, ij28H/2, ij28W/2))
		for y := 0; y < ij28W/2; y++ {
			for x := 0; x < ij28H/2; x++ {
				// upright landscape (x,y) = panel (y, H-1-x) under the CCW rotate.
				c := paletteInkJoy28Display[inkCodeToIndex(b[ij28H-1-2*x][2*y])]
				up.Set(x, y, color.RGBA{uint8(c[0]), uint8(c[1]), uint8(c[2]), 255})
			}
		}
		f, _ := os.Create(dir + "/ij28_landscape_overlay.png")
		png.Encode(f, up)
		f.Close()
	}
}
