package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStoreRawPreservesBytes: Store saves the uploaded bytes exactly as-is.
func TestStoreRawPreservesBytes(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)

	data := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0x02, 0x03}
	id, err := store.Store(bytes.NewReader(data), "test.bin")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Read the raw file back directly from disk (not via ServeBin which converts).
	raw, err := os.ReadFile(filepath.Join(dir, "images", id))
	if err != nil {
		t.Fatalf("read raw file: %v", err)
	}
	if !bytes.Equal(raw, data) {
		t.Errorf("raw bytes mismatch: got %v want %v", raw, data)
	}
}

// TestServeBinFromBin: Store a valid full-frame .bin → ServeBin returns the original bytes.
func TestServeBinFromBin(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)

	bin := make([]byte, frameW*frameH*2)
	for i := 0; i < len(bin); i += 2 {
		bin[i] = 0x01 // all black
	}

	id, err := store.Store(bytes.NewReader(bin), "photo.bin")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	got, err := store.ServeBin(id)
	if err != nil {
		t.Fatalf("ServeBin: %v", err)
	}
	if !bytes.Equal(got, bin) {
		t.Errorf("bin roundtrip mismatch: got %d bytes want %d bytes", len(got), len(bin))
	}
}

// TestServeBinCached: second ServeBin call reads from cache file, not re-converting.
func TestServeBinCached(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)

	bin := make([]byte, frameW*frameH*2)
	for i := 0; i < len(bin); i += 2 {
		bin[i] = 0x02 // all white
	}
	id, err := store.Store(bytes.NewReader(bin), "photo.bin")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	out1, err := store.ServeBin(id)
	if err != nil {
		t.Fatalf("first ServeBin: %v", err)
	}

	// Corrupt the raw file — second serve must still succeed (using cache).
	os.WriteFile(filepath.Join(dir, "images", id), []byte("corrupted"), 0644)

	out2, err := store.ServeBin(id)
	if err != nil {
		t.Fatalf("second ServeBin: %v", err)
	}
	if !bytes.Equal(out1, out2) {
		t.Error("second serve returned different bytes — cache not used")
	}
}

// TestCacheBounded: cache is evicted to stay within CacheMax.
func TestCacheBounded(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)
	binSize := int64(frameW * frameH * 2)
	store.CacheMax = binSize + 1 // fits exactly one converted .bin

	makeFrameBin := func(hiByte byte) []byte {
		b := make([]byte, frameW*frameH*2)
		for i := 0; i < len(b); i += 2 {
			b[i] = hiByte
		}
		return b
	}

	id1, _ := store.Store(bytes.NewReader(makeFrameBin(0x01)), "a.bin")
	id2, _ := store.Store(bytes.NewReader(makeFrameBin(0x02)), "b.bin")

	store.ServeBin(id1)
	time.Sleep(10 * time.Millisecond) // ensure distinct mtimes
	store.ServeBin(id2)

	// Total cache size must not exceed CacheMax.
	var total int64
	entries, _ := os.ReadDir(filepath.Join(dir, "cache"))
	for _, e := range entries {
		if fi, err := e.Info(); err == nil {
			total += fi.Size()
		}
	}
	if total > store.CacheMax {
		t.Errorf("cache size %d exceeds CacheMax %d", total, store.CacheMax)
	}
}

// TestServeBinFromPNG: Store a PNG → ServeBin returns a valid full-frame .bin.
func TestServeBinFromPNG(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PNG conversion test in short mode")
	}
	dir := t.TempDir()
	store := NewImageStore(dir)

	img := image.NewRGBA(image.Rect(0, 0, frameW, frameH))
	palColors := []color.RGBA{
		{30, 30, 30, 255}, {149, 162, 165, 255}, {166, 165, 17, 255},
		{121, 23, 17, 255}, {0, 76, 136, 255}, {46, 91, 65, 255},
	}
	for y := range frameH {
		for x := range frameW {
			img.Set(x, y, palColors[(x+y)%6])
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}

	id, err := store.Store(bytes.NewReader(buf.Bytes()), "palette.png")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	got, err := store.ServeBin(id)
	if err != nil {
		t.Fatalf("ServeBin: %v", err)
	}
	if len(got) != frameW*frameH*2 {
		t.Errorf("ServeBin size: got %d want %d", len(got), frameW*frameH*2)
	}
	validHi := map[byte]bool{0x01: true, 0x02: true, 0x03: true, 0x04: true, 0x06: true, 0x07: true}
	for i := 0; i < len(got); i += 2 {
		if !validHi[got[i]] {
			t.Errorf("invalid hi byte 0x%02x at offset %d", got[i], i)
			break
		}
	}
}

