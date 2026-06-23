package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const frameW, frameH = 1600, 1200
const defaultCacheMax = 500 << 20 // 500 MB

// CropRect is a normalized (0–1) rectangle within the source image.
type CropRect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// ImageMeta holds persisted metadata for a stored image.
type ImageMeta struct {
	ID      string              `json:"id"`
	Name    string              `json:"name"`
	Size    int64               `json:"size"`
	Width   int                  `json:"width,omitempty"`
	Height  int                  `json:"height,omitempty"`
	Crops   map[string]CropRect `json:"crops,omitempty"`
	FlatRGB bool                `json:"flat_rgb,omitempty"` // calibration PNG: per-pixel snap, no Stucki/LAB
}

// ImageStore manages raw image storage and a bounded converted-bin cache.
type ImageStore struct {
	dir      string
	CacheMax int64 // max total bytes in cache dir; exported so tests can override
}

// NewImageStore creates an ImageStore rooted at dir.
func NewImageStore(dir string) *ImageStore {
	return &ImageStore{dir: dir, CacheMax: defaultCacheMax}
}

func (s *ImageStore) rawDir() string   { return filepath.Join(s.dir, "images") }
func (s *ImageStore) cacheDir() string { return filepath.Join(s.dir, "cache") }
func (s *ImageStore) thumbDir() string { return filepath.Join(s.dir, "thumbs") }

// rawPath is the stored file for id (no extension — format derived from Name in meta).
func (s *ImageStore) rawPath(id string) string   { return filepath.Join(s.rawDir(), id) }
func (s *ImageStore) metaPath(id string) string  { return filepath.Join(s.rawDir(), id+".json") }
func (s *ImageStore) cachePath(id string) string { return filepath.Join(s.cacheDir(), id+".bin") }
func (s *ImageStore) thumbPath(id string) string { return filepath.Join(s.thumbDir(), id+".jpg") }

func (s *ImageStore) previewPath(id string) string {
	return filepath.Join(s.thumbDir(), id+"_preview.jpg")
}

// Store saves r as-is under a new ID and returns that ID.
func (s *ImageStore) Store(r io.Reader, name string) (string, error) {
	if err := os.MkdirAll(s.rawDir(), 0755); err != nil {
		return "", err
	}
	id := newID()
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(s.rawPath(id), data, 0644); err != nil {
		return "", err
	}
	meta := ImageMeta{ID: id, Name: name, Size: int64(len(data)), FlatRGB: isFlatCalibrationName(name)}
	if w, h, err := imageDisplaySize(data, name); err == nil {
		meta.Width, meta.Height = w, h
	}
	b, _ := json.Marshal(meta)
	os.WriteFile(s.metaPath(id), b, 0644)
	return id, nil
}

// ServeBin returns the .bin bytes for id, converting from the stored raw file if
// the cache is cold, then writing the result to cache and evicting if over CacheMax.
func (s *ImageStore) ServeBin(id string) ([]byte, error) {
	return s.ServeBinOrientation(id, false)
}

func (s *ImageStore) ServeBinOrientation(id string, portrait bool) ([]byte, error) {
	cacheFile := s.cachePath(id)
	if portrait {
		cacheFile = s.cachePath(id) + ".portrait"
	}
	// Cache hit.
	if bin, err := os.ReadFile(cacheFile); err == nil {
		s.applyRandomWipeIfConverted(id, bin)
		return bin, nil
	}

	// Cache miss — convert.
	bin, err := s.convertToBinOrientation(id, portrait)
	if err != nil {
		return nil, err
	}

	// Write to cache and enforce size limit.
	os.MkdirAll(s.cacheDir(), 0755)
	os.WriteFile(cacheFile, bin, 0644)
	s.evictCache()

	s.applyRandomWipeIfConverted(id, bin)
	return bin, nil
}

func (s *ImageStore) applyRandomWipeIfConverted(id string, bin []byte) {
	meta, err := s.readMeta(id)
	if err != nil || isStoredBin(meta.Name) {
		return
	}
	applyRandomWipe(bin)
}

func isStoredBin(name string) bool {
	return strings.ToLower(filepath.Ext(name)) == ".bin"
}

// ServeBinHTTP writes the .bin bytes as an HTTP response.
func (s *ImageStore) ServeBinHTTP(w http.ResponseWriter, r *http.Request, id string) {
	s.ServeBinOrientationHTTP(w, r, id, false)
}

