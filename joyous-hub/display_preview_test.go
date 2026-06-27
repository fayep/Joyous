package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParsePlayBinURL(t *testing.T) {
	payload, _ := buildPlayPayload("AA:BB:CC:DD:EE:FF", "http://192.168.1.7:8080/images/abc-p.bin")
	url, ok := parsePlayBinURL(payload)
	if !ok {
		t.Fatal("expected ok")
	}
	if url != "http://192.168.1.7:8080/images/abc-p.bin" {
		t.Fatalf("url=%q", url)
	}

	httpsPayload := []byte(`{"action":"play","data":{"host":"cdn.example.com","port":443,"imgs":[{"imgurl":"/path/img.bin"}]}}`)
	url, ok = parsePlayBinURL(httpsPayload)
	if !ok || url != "https://cdn.example.com:443/path/img.bin" {
		t.Fatalf("https url=%q ok=%v", url, ok)
	}
}

func TestDecodeBinToImagePortrait(t *testing.T) {
	// Solid red in top-left of landscape buffer → after portrait decode, appears top-right of portrait view.
	bin := make([]byte, frameW*frameH*2)
	for i := 0; i < len(bin); i += 2 {
		bin[i] = 0x01
	}
	// Set one red pixel at landscape (0,0) → hi row h-1 in FromBin order
	hi, _ := FromBin(bin, frameW, frameH)
	hi[0][0] = 0x04
	bin = ToBin(hi, loGrid(hi))

	land, err := decodeBinToImage(bin, false)
	if err != nil {
		t.Fatal(err)
	}
	red := color.RGBA{uint8(PaletteInkJoy[3][0]), uint8(PaletteInkJoy[3][1]), uint8(PaletteInkJoy[3][2]), 255}
	if !colorsEqual(land.At(0, 0), red) {
		t.Fatalf("landscape red at (0,0), got %v", land.At(0, 0))
	}

	port, err := decodeBinToImage(bin, true)
	if err != nil {
		t.Fatal(err)
	}
	b := port.Bounds()
	// 90° CW: landscape (0,0) → portrait (b.Dx()-1, 0)
	if !colorsEqual(port.At(b.Max.X-1, 0), red) {
		t.Fatalf("portrait red expected at top-right, got %v at (%d,0)", port.At(b.Max.X-1, 0), b.Max.X-1)
	}
}

func loGrid(hi [][]byte) [][]byte {
	lo := make([][]byte, len(hi))
	for y := range hi {
		lo[y] = make([]byte, len(hi[y]))
	}
	return lo
}

func colorsEqual(a, b color.Color) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return ar == br && ag == bg && ab == bb
}

func TestFetchDisplayPreview(t *testing.T) {
	bin := make([]byte, frameW*frameH*2)
	for i := 0; i < len(bin); i += 2 {
		bin[i] = 0x02
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bin)
	}))
	defer srv.Close()

	dir := t.TempDir()
	devices := NewDeviceRegistry(dir)
	mac := "AA:BB:CC:DD:EE:FF"
	devices.getOrCreateInkJoy(mac).Portrait = true

	h := &Hub{
		devices:        devices,
		displayPreview: NewDisplayPreviewStore(dir),
	}

	h.fetchDisplayPreview(mac, srv.URL+"/img.bin")

	dev, _ := devices.Get(inkjoyID(mac))
	if dev.DisplayPreviewAt.IsZero() {
		t.Fatal("expected display_preview_at set")
	}
	if dev.LastImageID != "" {
		t.Fatal("expected last_image_id cleared")
	}
	if _, err := os.Stat(filepath.Join(dir, "display", mac+".jpg")); err != nil {
		t.Fatalf("jpeg not saved: %v", err)
	}
}