func TestFlatCalibrationNameDetection(t *testing.T) {
	if !isFlatCalibrationName("color-guesses-1600x1200.png") {
		t.Error("color-guesses should be flat calibration")
	}
	if !isFlatCalibrationName("color-primaries-1600x1200.bin") {
		t.Error("color-primaries should be flat calibration")
	}
	if isFlatCalibrationName("sunset.jpg") {
		t.Error("normal photo should not be flat calibration")
	}
}

func TestColorGuessesPNGUsesFlatSnap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	dir := t.TempDir()
	store := NewImageStore(dir)

	raw, err := os.ReadFile(filepath.Join("..", "Samsung", "color-guesses-1600x1200.png"))
	if err != nil {
		t.Skip("color-guesses PNG not generated:", err)
	}
	id, err := store.Store(bytes.NewReader(raw), "color-guesses-1600x1200.png")
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.readMeta(id)
	if err != nil || !meta.FlatRGB {
		t.Fatalf("FlatRGB meta: %+v err=%v", meta, err)
	}
	bin, err := store.ServeBin(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(bin) != frameW*frameH*2 {
		t.Fatalf("bin size %d", len(bin))
	}
	_, lo1 := FromBin(bin, frameW, frameH)
	bin2, err := store.ServeBin(id)
	if err != nil {
		t.Fatal(err)
	}
	_, lo2 := FromBin(bin2, frameW, frameH)
	if wipeFingerprint(lo1) != wipeFingerprint(lo2) {
		t.Fatal("flat calibration wipe should be stable across serves")
	}
	if wipeFingerprint(lo1) != wipeFingerprint(calibrationWipeGrid()) {
		t.Fatal("flat calibration should use vertical sweep wipe")
	}
}

