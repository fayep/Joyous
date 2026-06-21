package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// markerImage is 2×2 with distinct corner colours for orientation tests.
func markerImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})   // top-left red
	img.Set(1, 0, color.RGBA{0, 255, 0, 255})   // top-right green
	img.Set(0, 1, color.RGBA{0, 0, 255, 255})   // bottom-left blue
	img.Set(1, 1, color.RGBA{255, 255, 0, 255}) // bottom-right yellow
	return img
}

func TestApplyExifOrientationAll(t *testing.T) {
	type corner struct {
		x, y int
		c    color.RGBA
	}
	tests := []struct {
		name    string
		orient  int
		corners []corner
		width   int
		height  int
	}{
		{
			name:   "normal",
			orient: 1,
			corners: []corner{
				{0, 0, color.RGBA{255, 0, 0, 255}},
				{1, 0, color.RGBA{0, 255, 0, 255}},
				{0, 1, color.RGBA{0, 0, 255, 255}},
				{1, 1, color.RGBA{255, 255, 0, 255}},
			},
			width: 2, height: 2,
		},
		{
			name:   "flip horizontal",
			orient: 2,
			corners: []corner{
				{0, 0, color.RGBA{0, 255, 0, 255}},
				{1, 0, color.RGBA{255, 0, 0, 255}},
				{0, 1, color.RGBA{255, 255, 0, 255}},
				{1, 1, color.RGBA{0, 0, 255, 255}},
			},
			width: 2, height: 2,
		},
		{
			name:   "rotate 180",
			orient: 3,
			corners: []corner{
				{0, 0, color.RGBA{255, 255, 0, 255}},
				{1, 0, color.RGBA{0, 0, 255, 255}},
				{0, 1, color.RGBA{0, 255, 0, 255}},
				{1, 1, color.RGBA{255, 0, 0, 255}},
			},
			width: 2, height: 2,
		},
		{
			name:   "flip vertical",
			orient: 4,
			corners: []corner{
				{0, 0, color.RGBA{0, 0, 255, 255}},
				{1, 0, color.RGBA{255, 255, 0, 255}},
				{0, 1, color.RGBA{255, 0, 0, 255}},
				{1, 1, color.RGBA{0, 255, 0, 255}},
			},
			width: 2, height: 2,
		},
		{
			name:   "transpose",
			orient: 5,
			corners: []corner{
				{0, 0, color.RGBA{255, 255, 0, 255}},
				{0, 1, color.RGBA{0, 0, 255, 255}},
				{1, 0, color.RGBA{0, 255, 0, 255}},
				{1, 1, color.RGBA{255, 0, 0, 255}},
			},
			width: 2, height: 2,
		},
		{
			name:   "rotate 90 cw",
			orient: 6,
			corners: []corner{
				{0, 0, color.RGBA{0, 0, 255, 255}},
				{0, 1, color.RGBA{255, 255, 0, 255}},
				{1, 0, color.RGBA{255, 0, 0, 255}},
				{1, 1, color.RGBA{0, 255, 0, 255}},
			},
			width: 2, height: 2,
		},
		{
			name:   "transverse",
			orient: 7,
			corners: []corner{
				{0, 0, color.RGBA{255, 0, 0, 255}},
				{0, 1, color.RGBA{0, 255, 0, 255}},
				{1, 0, color.RGBA{0, 0, 255, 255}},
				{1, 1, color.RGBA{255, 255, 0, 255}},
			},
			width: 2, height: 2,
		},
		{
			name:   "rotate 90 ccw",
			orient: 8,
			corners: []corner{
				{0, 0, color.RGBA{0, 255, 0, 255}},
				{1, 0, color.RGBA{255, 255, 0, 255}},
				{0, 1, color.RGBA{255, 0, 0, 255}},
				{1, 1, color.RGBA{0, 0, 255, 255}},
			},
			width: 2, height: 2,
		},
	}

	src := markerImage()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := applyExifOrientation(src, tt.orient)
			b := out.Bounds()
			if b.Dx() != tt.width || b.Dy() != tt.height {
				t.Fatalf("size: got %dx%d want %dx%d", b.Dx(), b.Dy(), tt.width, tt.height)
			}
			for _, c := range tt.corners {
				if got := pixelAt(out, c.x, c.y); got != c.c {
					t.Fatalf("(%d,%d): got %+v want %+v", c.x, c.y, got, c.c)
				}
			}
		})
	}
}

