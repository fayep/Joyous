package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func samsungFrameHTTPRequest(method, url, frameIP string) *http.Request {
	req := httptest.NewRequest(method, url, nil)
	req.RemoteAddr = net.JoinHostPort(frameIP, "54321")
	return req
}

func testPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 40), uint8(y * 40), 128, 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func TestSamsungLockfileBlocksStatus(t *testing.T) {
	dir := t.TempDir()
	s := NewSamsungStore(dir)
	frameID := "living-room"

	if err := s.writePNGLocked(frameID, testPNG()); err != nil {
		t.Fatal(err)
	}
	if s.IsLocked(frameID) {
		t.Fatal("should not be locked after write")
	}

	lock := s.lockPath(frameID)
	if err := os.WriteFile(lock, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	if !s.IsLocked(frameID) {
		t.Fatal("expected locked")
	}

	h := &Hub{samsung: s}
	rec := httptest.NewRecorder()
	h.handleSamsungStatus(rec, httptest.NewRequest("GET", "/samsung/living-room/status", nil), frameID)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var resp samsungStatusResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Locked {
		t.Error("expected locked true in status")
	}
	if !resp.HasImage {
		t.Error("expected has_image true")
	}
}

func TestSamsungETagChangesOnReplace(t *testing.T) {
	dir := t.TempDir()
	s := NewSamsungStore(dir)
	frameID := "test"

	if err := s.writePNGLocked(frameID, testPNG()); err != nil {
		t.Fatal(err)
	}
	etag1, _, _ := s.PNGInfo(frameID)

	time.Sleep(10 * time.Millisecond)
	if err := s.writePNGLocked(frameID, testPNG()); err != nil {
		t.Fatal(err)
	}
	etag2, _, _ := s.PNGInfo(frameID)
	if etag1 == etag2 {
		t.Errorf("etag should change after replace: %s == %s", etag1, etag2)
	}
}

func TestSamsungPNGIfNoneMatch(t *testing.T) {
	dir := t.TempDir()
	s := NewSamsungStore(dir)
	frameID := "test"
	s.writePNGLocked(frameID, testPNG())

	h := &Hub{samsung: s}
	req := httptest.NewRequest("GET", "/samsung/test.png", nil)
	rec := httptest.NewRecorder()
	h.handleSamsungPNG(rec, req, frameID)
	if rec.Code != 200 {
		t.Fatalf("first fetch %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")

	req2 := httptest.NewRequest("GET", "/samsung/test.png", nil)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.handleSamsungPNG(rec2, req2, frameID)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", rec2.Code)
	}
}

func TestSamsungPNGBlockedWhenLocked(t *testing.T) {
	dir := t.TempDir()
	s := NewSamsungStore(dir)
	frameID := "test"
	s.writePNGLocked(frameID, testPNG())
	os.WriteFile(s.lockPath(frameID), []byte{}, 0644)

	h := &Hub{samsung: s}
	rec := httptest.NewRecorder()
	h.handleSamsungPNG(rec, httptest.NewRequest("GET", "/samsung/test.png", nil), frameID)
	if rec.Code != http.StatusLocked {
		t.Fatalf("expected 423, got %d", rec.Code)
	}
}

func TestInInactiveWindow(t *testing.T) {
	loc := time.Local
	cases := []struct {
		t      time.Time
		begin  string
		end    string
		inside bool
	}{
		{time.Date(2024, 6, 15, 23, 0, 0, 0, loc), "22:00", "07:00", true},
		{time.Date(2024, 6, 15, 6, 0, 0, 0, loc), "22:00", "07:00", true},
		{time.Date(2024, 6, 15, 12, 0, 0, 0, loc), "22:00", "07:00", false},
		{time.Date(2024, 6, 15, 10, 0, 0, 0, loc), "09:00", "17:00", true},
		{time.Date(2024, 6, 15, 18, 0, 0, 0, loc), "09:00", "17:00", false},
		{time.Date(2024, 6, 15, 12, 0, 0, 0, loc), "", "07:00", false},
	}
	for _, c := range cases {
		got := InInactiveWindow(c.t, c.begin, c.end)
		if got != c.inside {
			t.Errorf("InInactiveWindow(%v, %s, %s) = %v, want %v", c.t.Format("15:04"), c.begin, c.end, got, c.inside)
		}
	}
}

func TestNextWakeTime(t *testing.T) {
	loc := time.Local
	now := time.Date(2024, 6, 15, 21, 30, 0, 0, loc)
	next := NextWakeTime(now, 60, "22:00", "07:00")
	// 21:30 + 60m = 22:30 which is inactive → wake at 07:00 next day
	want := time.Date(2024, 6, 16, 7, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}

	active := time.Date(2024, 6, 15, 10, 0, 0, 0, loc)
	next2 := NextWakeTime(active, 60, "22:00", "07:00")
	want2 := active.Add(time.Hour)
	if !next2.Equal(want2) {
		t.Errorf("next = %v, want %v", next2, want2)
	}
}

func TestSSSPConfigSize(t *testing.T) {
	dir := t.TempDir()
	s := NewSamsungStore(dir)
	if err := s.ensureDir(); err != nil {
		t.Fatal(err)
	}
	wgtData := []byte("fake-wgt-content-for-test")
	if err := os.WriteFile(s.wgtPath(), wgtData, 0644); err != nil {
		t.Fatal(err)
	}

	h := &Hub{samsung: s, serverAddr: "localhost:8080"}
	rec := httptest.NewRecorder()
	h.handleSamsungSSSPConfig(rec, httptest.NewRequest("GET", "/samsung/sssp_config.xml", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<size>") {
		t.Fatal("missing size tag")
	}
	if !strings.Contains(body, string(rune(len(wgtData)))) && !strings.Contains(body, "25") {
		// size should be 25 bytes
		if !strings.Contains(body, ">25<") {
			t.Errorf("expected size 25 in xml: %s", body)
		}
	}
}

func TestSamsungListFrames(t *testing.T) {
	dir := t.TempDir()
	s := NewSamsungStore(dir)
	s.SaveConfig(SamsungFrameConfig{FrameID: "a", PollIntervalMinutes: 30})
	s.writePNGLocked("b", testPNG())

	frames, err := s.ListFrames()
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames, got %v", frames)
	}
}

func TestSamsungUploadAPI(t *testing.T) {
	h := buildTestHub(t)
	s := h.samsung
	raw := testPNG()
	if err := s.StoreUpload("kitchen", raw, defaultSamsungDisplayProfile()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.pngPath("kitchen")); err != nil {
		t.Fatal("png not written")
	}
}

func TestConvertToSamsungPNG(t *testing.T) {
	raw := testPNG()
	out, err := convertToSamsungPNG(raw, defaultSamsungDisplayProfile(), CropRect{}, false)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != samsungW || b.Dy() != samsungH {
		t.Errorf("size %dx%d, want %dx%d", b.Dx(), b.Dy(), samsungW, samsungH)
	}
}

func TestSamsungFrameSeenFromContentJSON(t *testing.T) {
	h := buildTestHub(t)
	frameID := "192-168-1-108"
	h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	h.devices.mu.Lock()
	h.devices.m["samsung:192.168.1.108"].LastSeen = time.Now().Add(-time.Hour)
	h.devices.mu.Unlock()
	if err := h.samsung.writePNGLocked(frameID, testPNG()); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.handleSamsungContentJSON(rec, samsungFrameHTTPRequest("GET", "/samsung/"+frameID+"/content.json", "192.168.1.108"), frameID)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	d, ok := h.devices.Get("samsung:192.168.1.108")
	if !ok || d.LastAction != "content.json" {
		t.Fatalf("last action: ok=%v action=%q", ok, d.LastAction)
	}
	ApplySamsungConnected(d)
	if !d.Connected {
		t.Fatal("frame should be active after content.json fetch")
	}
}

func TestSamsungFrameSeenFromPNG304(t *testing.T) {
	h := buildTestHub(t)
	frameID := "192-168-1-108"
	h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	h.devices.mu.Lock()
	h.devices.m["samsung:192.168.1.108"].LastSeen = time.Now().Add(-time.Hour)
	h.devices.mu.Unlock()
	if err := h.samsung.writePNGLocked(frameID, testPNG()); err != nil {
		t.Fatal(err)
	}

	req := samsungFrameHTTPRequest("GET", "/samsung/"+frameID+".png", "192.168.1.108")
	rec := httptest.NewRecorder()
	h.handleSamsungPNG(rec, req, frameID)
	if rec.Code != 200 {
		t.Fatalf("first fetch %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")

	req2 := samsungFrameHTTPRequest("GET", "/samsung/"+frameID+".png", "192.168.1.108")
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.handleSamsungPNG(rec2, req2, frameID)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", rec2.Code)
	}
	d, ok := h.devices.Get("samsung:192.168.1.108")
	if !ok || d.LastAction != "png" {
		t.Fatalf("last action: ok=%v action=%q", ok, d.LastAction)
	}
	ApplySamsungConnected(d)
	if !d.Connected {
		t.Fatal("304 revalidation should count as frame contact")
	}
}

func TestSamsungHubPreviewDoesNotMarkActive(t *testing.T) {
	h := buildTestHub(t)
	frameID := "192-168-1-108"
	h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	if err := h.samsung.writePNGLocked(frameID, testPNG()); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/samsung/"+frameID+".png", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	h.handleSamsungPNG(rec, req, frameID)
	if rec.Code != 200 {
		t.Fatalf("preview fetch %d", rec.Code)
	}
	d, ok := h.devices.Get("samsung:192.168.1.108")
	if !ok {
		t.Fatal("device missing")
	}
	if d.LastAction == "png" {
		t.Fatal("hub preview should not update frame last action")
	}
	ApplySamsungConnected(d)
	if d.Connected {
		t.Fatal("hub preview should not mark frame active")
	}
}