// TestRenameImage updates display name in metadata without changing the raw file.
func TestRenameImage(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id, _ := store.Store(bytes.NewReader([]byte{1, 2, 3, 4}), "vacation.jpg")

	meta, err := store.Rename(id, "Sunset at the lake")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "Sunset at the lake" {
		t.Fatalf("got name %q", meta.Name)
	}
	reloaded, err := store.readMeta(id)
	if err != nil || reloaded.Name != "Sunset at the lake" {
		t.Fatalf("reload: %+v err=%v", reloaded, err)
	}
	if _, err := store.Rename(id, "  "); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestAlbumRevision(t *testing.T) {
	store := NewImageStore(t.TempDir())
	if rev := store.AlbumRevision(); rev != "empty" {
		t.Fatalf("empty store: %q", rev)
	}
	id1, _ := store.Store(bytes.NewReader([]byte{1}), "a.jpg")
	rev1 := store.AlbumRevision()
	id2, _ := store.Store(bytes.NewReader([]byte{2, 3}), "b.jpg")
	rev2 := store.AlbumRevision()
	if rev1 == rev2 {
		t.Fatal("revision should change after upload")
	}
	if _, err := store.Rename(id1, "renamed.jpg"); err != nil {
		t.Fatal(err)
	}
	rev3 := store.AlbumRevision()
	if rev3 == rev2 {
		t.Fatal("revision should change after rename")
	}
	store.DeleteImage(id2)
	rev4 := store.AlbumRevision()
	if rev4 == rev3 {
		t.Fatal("revision should change after delete")
	}
}

// TestDeleteCrop: DeleteCrop removes a stored crop from metadata.
func TestDeleteCrop(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id, _ := store.Store(bytes.NewReader([]byte{1, 2, 3}), "test.jpg")

	store.SetCrop(id, "4:3", CropRect{X: 0.1, Y: 0.1, W: 0.8, H: 0.8})
	store.SetCrop(id, "3:4", CropRect{X: 0.2, Y: 0.0, W: 0.6, H: 1.0})

	if err := store.DeleteCrop(id, "4:3"); err != nil {
		t.Fatalf("DeleteCrop: %v", err)
	}

	crops, _ := store.GetCrops(id)
	if _, ok := crops["4:3"]; ok {
		t.Error("4:3 crop should be deleted")
	}
	if _, ok := crops["3:4"]; !ok {
		t.Error("3:4 crop should still exist")
	}
}

// TestLargestCrop: largestCrop returns the crop with the greatest area,
// preferring landscape on a tie.
func TestLargestCrop(t *testing.T) {
	crops := map[string]CropRect{
		"3:4": {X: 0.1, Y: 0.0, W: 0.5, H: 1.0},  // area 0.5
		"4:3": {X: 0.0, Y: 0.1, W: 1.0, H: 0.75}, // area 0.75  ← largest
		"1:1": {X: 0.1, Y: 0.1, W: 0.8, H: 0.8},  // area 0.64
	}
	got, ok := largestCrop(crops)
	if !ok {
		t.Fatal("expected a result")
	}
	if got.W != 1.0 || got.H != 0.75 {
		t.Errorf("expected 4:3 crop, got %+v", got)
	}

	// Tie-break: landscape wins.
	tie := map[string]CropRect{
		"3:4": {W: 0.75, H: 1.0}, // area 0.75, portrait
		"4:3": {W: 1.0, H: 0.75}, // area 0.75, landscape  ← should win
	}
	got2, _ := largestCrop(tie)
	if got2.W < got2.H {
		t.Errorf("tie-break should prefer landscape, got %+v", got2)
	}
}

// TestSaveCrop: SetCrop persists the rect and it appears in subsequent metadata.
func TestSaveCrop(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id, _ := store.Store(bytes.NewReader([]byte{1, 2, 3}), "test.jpg")

	rect := CropRect{X: 0.1, Y: 0.05, W: 0.8, H: 0.6}
	if err := store.SetCrop(id, "4:3", rect); err != nil {
		t.Fatalf("SetCrop: %v", err)
	}

	crops, err := store.GetCrops(id)
	if err != nil {
		t.Fatalf("GetCrops: %v", err)
	}
	got, ok := crops["4:3"]
	if !ok {
		t.Fatal("crop not stored for 4:3")
	}
	if got.X != 0.1 || got.Y != 0.05 || got.W != 0.8 || got.H != 0.6 {
		t.Errorf("crop mismatch: %+v", got)
	}
}

// TestServeBinAppliesCrop: when a 4:3 crop is set, bin content reflects only that region.
func TestServeBinAppliesCrop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping crop bin test in short mode")
	}
	dir := t.TempDir()
	store := NewImageStore(dir)

	// Left half black, right half white-ish.
	img := image.NewRGBA(image.Rect(0, 0, frameW, frameH))
	for y := range frameH {
		for x := range frameW {
			if x < frameW/2 {
				img.Set(x, y, color.RGBA{30, 30, 30, 255})
			} else {
				img.Set(x, y, color.RGBA{200, 200, 200, 255})
			}
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	id, _ := store.Store(bytes.NewReader(buf.Bytes()), "halves.png")

	// Crop to the right (white) half.
	store.SetCrop(id, "4:3", CropRect{X: 0.5, Y: 0.0, W: 0.5, H: 1.0})
	os.Remove(filepath.Join(dir, "cache", id+".bin")) // force regeneration

	bin, err := store.ServeBin(id)
	if err != nil {
		t.Fatalf("ServeBin: %v", err)
	}
	if len(bin) != frameW*frameH*2 {
		t.Fatalf("bin size %d want %d", len(bin), frameW*frameH*2)
	}
	// All hi bytes should be white (0x02).
	for i := 0; i < len(bin); i += 2 {
		if bin[i] != 0x02 {
			t.Errorf("expected white (0x02) hi byte at offset %d, got 0x%02x", i, bin[i])
			break
		}
	}
}

// TestServePreview: ServePreview returns JPEG bytes for any stored image.
func TestServePreview(t *testing.T) {
	store := NewImageStore(t.TempDir())

	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	var buf bytes.Buffer
	png.Encode(&buf, img)
	id, _ := store.Store(bytes.NewReader(buf.Bytes()), "test.png")

	data, err := store.ServePreview(id)
	if err != nil {
		t.Fatalf("ServePreview: %v", err)
	}
	if data[0] != 0xFF || data[1] != 0xD8 {
		t.Errorf("not a JPEG: %02x %02x", data[0], data[1])
	}
}

// TestThumbUpdateAfterCrop: SetCrop invalidates the thumbnail cache so the next
// ServeThumb call generates a new thumbnail reflecting the crop.
func TestThumbUpdateAfterCrop(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)

	bin := make([]byte, frameW*frameH*2)
	for i := 0; i < len(bin); i += 2 {
		bin[i] = 0x01 // all black
	}
	id, _ := store.Store(bytes.NewReader(bin), "photo.bin")

	// Generate initial thumbnail.
	before, _ := store.ServeThumb(id)
	thumbFile := filepath.Join(dir, "thumbs", id+".jpg")

	// Set a crop (changes 0x01 black to covering only part of the image).
	store.SetCrop(id, "4:3", CropRect{X: 0.0, Y: 0.0, W: 1.0, H: 1.0})

	// Thumbnail cache file should be gone after SetCrop.
	if _, err := os.Stat(thumbFile); !os.IsNotExist(err) {
		t.Error("SetCrop should have removed the cached thumbnail")
	}

	// Re-generate — must not error.
	after, err := store.ServeThumb(id)
	if err != nil {
		t.Fatalf("ServeThumb after crop: %v", err)
	}
	_ = before
	_ = after
}

