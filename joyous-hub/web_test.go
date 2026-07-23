package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"joyous-hub/inkjoybridge"
	"joyous-hub/protocol"
)

// buildTestHub creates a Hub wired to a temp dir, with a no-op publisher.
func buildTestHub(t *testing.T) *Hub {
	t.Helper()
	dir := t.TempDir()
	colorStore := NewColorStore(dir)
	images := NewImageStore(dir)
	images.SetColorStore(colorStore)
	samsung := NewSamsungStore(dir)
	samsung.SetColorStore(colorStore)
	return &Hub{
		devices:        NewDeviceRegistry(dir),
		samsungBattery: NewSamsungBatteryStore(dir),
		images:         images,
		displayPreview: NewDisplayPreviewStore(dir),
		inkjoy:         NewInkJoyCache(dir),
		samsung:        samsung,
		sendDelivery:   NewSendDeliveryTracker(),
		overlay:        NewOverlayStore(dir),
		color:          colorStore,
		scheduledSends: NewScheduledSendStore(dir),
		events:         NewEventBus(),
		publisher:      &noopPublisher{},
		mqttLog:        NewMQTTLogBuffer(20),
	}
}

// noopPublisher satisfies MQTTPublisher without a real broker.
type noopPublisher struct{ published []publishedMsg }

type publishedMsg struct{ topic string; payload []byte }

func (n *noopPublisher) Publish(topic string, payload []byte) error {
	n.published = append(n.published, publishedMsg{topic, payload})
	return nil
}

