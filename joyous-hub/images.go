package main

import (
	"bytes"
	"crypto/rand"
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
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	ID    string               `json:"id"`
	Name  string               `json:"name"`
	Size  int64                `json:"size"`
	Crops map[string]CropRect  `json:"crops,omitempty"`
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
	meta := ImageMeta{ID: id, Name: name, Size: int64(len(data))}
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

	return bin, nil
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
	os.Remove(filepath.Join(s.thumbDir(), id+"_preview.jpg"))
	return nil
}

const thumbW, thumbH = 320, 240
const previewMaxW, previewMaxH = 1600, 1200

// ServeThumb returns a JPEG thumbnail for the image, generating and caching it on first call.
func (s *ImageStore) ServeThumb(id string) ([]byte, error) {
	if data, err := os.ReadFile(s.thumbPath(id)); err == nil {
		return data, nil
	}
	return s.generateThumb(id)
}

// ServeThumbHTTP writes the JPEG thumbnail as an HTTP response.
func (s *ImageStore) ServeThumbHTTP(w http.ResponseWriter, r *http.Request, id string) {
	data, err := s.ServeThumb(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(data)
}

// ServePreview returns the image as a browser-displayable JPEG (for the crop editor).
// Result is cached in the thumbs dir alongside thumbnails.
func (s *ImageStore) ServePreview(id string) ([]byte, error) {
	path := filepath.Join(s.thumbDir(), id+"_preview.jpg")
	if data, err := os.ReadFile(path); err == nil {
		return data, nil
	}
	return s.generatePreview(id, path)
}

// ServePreviewHTTP writes the preview JPEG as an HTTP response.
func (s *ImageStore) ServePreviewHTTP(w http.ResponseWriter, r *http.Request, id string) {
	data, err := s.ServePreview(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
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
	return convertImageWithCrop(raw, meta.Crops[cropKey], portrait)
}

func convertBin(raw []byte) ([]byte, error) {
	if len(raw) != frameW*frameH*2 {
		return nil, fmt.Errorf("bin size %d != %d (expected %dx%dx2)", len(raw), frameW*frameH*2, frameW, frameH)
	}
	return raw, nil
}

func convertImageWithCrop(raw []byte, crop CropRect, portrait bool) ([]byte, error) {
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
	n := UniqueColors(img)
	var hi, lo [][]byte
	if n <= 6 {
		hi, lo = snapToPalette(img)
	} else {
		enhanced := LABEnhance(img, 1.0)
		indices := StuckiDither(enhanced, PaletteInkJoy)
		hi = indicesToHi(indices)
		lo = MakeClockWipe(frameW, frameH)
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

func resizeToFrame(img image.Image) image.Image { return resizeTo(img, frameW, frameH) }

func snapToPalette(img image.Image) (hi, lo [][]byte) {
	b := img.Bounds()
	h, w := b.Dy(), b.Dx()
	hi = make([][]byte, h)
	lo = make([][]byte, h)
	wipe := MakeClockWipe(w, h)
	for y := range h {
		hi[y] = make([]byte, w)
		for x := range w {
			r, g, bv, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			idx := nearestColor([3]float64{float64(r >> 8), float64(g >> 8), float64(bv >> 8)}, PaletteInkJoy)
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