// TestServeThumbFromBin: Store a .bin → ServeThumb returns a valid JPEG.
func TestServeThumbFromBin(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)

	bin := make([]byte, frameW*frameH*2)
	for i := 0; i < len(bin); i += 2 {
		bin[i] = 0x04 // all red
	}
	id, err := store.Store(bytes.NewReader(bin), "photo.bin")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	thumb, err := store.ServeThumb(id)
	if err != nil {
		t.Fatalf("ServeThumb: %v", err)
	}
	if len(thumb) < 100 {
		t.Fatalf("thumb too small: %d bytes", len(thumb))
	}
	// JPEG magic bytes: FF D8 FF
	if thumb[0] != 0xFF || thumb[1] != 0xD8 {
		t.Errorf("not a JPEG: first bytes %02x %02x", thumb[0], thumb[1])
	}
}

// TestServeThumbFromPNG: Store a PNG → ServeThumb returns a valid JPEG.
func TestServeThumbFromPNG(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)

	img := image.NewRGBA(image.Rect(0, 0, 100, 80))
	for y := range 80 {
		for x := range 100 {
			img.Set(x, y, color.RGBA{uint8(x * 2), uint8(y * 3), 100, 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)

	id, err := store.Store(bytes.NewReader(buf.Bytes()), "test.png")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	thumb, err := store.ServeThumb(id)
	if err != nil {
		t.Fatalf("ServeThumb: %v", err)
	}
	if thumb[0] != 0xFF || thumb[1] != 0xD8 {
		t.Errorf("not a JPEG: first bytes %02x %02x", thumb[0], thumb[1])
	}
}

// TestServeThumbCached: second call to ServeThumb uses the cached file.
func TestServeThumbCached(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)

	bin := make([]byte, frameW*frameH*2)
	for i := 0; i < len(bin); i += 2 {
		bin[i] = 0x02
	}
	id, _ := store.Store(bytes.NewReader(bin), "photo.bin")
	store.ServeThumb(id)

	// Corrupt the raw file — second serve must still succeed from thumb cache.
	os.WriteFile(filepath.Join(dir, "images", id), []byte("corrupted"), 0644)

	_, err := store.ServeThumb(id)
	if err != nil {
		t.Fatalf("second ServeThumb (should use cache): %v", err)
	}
}

// TestServeBinFromHEIC: Store a HEIC photo → ServeBin returns a valid full-frame .bin.
// Requires sips (macOS) to generate the test fixture; skipped in -short mode.
func TestServeBinFromHEIC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping HEIC test in short mode")
	}
	sipsPath, err := exec.LookPath("sips")
	if err != nil {
		t.Skip("sips not found — skipping HEIC test")
	}

	// Build a small colourful PNG to convert to HEIC via sips.
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for y := range 240 {
		for x := range 320 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	tmp := t.TempDir()
	pngPath := filepath.Join(tmp, "src.png")
	heicPath := filepath.Join(tmp, "src.heic")
	f, _ := os.Create(pngPath)
	png.Encode(f, img)
	f.Close()

	if out, err := exec.Command(sipsPath, "-s", "format", "heic", pngPath, "--out", heicPath).CombinedOutput(); err != nil {
		t.Skipf("sips conversion failed (%v): %s", err, out)
	}

	heicData, _ := os.ReadFile(heicPath)

	store := NewImageStore(t.TempDir())
	id, err := store.Store(bytes.NewReader(heicData), "photo.heic")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	bin, err := store.ServeBin(id)
	if err != nil {
		t.Fatalf("ServeBin: %v", err)
	}
	if len(bin) != frameW*frameH*2 {
		t.Errorf("bin size: got %d want %d", len(bin), frameW*frameH*2)
	}
	validHi := map[byte]bool{0x01: true, 0x02: true, 0x03: true, 0x04: true, 0x06: true, 0x07: true}
	for i := 0; i < len(bin); i += 2 {
		if !validHi[bin[i]] {
			t.Errorf("invalid hi byte 0x%02x at offset %d", bin[i], i)
			break
		}
	}
}