func TestResizeBilinearHalvesCheckerboard(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	black := color.RGBA{0, 0, 0, 255}
	white := color.RGBA{255, 255, 255, 255}
	img.Set(0, 0, black)
	img.Set(1, 0, white)
	img.Set(0, 1, white)
	img.Set(1, 1, black)

	out := resizeBilinear(img, 1, 1)
	r, g, b, _ := out.At(0, 0).RGBA()
	if uint8(r>>8) != 127 && uint8(r>>8) != 128 {
		t.Fatalf("expected ~50%% gray, got rgb=%d,%d,%d", r>>8, g>>8, b>>8)
	}
	if uint8(g>>8) != uint8(r>>8) || uint8(b>>8) != uint8(r>>8) {
		t.Fatalf("expected neutral gray, got rgb=%d,%d,%d", r>>8, g>>8, b>>8)
	}
}

func TestBinToDisplayPreviewJPEGScale(t *testing.T) {
	jpegData, err := binToDisplayPreviewJPEG(makeSolidBin(0x02), false)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(jpegData))
	if err != nil {
		t.Fatal(err)
	}
	wantW := int(float64(frameW) * inkjoyDisplayPreviewScale)
	wantH := int(float64(frameH) * inkjoyDisplayPreviewScale)
	if img.Bounds().Dx() != wantW || img.Bounds().Dy() != wantH {
		t.Fatalf("landscape preview %dx%d want %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), wantW, wantH)
	}

	jpegDataP, err := binToDisplayPreviewJPEG(makeSolidBin(0x02), true)
	if err != nil {
		t.Fatal(err)
	}
	imgP, err := jpeg.Decode(bytes.NewReader(jpegDataP))
	if err != nil {
		t.Fatal(err)
	}
	wantPW := int(float64(frameH) * inkjoyDisplayPreviewScale)
	wantPH := int(float64(frameW) * inkjoyDisplayPreviewScale)
	if imgP.Bounds().Dx() != wantPW || imgP.Bounds().Dy() != wantPH {
		t.Fatalf("portrait preview %dx%d want %dx%d", imgP.Bounds().Dx(), imgP.Bounds().Dy(), wantPW, wantPH)
	}
}

func TestDisplayPreviewServeHTTP(t *testing.T) {
	dir := t.TempDir()
	store := NewDisplayPreviewStore(dir)
	mac := "11:22:33:44:55:66"
	jpeg, err := binToDisplayPreviewJPEG(makeSolidBin(0x02), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(mac, jpeg); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	store.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil), mac)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content-type=%q", ct)
	}
}

func makeSolidBin(hi byte) []byte {
	bin := make([]byte, frameW*frameH*2)
	for i := 0; i < len(bin); i += 2 {
		bin[i] = hi
	}
	return bin
}

func TestSetLastImageClearsDisplayPreview(t *testing.T) {
	dir := t.TempDir()
	devices := NewDeviceRegistry(dir)
	mac := "AA:BB:CC:DD:EE:FF"
	devices.SetDisplayPreview(mac)
	devices.SetLastImage(inkjoyID(mac), "img123", "")
	dev, _ := devices.Get(inkjoyID(mac))
	if dev.LastImageID != "img123" {
		t.Fatalf("last_image_id=%q", dev.LastImageID)
	}
	if !dev.DisplayPreviewAt.IsZero() {
		t.Fatal("expected display_preview_at cleared")
	}
}

func TestRestoreFromDisk(t *testing.T) {
	dir := t.TempDir()
	store := NewDisplayPreviewStore(dir)
	mac := "DE:AD:BE:EF:00:01"
	jpeg := []byte{0xff, 0xd8, 0xff, 0xd9}
	if err := store.Save(mac, jpeg); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	devices := NewDeviceRegistry(dir)
	store.RestoreFromDisk(devices)
	dev, _ := devices.Get(inkjoyID(mac))
	if dev.DisplayPreviewAt.IsZero() {
		t.Fatal("expected restored display_preview_at")
	}
}

func TestBuildPlayPayloadRoundtrip(t *testing.T) {
	payload, _ := buildPlayPayload("mac", "https://host:1443/foo/bar.bin")
	if _, ok := parsePlayBinURL(payload); !ok {
		t.Fatal("parse failed")
	}
	url, _ := parsePlayBinURL(payload)
	if url != "http://host:1443/foo/bar.bin" {
		t.Fatalf("url=%q", url)
	}
	_ = bytes.NewReader(payload)
}