func TestApplyExifOrientationInvalid(t *testing.T) {
	src := markerImage()
	for _, orient := range []int{0, 9, -1, 100} {
		out := applyExifOrientation(src, orient)
		if out != src {
			t.Fatalf("orientation %d should be unchanged", orient)
		}
	}
}

func TestReadExifOrientationDefaults(t *testing.T) {
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, markerImage()); err != nil {
		t.Fatal(err)
	}
	if got := readExifOrientation(pngBuf.Bytes()); got != 1 {
		t.Fatalf("PNG: got %d want 1", got)
	}
	if got := readExifOrientation(nil); got != 1 {
		t.Fatalf("nil: got %d want 1", got)
	}
	if got := readExifOrientation([]byte{0x00}); got != 1 {
		t.Fatalf("garbage: got %d want 1", got)
	}
}

func TestReadExifOrientationFromFixtures(t *testing.T) {
	tests := []struct {
		file string
		want int
	}{
		{"orient1.jpg", 1},
		{"orient3.jpg", 3},
		{"orient6.jpg", 6},
		{"orient8.jpg", 8},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "exif", tt.file))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if got := readExifOrientation(data); got != tt.want {
				t.Fatalf("readExifOrientation: got %d want %d", got, tt.want)
			}
			if got := parseExifOrientation(data); got != tt.want {
				t.Fatalf("parseExifOrientation: got %d want %d", got, tt.want)
			}
		})
	}
}

func TestDecodeAnyImageAppliesExifFromFixtures(t *testing.T) {
	// orient3.jpg stores pixels in 180° layout; decodeAnyImage should upright them.
	data, err := os.ReadFile(filepath.Join("testdata", "exif", "orient3.jpg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	img, err := decodeAnyImage(data)
	if err != nil {
		t.Fatalf("decodeAnyImage: %v", err)
	}
	if !colorsNear(pixelAt(img, 0, 0), color.RGBA{255, 255, 0, 255}) {
		t.Fatalf("top-left after correction: got %+v want yellow", pixelAt(img, 0, 0))
	}
	if !colorsNear(pixelAt(img, 63, 63), color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("bottom-right after correction: got %+v want red", pixelAt(img, 63, 63))
	}
}

func TestDecodeAnyImageLeavesOrient1Unchanged(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "exif", "orient1.jpg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	img, err := decodeAnyImage(data)
	if err != nil {
		t.Fatalf("decodeAnyImage: %v", err)
	}
	if !colorsNear(pixelAt(img, 0, 0), color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("top-left: got %+v want red", pixelAt(img, 0, 0))
	}
	if !colorsNear(pixelAt(img, 63, 63), color.RGBA{255, 255, 0, 255}) {
		t.Fatalf("bottom-right: got %+v want yellow", pixelAt(img, 63, 63))
	}
}

func TestServeThumbAppliesExifOrientation(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)

	data, err := os.ReadFile(filepath.Join("testdata", "exif", "orient3.jpg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	id, err := store.Store(bytes.NewReader(data), "phone.jpg")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	thumb, err := store.ServeThumb(id)
	if err != nil {
		t.Fatalf("ServeThumb: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("decode thumb: %v", err)
	}
	b := img.Bounds()
	if !colorsNear(pixelAt(img, 0, 0), color.RGBA{255, 255, 0, 255}) {
		t.Fatalf("thumb top-left: got %+v want yellow", pixelAt(img, 0, 0))
	}
	// Square source fits inside thumbW×thumbH preserving aspect ratio.
	if b.Dx() > thumbW || b.Dy() > thumbH {
		t.Fatalf("thumb exceeds bounds: %dx%d", b.Dx(), b.Dy())
	}
}

// TestReadExifOrientationFromJPEGWithExiftool exercises live EXIF writing when exiftool is present.
func TestReadExifOrientationFromJPEGWithExiftool(t *testing.T) {
	exiftool, err := exec.LookPath("exiftool")
	if err != nil {
		t.Skip("exiftool not found")
	}

	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "src.jpg")
	data, err := os.ReadFile(filepath.Join("testdata", "exif", "marker.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(exiftool, "-Orientation=3", "-n", "-overwrite_original", srcPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("exiftool: %v: %s", err, out)
	}

	updated, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := readExifOrientation(updated); got != 3 {
		t.Fatalf("readExifOrientation: got %d want 3", got)
	}
}

func colorsNear(a, b color.RGBA) bool {
	const tol = 8
	return absInt(int(a.R)-int(b.R)) <= tol &&
		absInt(int(a.G)-int(b.G)) <= tol &&
		absInt(int(a.B)-int(b.B)) <= tol
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