// TestServeContentType: /images/{id}.bin response has Content-Type: application/octet-stream.
func TestServeContentType(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)

	bin := make([]byte, frameW*frameH*2)
	for i := 0; i < len(bin); i += 2 {
		bin[i] = 0x02
	}
	id, err := store.Store(bytes.NewReader(bin), "x.bin")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		store.ServeBinHTTP(w, r, id)
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/images/" + id + ".bin")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	ct := resp.Header.Get("Content-Type")
	if ct != "application/octet-stream" {
		t.Errorf("Content-Type: got %q want %q", ct, "application/octet-stream")
	}
}

func TestServeThumbHTTPCaching(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)
	bin := make([]byte, frameW*frameH*2)
	id, _ := store.Store(bytes.NewReader(bin), "photo.bin")

	rec := httptest.NewRecorder()
	store.ServeThumbHTTP(rec, httptest.NewRequest(http.MethodGet, "/images/"+id+"/thumb", nil), id)
	if rec.Code != http.StatusOK {
		t.Fatalf("first fetch %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag")
	}
	if rec.Header().Get("Last-Modified") == "" {
		t.Fatal("expected Last-Modified")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/images/"+id+"/thumb", nil)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	store.ServeThumbHTTP(rec2, req2, id)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match: expected 304, got %d", rec2.Code)
	}

	req3 := httptest.NewRequest(http.MethodGet, "/images/"+id+"/thumb", nil)
	req3.Header.Set("If-Modified-Since", rec.Header().Get("Last-Modified"))
	rec3 := httptest.NewRecorder()
	store.ServeThumbHTTP(rec3, req3, id)
	if rec3.Code != http.StatusNotModified {
		t.Fatalf("If-Modified-Since: expected 304, got %d", rec3.Code)
	}
}