// ServeBinOrientationHTTP writes landscape or portrait-rotated .bin as an HTTP response.
func (s *ImageStore) ServeBinOrientationHTTP(w http.ResponseWriter, r *http.Request, id string, portrait bool) {
	bin, err := s.ServeBinOrientation(id, portrait)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(bin)))
	w.Write(bin)
}

// ListImages returns metadata for all stored images.
func (s *ImageStore) ListImages() ([]ImageMeta, error) {
	entries, err := os.ReadDir(s.rawDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []ImageMeta
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.rawDir(), e.Name()))
		if err != nil {
			continue
		}
		var m ImageMeta
		if json.Unmarshal(data, &m) == nil {
			s.fillDimensions(&m)
			out = append(out, m)
		}
	}
	return out, nil
}

// SetCrop stores a crop rect for the given aspect ratio key (e.g. "4:3") and
// invalidates the thumbnail so it regenerates with the new crop applied.
func (s *ImageStore) SetCrop(id, format string, rect CropRect) error {
	metaData, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return fmt.Errorf("image %s not found", id)
	}
	var meta ImageMeta
	json.Unmarshal(metaData, &meta)
	if meta.Crops == nil {
		meta.Crops = make(map[string]CropRect)
	}
	meta.Crops[format] = rect
	b, _ := json.Marshal(meta)
	if err := os.WriteFile(s.metaPath(id), b, 0644); err != nil {
		return err
	}
	// Invalidate thumbnail so next ServeThumb regenerates with the new crop.
	os.Remove(s.thumbPath(id))
	return nil
}

// DeleteCrop removes one saved crop by format key and invalidates the thumbnail.
func (s *ImageStore) DeleteCrop(id, format string) error {
	metaData, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return fmt.Errorf("image %s not found", id)
	}
	var meta ImageMeta
	json.Unmarshal(metaData, &meta)
	if meta.Crops != nil {
		delete(meta.Crops, format)
	}
	b, _ := json.Marshal(meta)
	if err := os.WriteFile(s.metaPath(id), b, 0644); err != nil {
		return err
	}
	os.Remove(s.thumbPath(id))
	return nil
}

// GetCrops returns all stored crop rects for an image.
func (s *ImageStore) GetCrops(id string) (map[string]CropRect, error) {
	metaData, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return nil, fmt.Errorf("image %s not found", id)
	}
	var meta ImageMeta
	json.Unmarshal(metaData, &meta)
	if meta.Crops == nil {
		return map[string]CropRect{}, nil
	}
	return meta.Crops, nil
}

func cropForFormat(crops map[string]CropRect, format string) (CropRect, bool) {
	if crops == nil || format == "" {
		return CropRect{}, false
	}
	r, ok := crops[format]
	if !ok || r.W <= 0 || r.H <= 0 {
		return CropRect{}, false
	}
	return r, true
}

// DeleteImage removes the stored raw file, its metadata, bin cache, and all thumb files.
func (s *ImageStore) DeleteImage(id string) error {
	os.Remove(s.rawPath(id))
	os.Remove(s.metaPath(id))
	os.Remove(s.cachePath(id))
	os.Remove(s.thumbPath(id))
	os.Remove(s.previewPath(id))
	return nil
}

const thumbW, thumbH = 320, 240
const previewMaxW, previewMaxH = 1600, 1200
const inkjoyDisplayPreviewScale = 0.5

// ServeThumb returns a JPEG thumbnail for the image, generating and caching it on first call.
func (s *ImageStore) ServeThumb(id string) ([]byte, error) {
	if data, err := os.ReadFile(s.thumbPath(id)); err == nil {
		return data, nil
	}
	return s.generateThumb(id)
}

// ServeThumbHTTP writes the JPEG thumbnail as an HTTP response.
func (s *ImageStore) ServeThumbHTTP(w http.ResponseWriter, r *http.Request, id string) {
	path := s.thumbPath(id)
	if _, err := os.Stat(path); err != nil {
		if _, genErr := s.generateThumb(id); genErr != nil {
			http.Error(w, genErr.Error(), http.StatusNotFound)
			return
		}
	}
	serveJPEGFile(w, r, path)
}

// ServePreview returns the image as a browser-displayable JPEG (for the crop editor).
// Result is cached in the thumbs dir alongside thumbnails.
func (s *ImageStore) ServePreview(id string) ([]byte, error) {
	path := s.previewPath(id)
	if data, err := os.ReadFile(path); err == nil {
		return data, nil
	}
	return s.generatePreview(id, path)
}