// TestGetDevicesEmpty: GET /api/devices with no devices returns empty JSON array.
func TestGetDevicesEmpty(t *testing.T) {
	h := buildTestHub(t)
	rec := httptest.NewRecorder()
	h.handleDevices(rec, httptest.NewRequest("GET", "/api/devices", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var out []any
	json.NewDecoder(rec.Body).Decode(&out)
	if out == nil {
		t.Error("expected empty array, got null")
	}
	if len(out) != 0 {
		t.Errorf("expected 0 devices, got %d", len(out))
	}
}

// TestGetDevicesWithData: registered devices appear in the response.
func TestGetDevicesWithData(t *testing.T) {
	h := buildTestHub(t)
	h.devices.MarkConnected("AABBCCDDEEFF")
	h.devices.UpdateHeart("AABBCCDDEEFF", inkjoybridge.HeartInfo{Battery: 77, Firmware: "0.5.6"})

	rec := httptest.NewRecorder()
	h.handleDevices(rec, httptest.NewRequest("GET", "/api/devices", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var out []map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if len(out) != 1 {
		t.Fatalf("expected 1 device, got %d", len(out))
	}
	if out[0]["mac"] != "AABBCCDDEEFF" {
		t.Errorf("mac: %v", out[0]["mac"])
	}
}

func TestSamsungFriendlyNameInDevices(t *testing.T) {
	h := buildTestHub(t)
	frameID := "192-168-1-108"
	h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	if err := h.samsung.SaveConfig(SamsungFrameConfig{
		FrameID:             frameID,
		Name:                "Kitchen Frame",
		PollIntervalMinutes: 60,
		CropFormat:          "16:9",
		DisplayWidth:        2560,
		DisplayHeight:       1440,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.handleDevices(rec, httptest.NewRequest("GET", "/api/devices", nil))
	var out []map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if len(out) != 1 {
		t.Fatalf("expected 1 device, got %d", len(out))
	}
	if out[0]["name"] != "Kitchen Frame" {
		t.Fatalf("name: got %v", out[0]["name"])
	}

	body := `{"name":"Kitchen Frame","poll_interval_minutes":30,"crop_format":"16:9","display_width":2560,"display_height":1440}`
	req := httptest.NewRequest("PUT", "/api/samsung/"+frameID+"/config", strings.NewReader(body))
	rec = httptest.NewRecorder()
	h.handleSamsungConfigPut(rec, req, frameID)
	if rec.Code != 200 {
		t.Fatalf("config put status %d: %s", rec.Code, rec.Body)
	}
	d, ok := h.devices.Get("samsung:192.168.1.108")
	if !ok || d.Name != "Kitchen Frame" {
		t.Fatalf("registry name: ok=%v name=%q", ok, d.Name)
	}
}

func TestSamsungListAPI(t *testing.T) {
	h := buildTestHub(t)
	frameID := "192-168-1-108"
	h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	h.devices.SetName("samsung:192.168.1.108", "Kitchen Frame")
	if err := h.samsung.SaveConfig(SamsungFrameConfig{
		FrameID:             frameID,
		Name:                "Kitchen Frame",
		PollIntervalMinutes: 60,
		CropFormat:          "16:9",
		DisplayWidth:        2560,
		DisplayHeight:       1440,
	}); err != nil {
		t.Fatal(err)
	}
	h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.109", Server: "Samsung MDC"})
	h.devices.TouchSamsung("192.168.1.108", "mdc_session")

	rec := httptest.NewRecorder()
	h.handleSamsungList(rec, httptest.NewRequest("GET", "/api/samsung", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out []map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if len(out) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(out))
	}
	byID := map[string]map[string]any{}
	for _, f := range out {
		byID[f["id"].(string)] = f
	}
	kitchen := byID[frameID]
	if kitchen["name"] != "Kitchen Frame" {
		t.Fatalf("name: got %v", kitchen["name"])
	}
	if kitchen["device_id"] != "samsung:192.168.1.108" {
		t.Fatalf("device_id: got %v", kitchen["device_id"])
	}
	if kitchen["connected"] != true {
		t.Fatalf("connected: got %v", kitchen["connected"])
	}
	other := byID["192-168-1-109"]
	if other["device_id"] != "samsung:192.168.1.109" {
		t.Fatalf("discovered-only device_id: got %v", other["device_id"])
	}
	if other["connected"] == true {
		t.Fatalf("discover alone should not mark connected: got %v", other["connected"])
	}
}

// TestGetImagesEmpty: GET /api/images with no images returns empty array.
func TestGetImagesEmpty(t *testing.T) {
	h := buildTestHub(t)
	rec := httptest.NewRecorder()
	h.handleImages(rec, httptest.NewRequest("GET", "/api/images", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var out []any
	json.NewDecoder(rec.Body).Decode(&out)
	if out == nil {
		t.Error("expected empty array, got null")
	}
}

// TestUploadImageBin: POST /api/images with any file returns 200 + JSON with id.
func TestUploadImageBin(t *testing.T) {
	h := buildTestHub(t)

	// A small arbitrary payload — upload just stores bytes as-is.
	data := []byte{0x01, 0x02, 0x03, 0x04}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, _ := w.CreateFormFile("file", "test.bin")
	fw.Write(data)
	w.Close()

	req := httptest.NewRequest("POST", "/api/images", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())

	rec := httptest.NewRecorder()
	h.handleImageUpload(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if out["id"] == "" {
		t.Error("expected id in response")
	}
}

// TestDisplayAction: POST /api/devices/{mac}/display publishes a play message.
func TestDisplayAction(t *testing.T) {
	h := buildTestHub(t)
	pub := h.publisher.(*noopPublisher)

	// Store a full-frame bin so ServeBin works when display is triggered.
	bin := make([]byte, frameW*frameH*2)
	for i := 0; i < len(bin); i += 2 {
		bin[i] = 0x02
	}
	id, _ := h.images.Store(bytes.NewReader(bin), "x.bin")
	h.devices.MarkConnected("AABBCCDDEEFF")

	body := fmt.Sprintf(`{"image_id":%q}`, id)
	req := httptest.NewRequest("POST", "/api/devices/AABBCCDDEEFF/display",
		strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleDisplay(rec, req, "AABBCCDDEEFF")

	if rec.Code != 202 {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if len(pub.published) == 0 {
		t.Fatal("expected a publish, got none")
	}
	var msg map[string]any
	json.Unmarshal(pub.published[0].payload, &msg)
	if msg["action"] != "play" {
		t.Errorf("action: got %v", msg["action"])
	}
	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if out["send_id"] == "" {
		t.Error("expected send_id in response")
	}
}

// TestRefreshAction: POST /api/devices/{mac}/refresh publishes image_refresh.
func TestRefreshAction(t *testing.T) {
	h := buildTestHub(t)
	pub := h.publisher.(*noopPublisher)
	h.devices.MarkConnected("AABBCCDDEEFF")

	req := httptest.NewRequest("POST", "/api/devices/AABBCCDDEEFF/refresh", nil)
	rec := httptest.NewRecorder()
	h.handleRefresh(rec, req, "AABBCCDDEEFF")

	if rec.Code != 202 {
		t.Fatalf("status %d", rec.Code)
	}
	if len(pub.published) == 0 {
		t.Fatal("expected a publish")
	}
	var msg map[string]any
	json.Unmarshal(pub.published[0].payload, &msg)
	if msg["action"] != "image_refresh" {
		t.Errorf("action: got %v", msg["action"])
	}
}

// TestUIRevisionAPI: GET /api/ui/revision returns the embedded UI hash.
func TestUIRevisionAPI(t *testing.T) {
	h := buildTestHub(t)
	rec := httptest.NewRecorder()
	h.handleUIRevision(rec, httptest.NewRequest("GET", "/api/ui/revision", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control: got %q", got)
	}
	var out struct {
		Revision string `json:"revision"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Revision != uiRevision || len(out.Revision) != 12 {
		t.Fatalf("revision: got %q want %q", out.Revision, uiRevision)
	}
}

// TestStaticRoot: GET / returns 200 with HTML content-type.
func TestStaticRoot(t *testing.T) {
	h := buildTestHub(t)
	rec := httptest.NewRecorder()
	h.handleStatic(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: got %q want text/html", ct)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control: got %q want no-cache", got)
	}
	body, _ := io.ReadAll(rec.Body)
	html := string(body)
	if !strings.Contains(html, "<html") {
		t.Error("body should contain <html>")
	}
	if strings.Contains(html, uiRevisionPlaceholder) {
		t.Error("served HTML should inject ui revision")
	}
	if !strings.Contains(html, "const JOYOUS_UI_REVISION='"+uiRevision+"'") {
		t.Error("served HTML should embed ui revision")
	}
}

// TestWebBranding: embedded UI uses Joyous branding, not legacy InkJoy Hub strings.
func TestWebBranding(t *testing.T) {
	if !strings.Contains(indexHTML, "<title>Joyous</title>") {
		t.Fatal("indexHTML title should be Joyous")
	}
	if !strings.Contains(indexHTML, "<h1>Joyous</h1>") {
		t.Fatal("indexHTML header should be Joyous")
	}
	for _, legacy := range []string{"InkJoy Hub", "InkJoy hub", "inkjoy hub"} {
		if strings.Contains(indexHTML, legacy) {
			t.Fatalf("indexHTML still contains legacy branding %q", legacy)
		}
	}
	if strings.Contains(indexHTML, "/thumb?t=") {
		t.Fatal("indexHTML should not cache-bust album thumbs with ?t=")
	}
	if strings.Contains(indexHTML, ".png?t=") {
		t.Fatal("indexHTML should not cache-bust Samsung preview PNGs with ?t=")
	}
	if !strings.Contains(indexHTML, "samsungPreviewURL") {
		t.Fatal("indexHTML should define samsungPreviewURL for etag/album cache busting")
	}
}

// TestDeleteCropAPI: DELETE /api/images/{id}/crop?format=4:3 removes the stored crop.
func TestDeleteCropAPI(t *testing.T) {
	h := buildTestHub(t)
	id, _ := h.images.Store(bytes.NewReader([]byte{1, 2, 3}), "test.jpg")
	h.images.SetCrop(id, "4:3", CropRect{X: 0.1, Y: 0.1, W: 0.8, H: 0.8})

	req := httptest.NewRequest("DELETE", "/api/images/"+id+"/crop?format=4:3", nil)
	rec := httptest.NewRecorder()
	h.handleDeleteCrop(rec, req, id)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	crops, _ := h.images.GetCrops(id)
	if _, ok := crops["4:3"]; ok {
		t.Error("crop should be deleted")
	}
}

// TestSaveCropAPI: POST /api/images/{id}/crop stores the crop and returns 200.
func TestSaveCropAPI(t *testing.T) {
	h := buildTestHub(t)
	id, _ := h.images.Store(bytes.NewReader([]byte{1, 2, 3}), "test.jpg")

	body := `{"format":"4:3","x":0.1,"y":0.05,"w":0.8,"h":0.6}`
	req := httptest.NewRequest("POST", "/api/images/"+id+"/crop", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleSaveCrop(rec, req, id)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	crops, _ := h.images.GetCrops(id)
	if _, ok := crops["4:3"]; !ok {
		t.Error("crop not found after save")
	}
}

// TestUploadFullBinDragDrop: POST /api/images with a full 1600×1200 .bin file (no dimension
// headers) succeeds — this is the real drag-and-drop path from the browser.
func TestUploadFullBinDragDrop(t *testing.T) {
	h := buildTestHub(t)

	bin := make([]byte, frameW*frameH*2)
	for i := 0; i < len(bin); i += 2 {
		bin[i] = 0x01 // all black
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "photo.bin")
	fw.Write(bin)
	mw.Close()

	req := httptest.NewRequest("POST", "/api/images", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// No X-Frame-Width / X-Frame-Height — simulates browser drag-and-drop.

	rec := httptest.NewRecorder()
	h.handleImageUpload(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if out["id"] == "" || out["id"] == nil {
		t.Error("expected non-empty id in response")
	}
}

// TestRenameImageAPI: PATCH /api/images/{id} updates the display name.
func TestRenameImageAPI(t *testing.T) {
	h := buildTestHub(t)
	id, _ := h.images.Store(bytes.NewReader([]byte{0x01, 0x02}), "orig.jpg")

	body := `{"name":"Beach day"}`
	req := httptest.NewRequest("PATCH", "/api/images/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleImagePatch(rec, req, id)
	if rec.Code != 200 {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var meta ImageMeta
	if err := json.NewDecoder(rec.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if meta.Name != "Beach day" {
		t.Fatalf("got %q", meta.Name)
	}
	got, err := h.images.readMeta(id)
	if err != nil || got.Name != "Beach day" {
		t.Fatalf("meta=%+v err=%v", got, err)
	}
}

func TestChromaBoostAPI(t *testing.T) {
	h := buildTestHub(t)
	id, _ := h.images.Store(bytes.NewReader([]byte{0x01, 0x02}), "landscape.jpg")

	body := `{"chroma_boost_mode":"on"}`
	req := httptest.NewRequest("PATCH", "/api/images/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleImagePatch(rec, req, id)
	if rec.Code != 200 {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var meta ImageMeta
	if err := json.NewDecoder(rec.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if meta.ChromaBoost == nil || !*meta.ChromaBoost {
		t.Fatalf("expected chroma on, got %+v", meta.ChromaBoost)
	}

	rec2 := httptest.NewRecorder()
	h.handleImageGet(rec2, httptest.NewRequest("GET", "/api/images/"+id, nil), id)
	if rec2.Code != 200 {
		t.Fatalf("get status %d", rec2.Code)
	}
}

// TestDeleteImage: DELETE /api/images/{id} removes the image.
func TestDeleteImage(t *testing.T) {
	h := buildTestHub(t)
	id, _ := h.images.Store(bytes.NewReader([]byte{0x01, 0x02}), "del.bin")

	req := httptest.NewRequest("DELETE", "/api/images/"+id, nil)
	rec := httptest.NewRecorder()
	h.handleImageDelete(rec, req, id)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}

	// Confirm it's gone.
	_, err := h.images.ServeBin(id)
	if err == nil {
		t.Error("image should be deleted")
	}
}

func TestMQTTLogsAPI(t *testing.T) {
	h := buildTestHub(t)
	h.joyousMQTTLog = NewMQTTLogBuffer(20)
	payload, _ := protocol.NewEnvelope(protocol.TypeDevices, "inkjoy", protocol.DevicesPayload{})
	h.joyousMQTTLog.AddJoyousBridgeToHub("joyous/bridge/inkjoy/devices", payload)
	cmdPayload, _ := protocol.NewEnvelope(protocol.TypeCmd, "inkjoy", protocol.CmdPayload{Cmd: protocol.CmdRefresh})
	h.joyousMQTTLog.AddJoyousHubToBridge("joyous/hub/inkjoy/cmd", cmdPayload)

	rec := httptest.NewRecorder()
	h.handleMQTTLogs(rec, httptest.NewRequest("GET", "/api/mqtt/logs", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var out struct {
		Local    []MQTTLogEntry `json:"local"`
		Upstream []MQTTLogEntry `json:"upstream"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Local) != 1 || out.Local[0].Action != protocol.TypeDevices {
		t.Fatalf("local=%+v", out.Local)
	}
	if len(out.Upstream) != 1 || out.Upstream[0].Action != protocol.TypeCmd+" · "+protocol.CmdRefresh {
		t.Fatalf("upstream=%+v", out.Upstream)
	}
}