func TestServePreviewHTTPCaching(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)
	bin := make([]byte, frameW*frameH*2)
	id, _ := store.Store(bytes.NewReader(bin), "photo.bin")

	rec := httptest.NewRecorder()
	store.ServePreviewHTTP(rec, httptest.NewRequest(http.MethodGet, "/images/"+id+"/preview", nil), id)
	if rec.Code != http.StatusOK {
		t.Fatalf("first fetch %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/images/"+id+"/preview", nil)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	store.ServePreviewHTTP(rec2, req2, id)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", rec2.Code)
	}
}

func TestServeThumbHTTPETagChangesAfterCrop(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)
	bin := make([]byte, frameW*frameH*2)
	id, _ := store.Store(bytes.NewReader(bin), "photo.bin")

	rec := httptest.NewRecorder()
	store.ServeThumbHTTP(rec, httptest.NewRequest(http.MethodGet, "/images/"+id+"/thumb", nil), id)
	etag1 := rec.Header().Get("ETag")

	if err := store.SetCrop(id, "4:3", CropRect{X: 0.1, Y: 0.1, W: 0.8, H: 0.6}); err != nil {
		t.Fatal(err)
	}

	rec2 := httptest.NewRecorder()
	store.ServeThumbHTTP(rec2, httptest.NewRequest(http.MethodGet, "/images/"+id+"/thumb", nil), id)
	etag2 := rec2.Header().Get("ETag")
	if etag1 == etag2 {
		t.Fatalf("etag should change after crop: %s", etag1)
	}

	req := httptest.NewRequest(http.MethodGet, "/images/"+id+"/thumb", nil)
	req.Header.Set("If-None-Match", etag1)
	rec3 := httptest.NewRecorder()
	store.ServeThumbHTTP(rec3, req, id)
	if rec3.Code != http.StatusOK {
		t.Fatalf("stale etag should return 200, got %d", rec3.Code)
	}
}

func TestColorPipelineForMetaChromaOverride(t *testing.T) {
	store := NewImageStore(t.TempDir())
	on := true
	off := false
	meta := ImageMeta{ChromaBoost: &on}
	pipe := store.colorPipelineForMeta(meta)
	if !pipe.LABChromaEnabled {
		t.Fatal("expected chroma on from per-image override")
	}
	meta.ChromaBoost = &off
	pipe = store.colorPipelineForMeta(meta)
	if pipe.LABChromaEnabled {
		t.Fatal("expected chroma off from per-image override")
	}
	meta.ChromaBoost = nil
	pipe = store.colorPipelineForMeta(meta)
	if pipe.LABChromaEnabled {
		t.Fatal("expected global default chroma off")
	}
}

func TestPatchMetaChromaEvictsCache(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(dir)
	id, _ := store.Store(bytes.NewReader([]byte{0x01, 0x02}), "photo.jpg")
	cacheFile := filepath.Join(dir, "cache", id+".bin")
	if err := os.MkdirAll(filepath.Join(dir, "cache"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheFile, []byte("cached"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PatchMeta(id, nil, "on"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cacheFile); !os.IsNotExist(err) {
		t.Fatal("expected bin cache evicted after chroma change")
	}
	meta, err := store.readMeta(id)
	if err != nil || meta.ChromaBoost == nil || !*meta.ChromaBoost {
		t.Fatalf("meta=%+v err=%v", meta, err)
	}
}

func TestServeOriginalHTTP(t *testing.T) {
	store := NewImageStore(t.TempDir())
	raw := []byte{0x89, 0x50, 0x4e, 0x47, 0x01, 0x02}
	id, _ := store.Store(bytes.NewReader(raw), "holiday snap.png")

	rec := httptest.NewRecorder()
	store.ServeOriginalHTTP(rec, httptest.NewRequest(http.MethodGet, "/images/"+id+"/original", nil), id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), raw) {
		t.Fatal("body mismatch")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type: %q", ct)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, `filename="holiday snap.png"`) {
		t.Fatalf("Content-Disposition: %q", cd)
	}
}