// ServePreviewHTTP writes the preview JPEG as an HTTP response.
func (s *ImageStore) ServePreviewHTTP(w http.ResponseWriter, r *http.Request, id string) {
	path := s.previewPath(id)
	if _, err := os.Stat(path); err != nil {
		if _, genErr := s.generatePreview(id, path); genErr != nil {
			http.Error(w, genErr.Error(), http.StatusNotFound)
			return
		}
	}
	serveJPEGFile(w, r, path)
}

func jpegFileInfo(path string) (etag string, mod time.Time, ok bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", time.Time{}, false
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())))
	return hex.EncodeToString(sum[:8]), fi.ModTime(), true
}

func cacheNotModified(r *http.Request, etag string, mod time.Time) bool {
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		for _, part := range strings.Split(inm, ",") {
			part = strings.TrimSpace(part)
			if part == `"`+etag+`"` || part == `W/"`+etag+`"` {
				return true
			}
		}
		return false
	}
	if mod.IsZero() {
		return false
	}
	if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		if t, err := http.ParseTime(ims); err == nil {
			return !mod.Truncate(time.Second).After(t)
		}
	}
	return false
}

func serveJPEGFile(w http.ResponseWriter, r *http.Request, path string) {
	etag, mod, ok := jpegFileInfo(path)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if cacheNotModified(r, etag, mod) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Last-Modified", mod.UTC().Format(http.TimeFormat))
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}

func (s *ImageStore) generatePreview(id, cachePath string) ([]byte, error) {
	meta, err := s.readMeta(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(s.rawPath(id))
	if err != nil {
		return nil, fmt.Errorf("raw file missing for %s", id)
	}

	var img image.Image
	if strings.ToLower(filepath.Ext(meta.Name)) == ".bin" {
		if len(raw) != frameW*frameH*2 {
			return nil, fmt.Errorf("bin size invalid")
		}
		hi, _ := FromBin(raw, frameW, frameH)
		img = renderHiToImage(hi)
	} else {
		img, err = decodeAnyImage(raw)
		if err != nil {
			return nil, err
		}
	}

	// Scale to fit within previewMaxW × previewMaxH, preserving aspect ratio.
	b := img.Bounds()
	pw, ph := previewMaxW, previewMaxH
	if b.Dx() <= pw && b.Dy() <= ph {
		pw, ph = b.Dx(), b.Dy() // already fits, no scale up
	} else {
		scaleX := float64(pw) / float64(b.Dx())
		scaleY := float64(ph) / float64(b.Dy())
		scale := scaleX
		if scaleY < scale {
			scale = scaleY
		}
		pw = int(float64(b.Dx()) * scale)
		ph = int(float64(b.Dy()) * scale)
	}
	img = resizeTo(img, pw, ph)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	data := buf.Bytes()
	os.MkdirAll(s.thumbDir(), 0755)
	os.WriteFile(cachePath, data, 0644)
	return data, nil
}

func (s *ImageStore) generateThumb(id string) ([]byte, error) {
	meta, err := s.readMeta(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(s.rawPath(id))
	if err != nil {
		return nil, fmt.Errorf("raw file missing for %s", id)
	}

	var img image.Image
	ext := strings.ToLower(filepath.Ext(meta.Name))
	if ext == ".bin" {
		if len(raw) != frameW*frameH*2 {
			return nil, fmt.Errorf("bin size %d invalid", len(raw))
		}
		hi, _ := FromBin(raw, frameW, frameH)
		img = renderHiToImage(hi)
	} else {
		img, err = decodeAnyImage(raw)
		if err != nil {
			return nil, err
		}
	}

	// Apply the largest-area stored crop to the thumbnail.
	if rect, ok := largestCrop(meta.Crops); ok {
		img = applyCrop(img, rect)
	}

	thumb := resizeToFit(img, thumbW, thumbH)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	data := buf.Bytes()
	os.MkdirAll(s.thumbDir(), 0755)
	os.WriteFile(s.thumbPath(id), data, 0644)
	return data, nil
}

// largestCrop returns the crop rect with the greatest normalised area (w×h).
// On a tie it prefers landscape (w≥h). Returns ok=false when the map is empty.
func largestCrop(crops map[string]CropRect) (CropRect, bool) {
	var best CropRect
	var bestArea float64
	found := false
	for _, r := range crops {
		area := r.W * r.H
		if !found || area > bestArea || (area == bestArea && r.W >= r.H) {
			best = r
			bestArea = area
			found = true
		}
	}
	return best, found
}

// resizeToFit scales img to fit within maxW×maxH, preserving aspect ratio.
func resizeToFit(img image.Image, maxW, maxH int) image.Image {
	b := img.Bounds()
	iw, ih := b.Dx(), b.Dy()
	if iw == 0 || ih == 0 {
		return img
	}
	scaleX := float64(maxW) / float64(iw)
	scaleY := float64(maxH) / float64(ih)
	scale := scaleX
	if scaleY < scale {
		scale = scaleY
	}
	w := int(float64(iw)*scale + 0.5)
	h := int(float64(ih)*scale + 0.5)
	if w > maxW {
		w = maxW
	}
	if h > maxH {
		h = maxH
	}
	return resizeTo(img, w, h)
}

// decodeBinToImage renders a frame .bin to an image. When portrait is true,
// rotates 90° CW so preview matches upright content (inverse of encode --portrait).
func decodeBinToImage(bin []byte, portrait bool) (image.Image, error) {
	if len(bin) != frameW*frameH*2 {
		return nil, fmt.Errorf("bin size %d != %d (expected %dx%dx2)", len(bin), frameW*frameH*2, frameW, frameH)
	}
	hi, _ := FromBin(bin, frameW, frameH)
	img := renderHiToImage(hi)
	if portrait {
		img = rotate90(img)
	}
	return img, nil
}

func binToDisplayPreviewJPEG(bin []byte, portrait bool) ([]byte, error) {
	img, err := decodeBinToImage(bin, portrait)
	if err != nil {
		return nil, err
	}
	preview := scaleInkJoyDisplayPreview(img)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, preview, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// scaleInkJoyDisplayPreview halves dithered frame previews with bilinear filtering
// so 2×2 ink patterns blend toward their local average color.
func scaleInkJoyDisplayPreview(img image.Image) image.Image {
	b := img.Bounds()
	w := int(float64(b.Dx())*inkjoyDisplayPreviewScale + 0.5)
	h := int(float64(b.Dy())*inkjoyDisplayPreviewScale + 0.5)
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return resizeBilinear(img, w, h)
}

// ServeInkJoyFramePreviewHTTP renders the cached send .bin at half resolution for hub UI preview.
func (s *ImageStore) ServeInkJoyFramePreviewHTTP(w http.ResponseWriter, r *http.Request, id string, portrait bool) {
	bin, err := s.ServeBinOrientation(id, portrait)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	jpeg, err := binToDisplayPreviewJPEG(bin, portrait)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(jpeg)
}

// renderHiToImage converts hi-byte grid to an RGBA image using the InkJoy palette.
func renderHiToImage(hi [][]byte) image.Image {
	h := len(hi)
	if h == 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	w := len(hi[0])
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y, row := range hi {
		for x, hb := range row {
			idx, ok := hiByteToIdx[hb]
			if !ok {
				idx = 0
			}
			c := PaletteInkJoy[idx]
			dst.Set(x, y, color.RGBA{uint8(c[0]), uint8(c[1]), uint8(c[2]), 255})
		}
	}
	return dst
}

// resizeBilinear scales img using bilinear interpolation (center-sampled).
func resizeBilinear(img image.Image, w, h int) image.Image {
	b := img.Bounds()
	if b.Dx() == w && b.Dy() == h {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	srcW, srcH := b.Dx(), b.Dy()
	scaleX := float64(srcW) / float64(w)
	scaleY := float64(srcH) / float64(h)
	minX, minY := b.Min.X, b.Min.Y
	maxX, maxY := b.Max.X-1, b.Max.Y-1

	for dy := range h {
		sy := (float64(dy)+0.5)*scaleY - 0.5 + float64(minY)
		y0 := int(math.Floor(sy))
		y1 := y0 + 1
		fy := sy - float64(y0)
		if y0 < minY {
			y0 = minY
			y1 = minY
			fy = 0
		} else if y1 > maxY {
			y1 = maxY
			fy = 1
			if y0 > maxY {
				y0 = maxY
				fy = 0
			}
		}
		for dx := range w {
			sx := (float64(dx)+0.5)*scaleX - 0.5 + float64(minX)
			x0 := int(math.Floor(sx))
			x1 := x0 + 1
			fx := sx - float64(x0)
			if x0 < minX {
				x0 = minX
				x1 = minX
				fx = 0
			} else if x1 > maxX {
				x1 = maxX
				fx = 1
				if x0 > maxX {
					x0 = maxX
					fx = 0
				}
			}
			dst.Set(dx, dy, bilinearSample(img, x0, y0, x1, y1, fx, fy))
		}
	}
	return dst
}

func bilinearSample(img image.Image, x0, y0, x1, y1 int, fx, fy float64) color.RGBA {
	c00 := colorRGBA8(img.At(x0, y0))
	c10 := colorRGBA8(img.At(x1, y0))
	c01 := colorRGBA8(img.At(x0, y1))
	c11 := colorRGBA8(img.At(x1, y1))
	lerp := func(a, b uint8, t float64) uint8 {
		return uint8(float64(a)*(1-t) + float64(b)*t + 0.5)
	}
	topR := lerp(c00.R, c10.R, fx)
	topG := lerp(c00.G, c10.G, fx)
	topB := lerp(c00.B, c10.B, fx)
	botR := lerp(c01.R, c11.R, fx)
	botG := lerp(c01.G, c11.G, fx)
	botB := lerp(c01.B, c11.B, fx)
	return color.RGBA{
		R: lerp(topR, botR, fy),
		G: lerp(topG, botG, fy),
		B: lerp(topB, botB, fy),
		A: 255,
	}
}

func colorRGBA8(c color.Color) color.RGBA {
	r, g, b, a := c.RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

// resizeTo scales img to the given dimensions using nearest-neighbour.
func resizeTo(img image.Image, w, h int) image.Image {
	b := img.Bounds()
	if b.Dx() == w && b.Dy() == h {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	scaleX := float64(b.Dx()) / float64(w)
	scaleY := float64(b.Dy()) / float64(h)
	for dy := range h {
		sy := int(float64(dy)*scaleY) + b.Min.Y
		for dx := range w {
			sx := int(float64(dx)*scaleX) + b.Min.X
			dst.Set(dx, dy, img.At(sx, sy))
		}
	}
	return dst
}

// ── conversion ───────────────────────────────────────────────────────────────

func (s *ImageStore) readMeta(id string) (ImageMeta, error) {
	var meta ImageMeta
	data, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return meta, fmt.Errorf("image %s not found", id)
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("corrupt metadata for %s", id)
	}
	return meta, nil
}

func (s *ImageStore) convertToBinOrientation(id string, portrait bool) ([]byte, error) {
	meta, err := s.readMeta(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(s.rawPath(id))
	if err != nil {
		return nil, fmt.Errorf("raw file missing for %s", id)
	}

	ext := strings.ToLower(filepath.Ext(meta.Name))
	if ext == ".bin" {
		return convertBin(raw)
	}
	cropKey := "4:3"
	if portrait {
		cropKey = "3:4"
	}
	return convertImageWithCrop(raw, meta.Crops[cropKey], portrait, meta.FlatRGB)
}

func isFlatCalibrationName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "color-guesses") || strings.Contains(lower, "color-primaries")
}

func convertBin(raw []byte) ([]byte, error) {
	if len(raw) != frameW*frameH*2 {
		return nil, fmt.Errorf("bin size %d != %d (expected %dx%dx2)", len(raw), frameW*frameH*2, frameW, frameH)
	}
	return raw, nil
}

func convertImageWithCrop(raw []byte, crop CropRect, portrait bool, flatRGB bool) ([]byte, error) {
	img, err := decodeAnyImage(raw)
	if err != nil {
		return nil, err
	}
	if crop.W > 0 && crop.H > 0 {
		img = applyCrop(img, crop)
	}
	if portrait {
		// Rotate the 3:4 cropped content 90° CCW, then resize to 1600×1200.
		// This matches encode_bin.py --portrait: rotate first, then resize to (tw, th).
		img = rotate90CCW(img)
		img = resizeToFrame(img)
	} else {
		img = resizeToFrame(img)
	}
	var hi, lo [][]byte
	if flatRGB {
		// Calibration swatches: flat per-pixel snap (no LAB/Stucki). Matches pre-built .bin.
		hi, lo = snapToPalette(img)
	} else {
		indices := StuckiTwoPalette(img, PaletteInkJoyDisplay, UniqueColors(img) > 6)
		hi = indicesToHi(indices)
		lo = randomWipeGrid()
	}
	return ToBin(hi, lo), nil
}

// applyCrop extracts the crop rect (normalized 0–1) from img.
func applyCrop(img image.Image, crop CropRect) image.Image {
	b := img.Bounds()
	fw, fh := float64(b.Dx()), float64(b.Dy())
	x0 := b.Min.X + int(crop.X*fw)
	y0 := b.Min.Y + int(crop.Y*fh)
	x1 := b.Min.X + int((crop.X+crop.W)*fw)
	y1 := b.Min.Y + int((crop.Y+crop.H)*fh)
	if x1 > b.Max.X {
		x1 = b.Max.X
	}
	if y1 > b.Max.Y {
		y1 = b.Max.Y
	}
	type subImager interface {
		SubImage(image.Rectangle) image.Image
	}
	if si, ok := img.(subImager); ok {
		return si.SubImage(image.Rect(x0, y0, x1, y1))
	}
	dst := image.NewRGBA(image.Rect(0, 0, x1-x0, y1-y0))
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			dst.Set(x-x0, y-y0, img.At(x, y))
		}
	}
	return dst
}

// ── cache eviction ────────────────────────────────────────────────────────────

type cacheEntry struct {
	path  string
	size  int64
	mtime int64
}

func (s *ImageStore) evictCache() {
	entries, err := os.ReadDir(s.cacheDir())
	if err != nil {
		return
	}

	var files []cacheEntry
	var total int64
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, cacheEntry{
			path:  filepath.Join(s.cacheDir(), e.Name()),
			size:  fi.Size(),
			mtime: fi.ModTime().UnixNano(),
		})
		total += fi.Size()
	}

	if total <= s.CacheMax {
		return
	}

	// Remove oldest first.
	sort.Slice(files, func(i, j int) bool { return files[i].mtime < files[j].mtime })
	for _, f := range files {
		if total <= s.CacheMax {
			break
		}
		os.Remove(f.path)
		total -= f.size
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func decodeAnyImage(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytesReader(data))
	if err != nil {
		return nil, errors.New("unsupported image format (accept .bin, PNG, JPEG, or HEIC)")
	}
	return applyExifOrientation(img, readExifOrientation(data)), nil
}

func imageDisplaySize(raw []byte, name string) (int, int, error) {
	if strings.ToLower(filepath.Ext(name)) == ".bin" {
		return frameW, frameH, nil
	}
	img, err := decodeAnyImage(raw)
	if err != nil {
		return 0, 0, err
	}
	b := img.Bounds()
	return b.Dx(), b.Dy(), nil
}

func (s *ImageStore) fillDimensions(meta *ImageMeta) {
	if meta.Width > 0 && meta.Height > 0 {
		return
	}
	raw, err := os.ReadFile(s.rawPath(meta.ID))
	if err != nil {
		return
	}
	w, h, err := imageDisplaySize(raw, meta.Name)
	if err != nil || w <= 0 || h <= 0 {
		return
	}
	meta.Width, meta.Height = w, h
	b, _ := json.Marshal(meta)
	os.WriteFile(s.metaPath(meta.ID), b, 0644)
}

func resizeToFrame(img image.Image) image.Image { return resizeTo(img, frameW, frameH) }

func snapToPalette(img image.Image) (hi, lo [][]byte) {
	b := img.Bounds()
	h, w := b.Dy(), b.Dx()
	hi = make([][]byte, h)
	lo = make([][]byte, h)
	wipe := randomWipeGrid()
	for y := range h {
		hi[y] = make([]byte, w)
		for x := range w {
			r, g, bv, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			idx := nearestColor([3]float64{float64(r >> 8), float64(g >> 8), float64(bv >> 8)}, PaletteInkJoySend)
			hi[y][x] = hiBytes[idx]
		}
		lo[y] = wipe[y]
	}
	return
}

func indicesToHi(indices [][]byte) [][]byte {
	hi := make([][]byte, len(indices))
	for y, row := range indices {
		hi[y] = make([]byte, len(row))
		for x, idx := range row {
			hi[y][x] = hiBytes[idx]
		}
	}
	return hi
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type byteReader struct {
	data []byte
	pos  int
}

func (b *byteReader) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	return n, nil
}

func bytesReader(data []byte) io.Reader { return &byteReader{data: data} }
