package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// MQTTPublisher is satisfied by the real broker and by test doubles.
type MQTTPublisher interface {
	Publish(topic string, payload []byte) error
}

// Hub wires together the broker, bridges, device registry, image store, and HTTP server.
type Hub struct {
	devices    *DeviceRegistry
	images     *ImageStore
	samsung    *SamsungStore
	hubIP      string // resolved non-loopback LAN IP, used for play URLs and BLE adoption
	publisher  MQTTPublisher
	serverAddr string // e.g. "192.168.1.5:8080" — used in play URLs
	mqttPort   int    // MQTT broker port the frame should connect to (e.g. 11883)
}

// handleDevices serves GET /api/devices.
func (h *Hub) handleDevices(w http.ResponseWriter, r *http.Request) {
	devs := h.devices.List()
	if devs == nil {
		devs = []Device{}
	}
	// Convert stored UTC sleep times → server local time for display.
	for i := range devs {
		devs[i].SleepBeginTime = utcHHMMToLocal(devs[i].SleepBeginTime)
		devs[i].SleepEndTime = utcHHMMToLocal(devs[i].SleepEndTime)
	}
	h.applySamsungFriendlyNames(devs)
	for i := range devs {
		ApplySamsungConnected(&devs[i])
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(devs)
}

// utcHHMMToLocal converts a "HH:MM" string from UTC to the server's local timezone.
// Returns the input unchanged on any parse error or if it's empty/00:00.
func utcHHMMToLocal(hhmm string) string {
	if hhmm == "" || hhmm == "00:00" {
		return hhmm
	}
	now := time.Now()
	t, err := time.ParseInLocation("2006-01-02 15:04",
		now.UTC().Format("2006-01-02")+" "+hhmm, time.UTC)
	if err != nil {
		return hhmm
	}
	return t.In(time.Local).Format("15:04")
}

// localHHMMToUTC converts a "HH:MM" string from the server's local timezone to UTC.
func localHHMMToUTC(hhmm string) string {
	if hhmm == "" || hhmm == "00:00" {
		return hhmm
	}
	now := time.Now()
	t, err := time.ParseInLocation("2006-01-02 15:04",
		now.Format("2006-01-02")+" "+hhmm, time.Local)
	if err != nil {
		return hhmm
	}
	return t.UTC().Format("15:04")
}

// handleBLEScan serves POST /api/inkjoy/ble/scan — scans for IJ_ BLE frames.
func (h *Hub) handleBLEScan(w http.ResponseWriter, r *http.Request) {
	frames, err := ScanBLEFrames(8 * time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(frames)
}

// handleBLEAdopt serves POST /api/inkjoy/ble/adopt — provisions a frame via BluFi.
func (h *Hub) handleBLEAdopt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Address string `json:"address"`
		SSID    string `json:"ssid"`
		WifiPwd string `json:"wifi_pwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Address == "" || body.SSID == "" {
		http.Error(w, "address, ssid and wifi_pwd required", http.StatusBadRequest)
		return
	}

	// Derive MQTT host from hub's server address (same host, different port).
	mqttHost := h.hubIP
	if mqttHost == "" {
		mqttHost, _, _ = net.SplitHostPort(h.serverAddr)
	}
	if mqttHost == "" {
		mqttHost = h.serverAddr
	}

	if err := AdoptBLEFrame(body.Address, body.SSID, body.WifiPwd, mqttHost, h.mqttPort, "inkjoy", "inkjoy"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleSleep serves POST /api/devices/{id}/sleep — sends wifi_sleep to an InkJoy frame.
func (h *Hub) handleSleep(w http.ResponseWriter, r *http.Request, id string) {
	dev, ok := h.devices.Get(id)
	if !ok || dev.Type != DeviceTypeInkJoy {
		http.Error(w, "inkjoy device required", http.StatusBadRequest)
		return
	}
	var body struct {
		BeginTime string `json:"beginTime"`
		EndTime   string `json:"endTime"`
		Mode      int    `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Mode == 0 {
		body.Mode = 2
	}
	// Convert local→UTC for the frame; store UTC in registry.
	utcBegin := localHHMMToUTC(body.BeginTime)
	utcEnd := localHHMMToUTC(body.EndTime)
	payload := buildActionPayloadFor(dev.MAC, "wifi_sleep", map[string]any{
		"beginTime": utcBegin,
		"endTime":   utcEnd,
		"mode":      body.Mode,
	})
	if err := h.publisher.Publish("/inkjoyap/"+dev.MAC, payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.devices.UpdateSleep(dev.MAC, utcBegin, utcEnd)
	h.devices.Save()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleDevicePatch serves PATCH /api/devices/{id} — updates mutable fields (name, portrait).
func (h *Hub) handleDevicePatch(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Name     *string `json:"name"`
		Portrait *bool   `json:"portrait"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	found := true
	if body.Name != nil {
		found = h.devices.SetName(id, *body.Name)
	}
	if body.Portrait != nil {
		found = h.devices.SetPortrait(id, *body.Portrait)
	}
	if !found {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	h.devices.Save()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleDeviceDelete serves DELETE /api/devices/{id}.
func (h *Hub) handleDeviceDelete(w http.ResponseWriter, r *http.Request, id string) {
	if !h.devices.Delete(id) {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	h.devices.Save()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleImages serves GET /api/images.
func (h *Hub) handleImages(w http.ResponseWriter, r *http.Request) {
	imgs, _ := h.images.ListImages()
	if imgs == nil {
		imgs = []ImageMeta{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(imgs)
}

// handleImageUpload serves POST /api/images.
func (h *Hub) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "parse form: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file field required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	name := header.Filename
	id, err := h.images.Store(file, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": id, "name": name})
}

// handleImageDelete serves DELETE /api/images/{id}.
func (h *Hub) handleImageDelete(w http.ResponseWriter, r *http.Request, id string) {
	h.images.DeleteImage(id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleDisplay serves POST /api/devices/{id}/display.
func (h *Hub) handleDisplay(w http.ResponseWriter, r *http.Request, deviceID string) {
	var body struct {
		ImageID string `json:"image_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ImageID == "" {
		http.Error(w, "image_id required", http.StatusBadRequest)
		return
	}
	if err := h.SendImageToDevice(deviceID, body.ImageID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleRefresh serves POST /api/devices/{id}/refresh (InkJoy only).
func (h *Hub) handleRefresh(w http.ResponseWriter, r *http.Request, deviceID string) {
	dev, ok := h.devices.Get(deviceID)
	if !ok || dev.Type != DeviceTypeInkJoy {
		http.Error(w, "inkjoy device required", http.StatusBadRequest)
		return
	}
	payload := buildActionPayloadFor(dev.MAC, "image_refresh", nil)
	if err := h.publisher.Publish("/inkjoyap/"+dev.MAC, payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleSaveCrop serves POST /api/images/{id}/crop.
func (h *Hub) handleSaveCrop(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Format string  `json:"format"`
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		W      float64 `json:"w"`
		H      float64 `json:"h"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Format == "" {
		http.Error(w, "format, x, y, w, h required", http.StatusBadRequest)
		return
	}
	if body.W <= 0 || body.H <= 0 {
		http.Error(w, "w and h must be > 0", http.StatusBadRequest)
		return
	}
	rect := CropRect{X: body.X, Y: body.Y, W: body.W, H: body.H}
	if err := h.images.SetCrop(id, body.Format, rect); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleDeleteCrop serves DELETE /api/images/{id}/crop?format=...
func (h *Hub) handleDeleteCrop(w http.ResponseWriter, r *http.Request, id string) {
	format := r.URL.Query().Get("format")
	if format == "" {
		http.Error(w, "format query param required", http.StatusBadRequest)
		return
	}
	if err := h.images.DeleteCrop(id, format); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleStatic serves the embedded SPA for any non-API route.
func (h *Hub) handleStatic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(indexHTML))
}

// ── MQTT payload helpers ─────────────────────────────────────────────────────

func buildActionPayloadFor(mac, action string, data map[string]any) []byte {
	msg := map[string]any{
		"action": action,
		"msgid":  fmt.Sprintf("%d", time.Now().UnixMilli()),
		"stamac": mac,
	}
	if data != nil {
		msg["data"] = data
	}
	b, _ := json.Marshal(msg)
	return b
}

// buildPlayPayload returns the MQTT payload and the msgid it embedded.
// Callers should register the msgid via registerInjectedPlay so the
// corresponding play_ack can be suppressed from upstream forwarding.
func buildPlayPayload(mac, imgURL string) ([]byte, string) {
	// imgURL is e.g. "https://192.168.1.7:1443/images/abc.bin"
	// Strip scheme (http:// or https://)
	rest := imgURL
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	host, portStr, path := "", "8080", ""
	if slash := strings.Index(rest, "/"); slash >= 0 {
		path = rest[slash:]
		rest = rest[:slash]
	}
	if h, p, err := net.SplitHostPort(rest); err == nil {
		host, portStr = h, p
	} else {
		host = rest
	}
	port := 8080
	fmt.Sscanf(portStr, "%d", &port)
	msgid := fmt.Sprintf("%d", time.Now().UnixMilli())
	b, _ := json.Marshal(map[string]any{
		"action": "play",
		"msgid":  msgid,
		"stamac": mac,
		"data": map[string]any{
			"host":     host,
			"port":     port,
			"imgs":     []any{map[string]any{"imgid": "local-0", "imgurl": path}},
			"mode":     2,
			"strategy": 1,
		},
	})
	return b, msgid
}

// ── Embedded static HTML ─────────────────────────────────────────────────────

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Joyous</title>
<style>
  body{font-family:system-ui,sans-serif;margin:0;padding:0;background:#f5f5f5}
  header{background:#1a1a2e;color:#fff;padding:1rem 2rem;display:flex;align-items:center;gap:1rem}
  header h1{margin:0;font-size:1.2rem}
  nav button{background:none;border:none;color:#aaa;font-size:1rem;cursor:pointer;padding:.5rem 1rem}
  nav button.active{color:#fff;border-bottom:2px solid #fff}
  .info-grid{display:grid;grid-template-columns:max-content 1fr;gap:.25rem .75rem;font-size:.9rem}
  .info-grid .label{color:#888}
  .frame-list-item{display:flex;align-items:center;gap:.6rem;padding:.5rem .75rem;border-radius:6px;cursor:pointer;margin-bottom:.3rem}
  .frame-list-item:hover{background:#f0f0ff}
  .frame-list-item.selected{background:#e8eaf6}
  .dot{width:8px;height:8px;border-radius:50%;flex-shrink:0}
  .dot.online{background:#28a745}.dot.offline{background:#dc3545}
  .last-image-preview{max-width:100%;border-radius:6px;display:block;margin:.5rem 0}
  .section-label{font-size:.8rem;text-transform:uppercase;letter-spacing:.05em;color:#888;margin:1rem 0 .4rem}
  main{padding:2rem;max-width:1200px;margin:auto}
  .card{background:#fff;border-radius:8px;padding:1rem 1.5rem;margin-bottom:1rem;box-shadow:0 1px 3px #0002}
  .device-mac{font-family:monospace;font-weight:bold}
  .badge{display:inline-block;padding:2px 8px;border-radius:12px;font-size:.75rem}
  .badge.online{background:#d4edda;color:#155724}
  .badge.offline{background:#f8d7da;color:#721c24}
  .btn{padding:.4rem .9rem;border:none;border-radius:4px;cursor:pointer;font-size:.9rem}
  .btn-primary{background:#1a1a2e;color:#fff}
  .btn-sm{padding:.25rem .6rem;font-size:.8rem}
  .img-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:1rem}
  .img-card{background:#fff;border-radius:8px;overflow:hidden;box-shadow:0 1px 3px #0002;text-align:center}
  .img-card img{width:100%;aspect-ratio:4/3;object-fit:contain;display:block;background:#eee}
  .img-card .card-body{padding:.5rem .75rem}
  .img-card .name{font-size:.8rem;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;margin-bottom:.4rem}
  /* crop modal */
  #crop-modal{display:none;position:fixed;inset:0;background:#000c;z-index:1000;align-items:center;justify-content:center;touch-action:none;overscroll-behavior:none}
  #crop-modal.open{display:flex;overflow:hidden}
  #crop-ui{background:#1a1a2e;border-radius:10px;padding:1rem;display:flex;flex-direction:column;gap:.6rem;max-width:96vw;max-height:96vh;touch-action:none;overflow:hidden}
  #crop-toolbar{display:flex;gap:.5rem;align-items:center;flex-wrap:wrap}
  #crop-toolbar select{padding:.3rem .5rem;border-radius:4px;border:1px solid #555;background:#2a2a4e;color:#fff;font-size:.85rem}
  #crop-toolbar label{color:#aaa;font-size:.8rem}
  #crop-stage{position:relative;flex:1;overflow:hidden;background:#111;border-radius:6px;min-width:min(80vw,600px);min-height:min(60vh,400px);touch-action:none;overscroll-behavior:none}
  #crop-img{position:absolute;display:block;user-select:none;-webkit-user-drag:none;pointer-events:none;touch-action:none}
  #crop-box{position:absolute;box-sizing:border-box;border:2px solid #fff;box-shadow:0 0 0 9999px rgba(0,0,0,.5);cursor:move;touch-action:none;user-select:none;-webkit-user-select:none;z-index:2}
  .ch{position:absolute;width:14px;height:14px;background:#fff;border:2px solid #333;border-radius:2px;touch-action:none;user-select:none;-webkit-user-select:none}
  .ch[data-h=tl]{top:-7px;left:-7px;cursor:nwse-resize}
  .ch[data-h=tr]{top:-7px;right:-7px;cursor:nesw-resize}
  .ch[data-h=bl]{bottom:-7px;left:-7px;cursor:nesw-resize}
  .ch[data-h=br]{bottom:-7px;right:-7px;cursor:nwse-resize}
  @media (pointer: coarse){
    .ch{width:28px;height:28px}
    .ch[data-h=tl]{top:-14px;left:-14px}
    .ch[data-h=tr]{top:-14px;right:-14px}
    .ch[data-h=bl]{bottom:-14px;left:-14px}
    .ch[data-h=br]{bottom:-14px;right:-14px}
  }
  #crop-hint{color:#888;font-size:.75rem;text-align:right}
  .crop-dim{position:absolute;box-sizing:border-box;pointer-events:none;border:1.5px dashed rgba(255,255,255,.38)}
  .crop-dim-label{position:absolute;top:3px;left:5px;font:10px/1 sans-serif;color:rgba(255,255,255,.5);pointer-events:none}
  #upload-zone{border:2px dashed #ccc;border-radius:8px;padding:2rem;text-align:center;margin-bottom:1rem;cursor:pointer}
  #upload-zone.drag{border-color:#1a1a2e;background:#f0f0ff}
  input[type=file]{display:none}
  #send-picker{display:none;position:fixed;z-index:1000;background:#fff;border:1px solid #ccc;border-radius:6px;box-shadow:0 4px 12px #0002;min-width:160px;max-height:min(50vh,320px);overflow-y:auto;-webkit-overflow-scrolling:touch}
  .send-picker-item{padding:.5rem .85rem;cursor:pointer;font-size:.9rem;white-space:nowrap}
  .send-picker-item:hover{background:#f0f0ff}
</style>
</head>
<body>
<header>
  <h1>Joyous</h1>
  <nav>
    <button class="active" onclick="showTab('devices',this)">Devices</button>
    <button onclick="showTab('album',this)">Album</button>
    <button onclick="showTab('inkjoy',this)">InkJoy</button>
    <button onclick="showTab('samsung',this)">Samsung</button>
  </nav>
</header>
<div id="send-picker"></div>
<main>
  <div id="tab-devices">
    <div style="margin-bottom:1rem">
      <button class="btn btn-primary btn-sm" id="discover-btn" onclick="discoverFrames()">Discover photo frames</button>
      <span id="discover-status" style="margin-left:.75rem;color:#666;font-size:.9rem"></span>
    </div>
    <div id="device-list"><p>Loading…</p></div>
  </div>
  <div id="tab-album" style="display:none">
    <div id="upload-zone" onclick="document.getElementById('file-input').click()">
      Drop images here or click to upload (.bin, .png, .jpg)
    </div>
    <input type="file" id="file-input" accept=".bin,.png,.jpg,.jpeg" multiple>
    <div id="image-grid" class="img-grid"></div>
  </div>
  <div id="tab-inkjoy" style="display:none">
    <!-- Adopt modal -->
    <div id="adopt-modal" style="display:none;position:fixed;inset:0;background:#000a;z-index:1000;align-items:center;justify-content:center">
      <div style="background:#fff;border-radius:10px;padding:1.5rem;min-width:320px;max-width:420px">
        <h3 style="margin-top:0">Adopt frame</h3>
        <p id="adopt-frame-name" style="font-family:monospace;color:#555;margin:.25rem 0 1rem"></p>
        <label style="display:block;margin-bottom:.75rem">
          WiFi network (SSID)<br>
          <input id="adopt-ssid" style="width:100%;box-sizing:border-box;padding:.4rem;margin-top:.3rem;border:1px solid #ccc;border-radius:4px">
        </label>
        <label style="display:block;margin-bottom:1rem">
          WiFi password<br>
          <input id="adopt-pwd" type="password" style="width:100%;box-sizing:border-box;padding:.4rem;margin-top:.3rem;border:1px solid #ccc;border-radius:4px">
        </label>
        <div id="adopt-status" style="font-size:.9rem;color:#666;min-height:1.2em;margin-bottom:.75rem"></div>
        <div style="display:flex;gap:.5rem;justify-content:flex-end">
          <button class="btn btn-sm" onclick="closeAdopt()">Cancel</button>
          <button class="btn btn-sm btn-primary" id="adopt-submit-btn" onclick="submitAdopt()">Adopt</button>
        </div>
      </div>
    </div>
    <div style="display:flex;gap:1.5rem;align-items:flex-start">
      <div style="min-width:220px">
        <div class="section-label">Frames</div>
        <div id="inkjoy-frame-list"><p style="color:#888;font-size:.9rem">No InkJoy frames yet.</p></div>
        <div style="margin-top:.75rem">
          <button class="btn btn-sm btn-primary" id="ble-scan-btn" onclick="startBLEScan()">Find new frames</button>
          <span id="ble-scan-status" style="display:block;font-size:.8rem;color:#666;margin-top:.4rem"></span>
        </div>
        <div id="ble-scan-results" style="margin-top:.75rem"></div>
      </div>
      <div id="inkjoy-editor" style="flex:1;display:none">
        <div class="card">
          <div style="display:flex;align-items:center;gap:.75rem;margin-bottom:1rem">
            <div class="dot" id="ij-dot"></div>
            <h3 style="margin:0" id="ij-title"></h3>
            <span id="ij-status-badge" class="badge"></span>
          </div>
          <div style="display:flex;gap:.5rem;margin-bottom:.75rem">
            <input id="ij-name-input" placeholder="Friendly name" style="padding:.3rem .5rem;border:1px solid #ccc;border-radius:4px;flex:1">
            <button class="btn btn-sm btn-primary" onclick="saveIJName()">Rename</button>
          </div>
          <div style="display:flex;align-items:center;gap:.5rem;margin-bottom:1rem">
            <label style="display:flex;align-items:center;gap:.4rem;font-size:.9rem;cursor:pointer">
              <input type="checkbox" id="ij-portrait" onchange="saveIJPortrait()">
              Portrait orientation (3:4 crop, rotate 90°)
            </label>
          </div>
          <div class="info-grid" id="ij-info"></div>
        </div>
        <div class="card">
          <div class="section-label" style="margin-top:0">Currently displayed</div>
          <div id="ij-last-image"><p style="color:#888;font-size:.9rem">Nothing sent yet this session.</p></div>
          <div style="margin-top:.75rem;display:flex;gap:.5rem;flex-wrap:wrap">
            <button class="btn btn-primary btn-sm" onclick="ijSendImage()">Send image from album</button>
            <button class="btn btn-sm" style="background:#6c757d;color:#fff" onclick="ijRefresh()">Refresh display</button>
          </div>
        </div>
        <div class="card">
          <div class="section-label" style="margin-top:0">Sleep schedule</div>
          <div style="display:flex;gap:1rem;align-items:center;flex-wrap:wrap">
            <label style="font-size:.9rem">Sleep from <input type="time" id="ij-sleep-begin" style="margin-left:.4rem;padding:.3rem"></label>
            <label style="font-size:.9rem">to <input type="time" id="ij-sleep-end" style="padding:.3rem"></label>
            <button class="btn btn-sm btn-primary" onclick="ijSaveSleep()">Save</button>
            <button class="btn btn-sm" style="background:#6c757d;color:#fff" onclick="ijClearSleep()">Clear (always on)</button>
          </div>
          <p style="font-size:.8rem;color:#888;margin:.5rem 0 0">Frame will not display images during the sleep window. Clear sets begin=end=00:00.</p>
        </div>
        <div class="card" style="border:1px solid #f5c6cb">
          <div class="section-label" style="margin-top:0;color:#dc3545">Danger zone</div>
          <button class="btn btn-sm" style="background:#dc3545;color:#fff" onclick="ijDeleteDevice()">Remove frame from hub</button>
          <span style="margin-left:.75rem;font-size:.85rem;color:#888">Does not affect the frame itself.</span>
        </div>
      </div>
    </div>
  </div>
  <div id="tab-samsung" style="display:none">
    <div style="display:flex;gap:1.5rem;align-items:flex-start">
      <div style="min-width:220px">
        <div class="section-label">Frames</div>
        <div id="samsung-frame-list"><p style="color:#888;font-size:.9rem">No Samsung frames yet.</p></div>
        <div style="margin-top:.75rem">
          <button class="btn btn-sm btn-primary" id="samsung-discover-btn" onclick="discoverSamsungFrames()">Discover displays</button>
          <span id="samsung-discover-status" style="display:block;font-size:.8rem;color:#666;margin-top:.4rem"></span>
        </div>
      </div>
      <div id="samsung-editor" style="flex:1;display:none">
        <div class="card">
          <div style="display:flex;align-items:center;gap:.75rem;margin-bottom:1rem">
            <div class="dot" id="samsung-dot"></div>
            <h3 style="margin:0" id="samsung-frame-title"></h3>
            <span id="samsung-status-badge" class="badge"></span>
          </div>
          <div style="display:flex;gap:.5rem;margin-bottom:.75rem">
            <input id="samsung-name-input" placeholder="Friendly name" style="padding:.3rem .5rem;border:1px solid #ccc;border-radius:4px;flex:1">
            <button class="btn btn-sm btn-primary" onclick="saveSamsungName()">Rename</button>
          </div>
          <div class="info-grid" id="samsung-info"></div>
        </div>
        <div class="card">
          <div class="section-label" style="margin-top:0">Currently displayed</div>
          <div id="samsung-preview-wrap"><p style="color:#888;font-size:.9rem">No image uploaded yet.</p></div>
          <div style="margin-top:.75rem;display:flex;gap:.5rem;flex-wrap:wrap">
            <button class="btn btn-primary btn-sm" onclick="samsungSendImage()">Send image from album</button>
          </div>
          <div id="samsung-upload-zone" style="border:2px dashed #ccc;border-radius:8px;padding:1.5rem;text-align:center;cursor:pointer;margin-top:1rem" onclick="document.getElementById('samsung-file-input').click()">
            Drop image to upload for this frame
          </div>
          <input type="file" id="samsung-file-input" accept=".png,.jpg,.jpeg,.heic" style="display:none">
          <div id="samsung-status" style="font-size:.85rem;color:#666;margin-top:.75rem"></div>
        </div>
        <div class="card">
          <div class="section-label" style="margin-top:0">Display settings</div>
          <div style="display:flex;flex-wrap:wrap;gap:1rem;align-items:end">
            <label style="font-size:.9rem">Poll interval (min)<br><input type="number" id="samsung-poll" min="1" value="60" style="width:5rem;padding:.3rem;margin-top:.25rem"></label>
            <label style="font-size:.9rem">Inactive begin<br><input type="time" id="samsung-inactive-begin" style="padding:.3rem;margin-top:.25rem"></label>
            <label style="font-size:.9rem">Inactive end<br><input type="time" id="samsung-inactive-end" style="padding:.3rem;margin-top:.25rem"></label>
            <label style="display:flex;align-items:center;gap:.4rem;font-size:.9rem;cursor:pointer;padding-bottom:.3rem">
              <input type="checkbox" id="samsung-portrait">
              Portrait orientation
            </label>
            <label style="font-size:.9rem">Width<br><input type="number" id="samsung-display-width" min="0" placeholder="2560" style="width:6rem;padding:.3rem;margin-top:.25rem"></label>
            <label style="font-size:.9rem">Height<br><input type="number" id="samsung-display-height" min="0" placeholder="1440" style="width:6rem;padding:.3rem;margin-top:.25rem"></label>
            <button class="btn btn-sm btn-primary" onclick="saveSamsungConfig()">Save settings</button>
          </div>
          <p style="font-size:.8rem;color:#888;margin:.75rem 0 0">Frame skips polling during the inactive window.</p>
        </div>
        <div class="card">
          <div class="section-label" style="margin-top:0">Tizen widget (optional)</div>
          <p style="font-size:.9rem;margin:.25rem 0">Install URL for Samsung E-Paper Custom App player:</p>
          <code id="samsung-install-url">loading…</code>
          <p style="color:#666;font-size:.85rem;margin-top:.75rem;margin-bottom:0">Place signed <code>joyous-widget.wgt</code> in <code>data/samsung/</code> on the hub.</p>
        </div>
        <div class="card" style="border:1px solid #f5c6cb">
          <div class="section-label" style="margin-top:0;color:#dc3545">Danger zone</div>
          <button class="btn btn-sm" style="background:#dc3545;color:#fff" onclick="samsungDeleteDevice()">Remove frame from hub</button>
          <span style="margin-left:.75rem;font-size:.85rem;color:#888">Does not affect the display itself.</span>
        </div>
      </div>
    </div>
  </div>
</main>
<script>
let devices=[], images=[];

function showTab(name,btn){
  document.querySelectorAll('[id^=tab-]').forEach(e=>e.style.display='none');
  document.getElementById('tab-'+name).style.display='';
  document.querySelectorAll('nav button').forEach(b=>b.classList.remove('active'));
  btn.classList.add('active');
  if(name==='devices'||name==='samsung') startSamsungReachabilityPoll();
  else stopSamsungReachabilityPoll();
}

let samsungReachabilityTimer=null;
function startSamsungReachabilityPoll(){
  if(samsungReachabilityTimer) return;
  pollSamsungReachability();
  samsungReachabilityTimer=setInterval(pollSamsungReachability, 5*60*1000);
}
function stopSamsungReachabilityPoll(){
  if(samsungReachabilityTimer){ clearInterval(samsungReachabilityTimer); samsungReachabilityTimer=null; }
}
async function pollSamsungReachability(){
  try{
    await fetch('/api/samsung/poll',{method:'POST'});
    await loadDevicesInner();
    loadSamsungFrames();
  }catch(_){}
}

async function loadDevices(){
  await loadDevicesInner();
  loadIJFrames();
  loadSamsungFrames();
}

async function discoverFrames(){
  const btn=document.getElementById('discover-btn');
  const st=document.getElementById('discover-status');
  btn.disabled=true; st.textContent='Scanning network…';
  try{
    const r=await fetch('/api/devices/discover',{method:'POST'});
    const data=await r.json();
    if(!r.ok) throw new Error(data.error||r.statusText);
    const seen=data.ssdp_seen!=null?' ('+data.ssdp_seen+' UPnP devices)':'';
    st.textContent=data.found?'Found '+data.found+' frame(s)'+seen:'No frames matched'+(seen||'');
    loadDevices();
  }catch(e){
    st.textContent='Discovery failed: '+e.message;
  }finally{
    btn.disabled=false;
  }
}

async function refreshDevice(id){
  await fetch('/api/devices/'+encodeURIComponent(id)+'/refresh',{method:'POST'});
}

let imageCropsCache = {}; // id → crops map, kept fresh by loadImages + save/delete

async function loadImages(){
  const r=await fetch('/api/images'); images=await r.json();
  images.forEach(img=>{ imageCropsCache[img.id]=img.crops||{}; });
  const el=document.getElementById('image-grid');
  if(!images||!images.length){el.innerHTML='<p>No images uploaded yet.</p>';return;}
  el.innerHTML=images.map(img=>'<div class="img-card" id="card-'+img.id+'">'+
    '<img src="/images/'+img.id+'/thumb" alt="'+img.name+'" loading="lazy">'+
    '<div class="card-body">'+
      '<div class="name" title="'+img.name+'">'+img.name+'</div>'+
      '<button class="btn btn-sm btn-primary" onclick="openCrop(\''+img.id+'\')">Frame</button> '+
      '<button class="btn btn-sm btn-primary" id="send-btn-'+img.id+'" onclick="sendImageToFrame(event,\''+img.id+'\')">Send</button> '+
      '<button class="btn btn-sm" style="background:#dc3545;color:#fff" onclick="deleteImg(\''+img.id+'\')">✕</button>'+
    '</div>'+
  '</div>').join('');
}

const sendPicker=document.getElementById('send-picker');
let sendPickerImageId=null;
function closePickers(){
  sendPicker.style.display='none';
  sendPicker.style.maxHeight='';
  sendPicker.style.overflowY='';
  sendPickerImageId=null;
}
function positionSendPicker(anchor){
  const margin=8;
  sendPicker.style.display='block';
  sendPicker.style.maxHeight='';
  sendPicker.style.overflowY='';
  sendPicker.style.left='0px';
  sendPicker.style.top='0px';
  const pickerW=sendPicker.offsetWidth;
  const pickerH=sendPicker.offsetHeight;
  const viewH=window.innerHeight;
  const viewW=window.innerWidth;
  let top=anchor.bottom+4;
  if(top+pickerH>viewH-margin && anchor.top-4-pickerH>=margin){
    top=anchor.top-pickerH-4;
  }
  if(top+pickerH>viewH-margin){
    top=margin;
    sendPicker.style.maxHeight=(viewH-2*margin)+'px';
    sendPicker.style.overflowY='auto';
  }
  if(top<margin) top=margin;
  let left=anchor.left;
  if(left+pickerW>viewW-margin) left=viewW-pickerW-margin;
  if(left<margin) left=margin;
  sendPicker.style.top=top+'px';
  sendPicker.style.left=left+'px';
}
document.addEventListener('click',e=>{
  if(!sendPicker.contains(e.target)&&!e.target.id.startsWith('send-btn-')) closePickers();
});

async function doSend(imageId, deviceId, feedbackBtn){
  closePickers();
  const orig=feedbackBtn?feedbackBtn.textContent:'';
  try{
    if(feedbackBtn){feedbackBtn.disabled=true;feedbackBtn.textContent='Sending…';}
    const r=await fetch('/api/devices/'+encodeURIComponent(deviceId)+'/display',{
      method:'POST',headers:{'Content-Type':'application/json'},
      body:JSON.stringify({image_id:imageId})
    });
    if(!r.ok) throw new Error(await r.text());
    if(feedbackBtn){feedbackBtn.textContent='✓ Sent';setTimeout(()=>{feedbackBtn.textContent=orig;feedbackBtn.disabled=false;},2000);}
  }catch(e){
    alert('Send failed: '+e.message);
    if(feedbackBtn){feedbackBtn.textContent=orig;feedbackBtn.disabled=false;}
  }
}

function sendImageToFrame(evt, imageId){
  evt.stopPropagation();
  const frameDevices=devices.filter(d=>d.type==='inkjoy'||d.type==='samsung');
  const btn=document.getElementById('send-btn-'+imageId);
  if(!frameDevices.length){alert('No frames connected — check Devices tab.');return;}
  if(frameDevices.length===1){doSend(imageId,frameDevices[0].id,btn);return;}
  if(sendPickerImageId===imageId){closePickers();return;}
  sendPickerImageId=imageId;
  sendPicker.innerHTML=frameDevices.map(d=>{
    const label=d.name||(d.mac?d.mac:d.ip)||d.id;
    return '<div class="send-picker-item" onclick="doSend(\''+imageId+'\',\''+d.id+'\',document.getElementById(\'send-btn-'+imageId+'\'))">'+label+'</div>';
  }).join('');
  positionSendPicker(btn.getBoundingClientRect());
}

async function sendToFrame(deviceId){
  const frameImages=images;
  if(!frameImages.length){alert('No images in album — upload one first.');return;}
  if(frameImages.length===1){doSend(frameImages[0].id,deviceId,null);return;}
  // Show image picker modal — reuse album grid logic with a simple prompt for now
  // (this path is rarely used; main flow is Album → Send)
  const imageId=prompt('Image ID to send:\n'+frameImages.map(i=>i.id.slice(0,8)+' '+i.name).join('\n'));
  if(!imageId)return;
  const match=frameImages.find(i=>i.id.startsWith(imageId.trim()))||frameImages.find(i=>i.name.includes(imageId.trim()));
  if(!match){alert('Image not found');return;}
  doSend(match.id,deviceId,null);
}

async function deleteImg(id){
  if(!confirm('Delete image?'))return;
  await fetch('/api/images/'+id,{method:'DELETE'});
  loadImages();
}

document.getElementById('file-input').addEventListener('change',async e=>{
  for(const f of e.target.files){
    const fd=new FormData(); fd.append('file',f);
    await fetch('/api/images',{method:'POST',body:fd});
  }
  loadImages();
});

const zone=document.getElementById('upload-zone');
zone.addEventListener('dragover',e=>{e.preventDefault();zone.classList.add('drag')});
zone.addEventListener('dragleave',()=>zone.classList.remove('drag'));
zone.addEventListener('drop',async e=>{
  e.preventDefault();zone.classList.remove('drag');
  for(const f of e.dataTransfer.files){
    const fd=new FormData();fd.append('file',f);
    await fetch('/api/images',{method:'POST',body:fd});
  }
  loadImages();
});

// ── BLE adopt ────────────────────────────────────────────────────────────────
let adoptTarget = null;

async function startBLEScan(){
  const btn=document.getElementById('ble-scan-btn');
  const st=document.getElementById('ble-scan-status');
  const res=document.getElementById('ble-scan-results');
  btn.disabled=true; st.textContent='Scanning for 8 seconds…'; res.innerHTML='';
  try{
    const r=await fetch('/api/inkjoy/ble/scan',{method:'POST'});
    if(!r.ok) throw new Error(await r.text());
    const frames=await r.json();
    if(!frames||!frames.length){st.textContent='No InkJoy frames found nearby.';return;}
    st.textContent=frames.length+' frame(s) found:';
    res.innerHTML=frames.map(f=>
      '<div class="frame-list-item" style="background:#f0f7ff;margin-bottom:.3rem" onclick="openAdopt('+JSON.stringify(f)+')" title="Click to adopt">'+
        '<div class="dot offline"></div>'+
        '<span style="font-weight:500;font-size:.9rem">'+f.name+'</span>'+
        '<button class="btn btn-sm btn-primary" style="margin-left:auto;font-size:.75rem" onclick="event.stopPropagation();openAdopt('+JSON.stringify(f)+')">Adopt</button>'+
      '</div>'
    ).join('');
  }catch(e){
    st.textContent='Scan failed: '+e.message;
  }finally{
    btn.disabled=false;
  }
}

function openAdopt(frame){
  adoptTarget=frame;
  document.getElementById('adopt-frame-name').textContent=frame.name+'  '+frame.mac;
  document.getElementById('adopt-ssid').value='';
  document.getElementById('adopt-pwd').value='';
  document.getElementById('adopt-status').textContent='';
  document.getElementById('adopt-submit-btn').disabled=false;
  document.getElementById('adopt-modal').style.display='flex';
  setTimeout(()=>document.getElementById('adopt-ssid').focus(),100);
}

function closeAdopt(){
  document.getElementById('adopt-modal').style.display='none';
  adoptTarget=null;
}

async function submitAdopt(){
  if(!adoptTarget)return;
  const ssid=document.getElementById('adopt-ssid').value.trim();
  const pwd=document.getElementById('adopt-pwd').value;
  if(!ssid){alert('Enter WiFi network name');return;}
  const st=document.getElementById('adopt-status');
  const btn=document.getElementById('adopt-submit-btn');
  st.textContent='Connecting via Bluetooth…'; btn.disabled=true;
  try{
    const r=await fetch('/api/inkjoy/ble/adopt',{
      method:'POST',headers:{'Content-Type':'application/json'},
      body:JSON.stringify({address:adoptTarget.address,ssid,wifi_pwd:pwd})
    });
    if(!r.ok) throw new Error(await r.text());
    st.style.color='#28a745'; st.textContent='Frame adopted! Waiting for it to connect…';
    setTimeout(async()=>{
      closeAdopt();
      await loadDevicesInner(); loadIJFrames();
      document.getElementById('ble-scan-results').innerHTML='';
      document.getElementById('ble-scan-status').textContent='';
    }, 4000);
  }catch(e){
    st.style.color='#dc3545'; st.textContent='Failed: '+e.message;
    btn.disabled=false;
  }
}

// ── InkJoy tab ───────────────────────────────────────────────────────────────
let ijDevices=[], ijCurrentId=null;

function loadIJFrames(){
  const inkjoy=devices.filter(d=>d.type==='inkjoy');
  ijDevices=inkjoy;
  const el=document.getElementById('inkjoy-frame-list');
  if(!inkjoy.length){el.innerHTML='<p style="color:#888;font-size:.9rem">No InkJoy frames yet.</p>';return;}
  el.innerHTML=inkjoy.map(d=>{
    const label=d.name||d.mac||d.id;
    const sel=d.id===ijCurrentId?' selected':'';
    const online=d.connected;
    return '<div class="frame-list-item'+sel+'" onclick="openIJFrame(\''+d.id+'\')" id="ijli-'+d.id+'">'+
      '<div class="dot '+(online?'online':'offline')+'"></div>'+
      '<span style="font-weight:500">'+label+'</span>'+
      (d.battery?'<span style="margin-left:auto;font-size:.8rem;color:#666">🔋'+d.battery+'%</span>':'')+
      '</div>';
  }).join('');
  // On periodic refresh, only update status/info — never form inputs.
  if(ijCurrentId){
    const d=ijDevices.find(x=>x.id===ijCurrentId);
    if(d) updateIJEditorStatus(d);
  }
}

function openIJFrame(id){
  ijCurrentId=id;
  document.querySelectorAll('.frame-list-item').forEach(el=>el.classList.remove('selected'));
  const li=document.getElementById('ijli-'+id);
  if(li)li.classList.add('selected');
  const d=ijDevices.find(x=>x.id===id);
  if(d) renderIJEditor(d);  // full render including form inputs
  document.getElementById('inkjoy-editor').style.display='';
}

// renderIJEditor does a full render including form inputs — call only when opening a frame.
function renderIJEditor(d){
  updateIJEditorStatus(d);
  document.getElementById('ij-name-input').value=d.name||'';
  document.getElementById('ij-portrait').checked=!!d.portrait;
  document.getElementById('ij-sleep-begin').value=d.sleep_begin_time||'';
  document.getElementById('ij-sleep-end').value=d.sleep_end_time||'';
}

// updateIJEditorStatus refreshes only the read-only parts — safe to call on every poll.
function updateIJEditorStatus(d){
  const label=d.name||d.mac||d.id;
  document.getElementById('ij-title').textContent=label;
  const dot=document.getElementById('ij-dot');
  dot.className='dot '+(d.connected?'online':'offline');
  const badge=document.getElementById('ij-status-badge');
  badge.className='badge '+(d.connected?'online':'offline');
  badge.textContent=d.connected?'online':'offline';
  const ago=d.last_seen?timeAgo(d.last_seen):'never';
  document.getElementById('ij-info').innerHTML=
    '<span class="label">MAC</span><span style="font-family:monospace">'+d.mac+'</span>'+
    '<span class="label">Firmware</span><span>'+(d.firmware||'—')+'</span>'+
    '<span class="label">Battery</span><span>'+(d.battery?d.battery+'%':'—')+'</span>'+
    '<span class="label">Signal</span><span>'+(d.rssi?d.rssi+' dBm':'—')+'</span>'+
    '<span class="label">Last seen</span><span>'+ago+'</span>'+
    '<span class="label">Last action</span><span>'+(d.last_action||'—')+'</span>';
  const li=document.getElementById('ij-last-image');
  if(d.last_image_id){
    li.innerHTML='<img class="last-image-preview" src="/images/'+d.last_image_id+'/thumb" alt="last sent">';
  } else {
    li.innerHTML='<p style="color:#888;font-size:.9rem">Nothing sent yet.</p>';
  }
}

function timeAgo(iso){
  const d=new Date(iso), now=Date.now(), s=Math.round((now-d)/1000);
  if(s<5)return 'just now';
  if(s<60)return s+'s ago';
  if(s<3600)return Math.round(s/60)+'m ago';
  if(s<86400)return Math.round(s/3600)+'h ago';
  return Math.round(s/86400)+'d ago';
}

async function saveIJName(){
  if(!ijCurrentId)return;
  const name=document.getElementById('ij-name-input').value.trim();
  await fetch('/api/devices/'+encodeURIComponent(ijCurrentId),{
    method:'PATCH',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({name})
  });
  await loadDevicesInner();
  loadIJFrames();
}

async function saveIJPortrait(){
  if(!ijCurrentId)return;
  const portrait=document.getElementById('ij-portrait').checked;
  await fetch('/api/devices/'+encodeURIComponent(ijCurrentId),{
    method:'PATCH',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({portrait})
  });
}

async function ijSendImage(){
  if(!ijCurrentId)return;
  let imageId=prompt('Image ID from album:\n'+(images.length?images.map(i=>i.id+' '+i.name).join('\n'):'(upload images first)'));
  if(!imageId)return;
  imageId=imageId.trim().split(/\s+/)[0];
  const r=await fetch('/api/devices/'+encodeURIComponent(ijCurrentId)+'/display',{
    method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({image_id:imageId})
  });
  if(!r.ok){alert('Error: '+(await r.text()));return;}
  await loadDevicesInner();
  loadIJFrames();
}

async function ijRefresh(){
  if(!ijCurrentId)return;
  await fetch('/api/devices/'+encodeURIComponent(ijCurrentId)+'/refresh',{method:'POST'});
}

async function ijSaveSleep(){
  if(!ijCurrentId)return;
  const begin=document.getElementById('ij-sleep-begin').value;
  const end=document.getElementById('ij-sleep-end').value;
  const r=await fetch('/api/devices/'+encodeURIComponent(ijCurrentId)+'/sleep',{
    method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({beginTime:begin||'00:00',endTime:end||'00:00',mode:2})
  });
  if(!r.ok)alert('Error: '+(await r.text()));
}

async function ijClearSleep(){
  if(!ijCurrentId)return;
  document.getElementById('ij-sleep-begin').value='00:00';
  document.getElementById('ij-sleep-end').value='00:00';
  await fetch('/api/devices/'+encodeURIComponent(ijCurrentId)+'/sleep',{
    method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({beginTime:'00:00',endTime:'00:00',mode:2})
  });
}

async function ijDeleteDevice(){
  if(!ijCurrentId)return;
  if(!confirm('Remove this frame from the hub? (The frame itself is unaffected.)'))return;
  await fetch('/api/devices/'+encodeURIComponent(ijCurrentId),{method:'DELETE'});
  ijCurrentId=null;
  document.getElementById('inkjoy-editor').style.display='none';
  await loadDevicesInner();
  loadIJFrames();
}

async function loadDevicesInner(){
  const r=await fetch('/api/devices'); devices=await r.json();
  const el=document.getElementById('device-list');
  if(!devices||!devices.length){el.innerHTML='<p>No frames yet. Connect an InkJoy frame via MQTT, or click Discover for Samsung displays.</p>';return;}
  el.innerHTML=devices.map(d=>{
    const label=d.name||d.mac||d.ip||d.id;
    const type=d.type||'inkjoy';
    const status=type==='samsung'
      ? (d.connected?'<span class="badge online">active</span>':'<span class="badge offline">asleep</span>')
      : (d.connected?'<span class="badge online">online</span>':'<span class="badge offline">offline</span>');
    const meta=type==='inkjoy'
      ? ((d.firmware?'fw '+d.firmware+' ':'')+(d.battery?'🔋'+d.battery+'% ':'')+(d.rssi?'📶'+d.rssi+'dBm ':''))
      : (d.ip?d.ip+' ':'')+(d.display_crop_format?('<span style="color:#666">'+d.display_crop_format+(d.display_width?(' · '+d.display_width+'×'+d.display_height):'')+'</span> '):'')+(d.usn?'<span style="color:#888;font-size:.8rem">'+d.usn.split('::')[0]+'</span>':'');
    const refreshBtn=type==='inkjoy'?'<button class="btn btn-sm btn-primary" onclick="refreshDevice(\''+d.id+'\')">Refresh display</button> ':'';
    return '<div class="card">'+
      '<span class="badge" style="background:#eee;color:#333;margin-right:.5rem">'+type+'</span>'+
      '<strong>'+label+'</strong> '+status+' '+
      '<span style="margin-left:.5rem;color:#666;font-size:.9rem">'+meta+'</span>'+
      '<div style="margin-top:.5rem">'+refreshBtn+
      '<button class="btn btn-sm btn-primary" onclick="sendToFrame(\''+d.id+'\')">Send image</button>'+
      '</div></div>';
  }).join('');
}

loadDevices(); loadImages();
startSamsungReachabilityPoll();
setInterval(loadDevices,5000);
document.getElementById('samsung-install-url').textContent=location.origin+'/samsung/';

// ── Samsung tab ─────────────────────────────────────────────────────────────
let samsungFrames=[], samsungCurrentId=null, samsungStatusCache=null, samsungPreviewEtag=null;

function samsungFrameRecord(frameId){
  return samsungFrames.find(x=>x.id===frameId);
}

function samsungDeviceForFrame(frameId){
  const rec=samsungFrameRecord(frameId);
  if(rec&&rec.device_id) return devices.find(d=>d.id===rec.device_id);
  return devices.find(d=>d.type==='samsung'&&samsungFrameIDFromDevice(d)===frameId);
}

function samsungFrameIDFromDevice(d){
  if(d.ip) return d.ip.replace(/\./g,'-');
  const id=d.id||'';
  if(id.startsWith('samsung:')) return id.slice(8).replace(/\./g,'-');
  return id.replace(/\./g,'-');
}

async function loadSamsungFrames(){
  try{
    const r=await fetch('/api/samsung');
    samsungFrames=await r.json()||[];
  }catch(_){
    samsungFrames=[];
  }
  const el=document.getElementById('samsung-frame-list');
  if(!samsungFrames.length){
    el.innerHTML='<p style="color:#888;font-size:.9rem">No Samsung frames yet.</p>';
    return;
  }
  el.innerHTML=samsungFrames.map(f=>{
    const label=f.name||f.ip||f.id;
    const sel=f.id===samsungCurrentId?' selected':'';
    return '<div class="frame-list-item'+sel+'" onclick="openSamsungFrame(\''+f.id+'\')" id="samli-'+f.id+'">'+
      '<div class="dot '+(f.connected?'online':'offline')+'"></div>'+
      '<span style="font-weight:500">'+label+'</span>'+
      '</div>';
  }).join('');
  if(samsungCurrentId){
    const rec=samsungFrameRecord(samsungCurrentId);
    const d=samsungDeviceForFrame(samsungCurrentId);
    if(rec||d) updateSamsungEditorStatus(d,rec,samsungStatusCache);
    else if(samsungStatusCache) updateSamsungEditorStatus(null,null,samsungStatusCache);
  }
}

async function discoverSamsungFrames(){
  const btn=document.getElementById('samsung-discover-btn');
  const st=document.getElementById('samsung-discover-status');
  btn.disabled=true; st.textContent='Scanning network…';
  try{
    const r=await fetch('/api/devices/discover',{method:'POST'});
    const data=await r.json();
    if(!r.ok) throw new Error(data.error||r.statusText);
    st.textContent=data.found?'Found '+data.found+' frame(s)':'No frames matched';
    await loadDevicesInner();
    loadSamsungFrames();
  }catch(e){
    st.textContent='Discovery failed: '+e.message;
  }finally{
    btn.disabled=false;
  }
}

async function openSamsungFrame(frameId){
  samsungCurrentId=frameId;
  samsungPreviewEtag=null;
  document.querySelectorAll('#samsung-frame-list .frame-list-item').forEach(el=>el.classList.remove('selected'));
  const li=document.getElementById('samli-'+frameId);
  if(li) li.classList.add('selected');
  document.getElementById('samsung-editor').style.display='';
  const d=samsungDeviceForFrame(frameId);
  try{
    const r=await fetch('/samsung/'+encodeURIComponent(frameId)+'/status');
    if(!r.ok) throw new Error(await r.text());
    samsungStatusCache=await r.json();
  }catch(e){
    alert('Load failed: '+e.message);
    return;
  }
  renderSamsungEditor(d,samsungStatusCache,samsungFrameRecord(frameId));
}

function renderSamsungEditor(d,s,rec){
  updateSamsungEditorStatus(d,rec,s);
  document.getElementById('samsung-name-input').value=(s&&s.name)||(rec&&rec.name)||(d&&d.name)||'';
  document.getElementById('samsung-poll').value=(s&&s.poll_interval_minutes)||60;
  document.getElementById('samsung-inactive-begin').value=(s&&s.inactive_begin)||'';
  document.getElementById('samsung-inactive-end').value=(s&&s.inactive_end)||'';
  const fmt=(s&&s.crop_format)||'16:9';
  document.getElementById('samsung-portrait').checked=(fmt==='9:16'||fmt==='3:4');
  document.getElementById('samsung-display-width').value=(s&&s.display_width)||'';
  document.getElementById('samsung-display-height').value=(s&&s.display_height)||'';
}

function updateSamsungEditorStatus(d,rec,s){
  const label=(s&&s.name)||(rec&&rec.name)||(d&&d.name)||(rec&&rec.ip)||(d&&d.ip)||samsungCurrentId||'';
  document.getElementById('samsung-frame-title').textContent=label;
  const online=!!((d&&d.connected)||(rec&&rec.connected));
  document.getElementById('samsung-dot').className='dot '+(online?'online':'offline');
  const badge=document.getElementById('samsung-status-badge');
  badge.className='badge '+(online?'online':'offline');
  badge.textContent=online?'active':'asleep';
  const lastSeen=(d&&d.last_seen)||(rec&&rec.last_seen);
  const ago=lastSeen?timeAgo(lastSeen):'—';
  const ip=(d&&d.ip)||(rec&&rec.ip)||'—';
  const lastAction=(d&&d.last_action)||(rec&&rec.last_action)||'—';
  document.getElementById('samsung-info').innerHTML=
    '<span class="label">Frame ID</span><span style="font-family:monospace">'+samsungCurrentId+'</span>'+
    '<span class="label">IP</span><span>'+ip+'</span>'+
    '<span class="label">Crop</span><span>'+((s&&s.crop_format)||(rec&&rec.crop_format)||'—')+((s&&s.display_width)?(' · '+s.display_width+'×'+s.display_height):'')+'</span>'+
    '<span class="label">Poll</span><span>'+((s&&s.poll_interval_minutes)?(s.poll_interval_minutes+' min'):((rec&&rec.poll_interval_minutes)?(rec.poll_interval_minutes+' min'):'—'))+'</span>'+
    '<span class="label">Last seen</span><span>'+ago+'</span>'+
    '<span class="label">Last action</span><span>'+lastAction+'</span>';
  const wrap=document.getElementById('samsung-preview-wrap');
  if(s&&s.has_image&&!s.locked){
    const url='/samsung/'+encodeURIComponent(samsungCurrentId)+'.png';
    const etag=s.etag||'';
    const img=document.getElementById('samsung-preview');
    if(!img||img.parentElement!==wrap){
      wrap.innerHTML='<img class="last-image-preview" id="samsung-preview" src="'+url+'" alt="current image">';
      samsungPreviewEtag=etag;
    }else if(etag!==samsungPreviewEtag){
      samsungPreviewEtag=etag;
      refreshSamsungPreview();
    }
  }else{
    samsungPreviewEtag=null;
    wrap.innerHTML='<p style="color:#888;font-size:.9rem">'+(s&&s.locked?'Image locked.':'No image uploaded yet.')+'</p>';
  }
  const st=document.getElementById('samsung-status');
  if(s){
    st.textContent=(s.has_image?'Image etag '+s.etag:'No image yet')+(s.locked?' (locked)':'');
    if(s.inactive_begin&&s.inactive_end) st.textContent+=' · inactive '+s.inactive_begin+'–'+s.inactive_end;
  }else{
    st.textContent='';
  }
}

async function reloadSamsungFrame(){
  if(!samsungCurrentId) return;
  const d=samsungDeviceForFrame(samsungCurrentId);
  const rec=samsungFrameRecord(samsungCurrentId);
  try{
    const r=await fetch('/samsung/'+encodeURIComponent(samsungCurrentId)+'/status');
    if(!r.ok) throw new Error(await r.text());
    samsungStatusCache=await r.json();
    updateSamsungEditorStatus(d,rec,samsungStatusCache);
  }catch(_){}
  loadSamsungFrames();
}

async function saveSamsungName(){
  if(!samsungCurrentId)return;
  const name=document.getElementById('samsung-name-input').value.trim();
  const r=await fetch('/samsung/'+encodeURIComponent(samsungCurrentId)+'/status');
  const s=await r.json();
  await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/config',{
    method:'PUT',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({
      name,
      poll_interval_minutes:s.poll_interval_minutes||60,
      inactive_begin:s.inactive_begin||'',
      inactive_end:s.inactive_end||'',
      crop_format:s.crop_format||'16:9',
      display_width:s.display_width||0,
      display_height:s.display_height||0
    })
  });
  document.getElementById('samsung-frame-title').textContent=name||samsungCurrentId;
  await loadDevicesInner();
  loadSamsungFrames();
}

function samsungCropFormat(){
  const portrait=document.getElementById('samsung-portrait').checked;
  const w=parseInt(document.getElementById('samsung-display-width').value,10)||0;
  const h=parseInt(document.getElementById('samsung-display-height').value,10)||0;
  // Pick the right shorthand key for the aspect ratio so it matches saved crop rectangles.
  if(w>0&&h>0){
    const long=Math.max(w,h), short=Math.min(w,h), r=long/short;
    const is43=(r>=1.3&&r<1.4);
    if(portrait) return is43?'3:4':'9:16';
    return is43?'4:3':'16:9';
  }
  return portrait?'9:16':'16:9';
}

async function saveSamsungConfig(){
  if(!samsungCurrentId)return;
  const begin=document.getElementById('samsung-inactive-begin').value;
  const end=document.getElementById('samsung-inactive-end').value;
  const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/config',{
    method:'PUT',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({
      name:document.getElementById('samsung-name-input').value.trim(),
      poll_interval_minutes:parseInt(document.getElementById('samsung-poll').value,10)||60,
      inactive_begin:begin?begin.slice(0,5):'',
      inactive_end:end?end.slice(0,5):'',
      crop_format:samsungCropFormat(),
      display_width:parseInt(document.getElementById('samsung-display-width').value,10)||0,
      display_height:parseInt(document.getElementById('samsung-display-height').value,10)||0
    })
  });
  if(!r.ok){alert('Save failed: '+(await r.text()));return;}
  await reloadSamsungFrame();
  await loadDevicesInner();
}

async function samsungSendImage(){
  if(!samsungCurrentId)return;
  const rec=samsungFrameRecord(samsungCurrentId);
  const dev=samsungDeviceForFrame(samsungCurrentId);
  const deviceId=(rec&&rec.device_id)||(dev&&dev.id);
  if(!deviceId){alert('Frame is not registered on the hub — click Discover displays first.');return;}
  let imageId=prompt('Image ID from album:\n'+(images.length?images.map(i=>i.id+' '+i.name).join('\n'):'(upload images first)'));
  if(!imageId)return;
  imageId=imageId.trim().split(/\s+/)[0];
  const match=images.find(i=>i.id.startsWith(imageId))||images.find(i=>i.name.includes(imageId));
  if(!match){alert('Image not found');return;}
  const r=await fetch('/api/devices/'+encodeURIComponent(deviceId)+'/display',{
    method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({image_id:match.id})
  });
  if(!r.ok){alert('Send failed: '+(await r.text()));return;}
  await loadDevicesInner();
  loadSamsungFrames();
  await reloadSamsungFrame();
}

async function samsungDeleteDevice(){
  if(!samsungCurrentId)return;
  const rec=samsungFrameRecord(samsungCurrentId);
  const dev=samsungDeviceForFrame(samsungCurrentId);
  const deviceId=(rec&&rec.device_id)||(dev&&dev.id);
  if(!deviceId){alert('This frame is not in the device registry.');return;}
  if(!confirm('Remove this frame from the hub? (The display itself is unaffected.)'))return;
  await fetch('/api/devices/'+encodeURIComponent(deviceId),{method:'DELETE'});
  samsungCurrentId=null;
  samsungStatusCache=null;
  samsungPreviewEtag=null;
  document.getElementById('samsung-editor').style.display='none';
  await loadDevicesInner();
  loadSamsungFrames();
}

document.getElementById('samsung-file-input').addEventListener('change',async e=>{
  if(!samsungCurrentId||!e.target.files.length)return;
  const fd=new FormData(); fd.append('file',e.target.files[0]);
  const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/image',{method:'POST',body:fd});
  if(!r.ok){alert('Upload failed: '+(await r.text()));return;}
  await reloadSamsungFrame();
});
const sz=document.getElementById('samsung-upload-zone');
sz.addEventListener('dragover',e=>{e.preventDefault();sz.style.borderColor='#1a1a2e'});
sz.addEventListener('dragleave',()=>{sz.style.borderColor='#ccc'});
sz.addEventListener('drop',async e=>{
  e.preventDefault();sz.style.borderColor='#ccc';
  if(!samsungCurrentId||!e.dataTransfer.files.length)return;
  const fd=new FormData(); fd.append('file',e.dataTransfer.files[0]);
  const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/image',{method:'POST',body:fd});
  if(!r.ok){alert('Upload failed: '+(await r.text()));return;}
  await reloadSamsungFrame();
});
</script>

<!-- ── Crop editor modal ─────────────────────────────────────── -->
<div id="crop-modal">
<div id="crop-ui">
  <div id="crop-toolbar">
    <label>Frame format</label>
    <select id="crop-format" onchange="onFormatChange()">
      <option value="4:3">Landscape 4:3</option>
      <option value="3:4">Portrait 3:4</option>
      <option value="16:9">Landscape 16:9</option>
      <option value="9:16">Portrait 9:16</option>
      <option value="1:1">Square 1:1</option>
    </select>
    <button class="btn btn-primary btn-sm" onclick="saveCrop()">Save</button>
    <button id="crop-delete-btn" class="btn btn-sm" style="background:#c0392b;color:#fff;display:none" onclick="deleteCrop()">Delete</button>
    <button class="btn btn-sm" onclick="closeCrop()">Close</button>
    <span id="crop-hint"></span>
  </div>
  <div id="crop-stage">
    <img id="crop-img" draggable="false">
    <div id="crop-box">
      <div class="ch" data-h="tl"></div>
      <div class="ch" data-h="tr"></div>
      <div class="ch" data-h="bl"></div>
      <div class="ch" data-h="br"></div>
    </div>
  </div>
</div>
</div>

<script>
// ── Crop editor ──────────────────────────────────────────────────────────────
const FORMATS = {
  '4:3':  {ar:4/3},
  '3:4':  {ar:3/4},
  '16:9': {ar:16/9},
  '9:16': {ar:9/16},
  '1:1':  {ar:1},
};

let cropId=null, cropAR=4/3, cropFmt='4:3';
let cropRect={x:0,y:0,w:1,h:1};    // normalised 0-1 (relative to source image)
let cropImgAR=1;                    // source image aspect ratio (w/h), set onload
let imgDisp={x:0,y:0,w:0,h:0};     // image rect in stage pixel coords
let allCrops={};
let drag=null;  // {type:'move'|handle, sx,sy, cr0, corner?}

const cropModal  = ()=>document.getElementById('crop-modal');
const cropImgEl  = ()=>document.getElementById('crop-img');
const cropBoxEl  = ()=>document.getElementById('crop-box');
const cropStageEl= ()=>document.getElementById('crop-stage');

// defaultCrop returns a centered crop rect in normalised source-image coordinates
// that achieves targetAR visually, given the source image has aspect ratio imgAR.
function defaultCrop(targetAR, imgAR){
  if(targetAR > imgAR){
    // target is wider than source: use full width, letterbox height
    const h = imgAR / targetAR;
    return {x:0, y:(1-h)/2, w:1, h};
  } else {
    // target is taller (or equal): use full height, pillarbox width
    const w = targetAR / imgAR;
    return {x:(1-w)/2, y:0, w, h:1};
  }
}

function updateDeleteBtn(){
  document.getElementById('crop-delete-btn').style.display = allCrops[cropFmt] ? '' : 'none';
}

function openCrop(id){
  cropId=id; allCrops={...(imageCropsCache[id]||{})};
  cropFmt = Object.keys(FORMATS).find(k=>allCrops[k]) || '4:3';
  cropAR  = FORMATS[cropFmt].ar;
  document.getElementById('crop-format').value = cropFmt;
  imgDisp  = {x:0,y:0,w:0,h:0};

  const img = cropImgEl();
  img.onload = ()=>{
    cropImgAR = img.naturalWidth / img.naturalHeight;
    layoutImg();
    cropRect = allCrops[cropFmt] ? {...allCrops[cropFmt]} : defaultCrop(cropAR, cropImgAR);
    renderBox();
    renderOverlays();
    updateDeleteBtn();
  };
  img.src = '/images/'+id+'/preview';
  cropModal().classList.add('open');
  setCropScrollLock(true);
}

function closeCrop(){
  cropModal().classList.remove('open');
  cropId=null;
  endCropDrag();
  setCropScrollLock(false);
}

let cropScrollLockY=0;
function setCropScrollLock(on){
  const html=document.documentElement, body=document.body;
  if(on){
    cropScrollLockY=window.scrollY;
    body.style.position='fixed';
    body.style.top='-'+cropScrollLockY+'px';
    body.style.left='0';
    body.style.right='0';
    body.style.width='100%';
    html.style.overflow='hidden';
    body.style.overflow='hidden';
  }else{
    body.style.position='';
    body.style.top='';
    body.style.left='';
    body.style.right='';
    body.style.width='';
    html.style.overflow='';
    body.style.overflow='';
    window.scrollTo(0,cropScrollLockY);
  }
}

function onFormatChange(){
  cropFmt = document.getElementById('crop-format').value;
  cropAR  = FORMATS[cropFmt].ar;
  cropRect = allCrops[cropFmt] ? {...allCrops[cropFmt]} : defaultCrop(cropAR, cropImgAR);
  renderBox();
  renderOverlays();
  updateDeleteBtn();
}

async function saveCrop(){
  if(!cropId) return;
  const r = await fetch('/api/images/'+cropId+'/crop',{
    method:'POST', headers:{'Content-Type':'application/json'},
    body:JSON.stringify({format:cropFmt, ...cropRect})
  });
  if(!r.ok){ alert('Save failed: '+(await r.text())); return; }
  allCrops[cropFmt]={...cropRect};
  if(!imageCropsCache[cropId]) imageCropsCache[cropId]={};
  imageCropsCache[cropId][cropFmt]={...cropRect};
  renderOverlays();
  updateDeleteBtn();
  refreshThumb(cropId);
}

async function deleteCrop(){
  if(!cropId || !allCrops[cropFmt]) return;
  const r = await fetch('/api/images/'+cropId+'/crop?format='+encodeURIComponent(cropFmt),{method:'DELETE'});
  if(!r.ok){ alert('Delete failed: '+(await r.text())); return; }
  delete allCrops[cropFmt];
  if(imageCropsCache[cropId]) delete imageCropsCache[cropId][cropFmt];
  cropRect = defaultCrop(cropAR, cropImgAR);
  renderBox();
  renderOverlays();
  updateDeleteBtn();
  refreshThumb(cropId);
}

function refreshThumb(id){
  const card=document.getElementById('card-'+id);
  if(!card) return;
  const t=card.querySelector('img');
  if(!t) return;
  const url='/images/'+encodeURIComponent(id)+'/thumb';
  // Etag changes when crop is saved; reassign src to force revalidation (no ?t= bust).
  t.src='';
  t.src=url;
}

function refreshSamsungPreview(){
  const t=document.getElementById('samsung-preview');
  if(!t||!samsungCurrentId) return;
  const url='/samsung/'+encodeURIComponent(samsungCurrentId)+'.png';
  t.src='';
  t.src=url;
}

// ── layout ───────────────────────────────────────────────────────────────────
function layoutImg(){
  const stage = cropStageEl(), img = cropImgEl();
  const sw = stage.clientWidth, sh = stage.clientHeight;
  const iw = img.naturalWidth,  ih = img.naturalHeight;
  const scale = Math.min(sw/iw, sh/ih);
  const dw = iw*scale, dh = ih*scale;
  const ox = (sw-dw)/2, oy = (sh-dh)/2;
  img.style.cssText = 'left:'+ox+'px;top:'+oy+'px;width:'+dw+'px;height:'+dh+'px';
  imgDisp = {x:ox,y:oy,w:dw,h:dh};
}

function renderBox(){
  if(!imgDisp.w) return;
  const {x,y,w,h} = cropRect;
  const bx = imgDisp.x + x*imgDisp.w;
  const by = imgDisp.y + y*imgDisp.h;
  const bw = w*imgDisp.w, bh = h*imgDisp.h;
  cropBoxEl().style.cssText = 'left:'+bx+'px;top:'+by+'px;width:'+bw+'px;height:'+bh+'px';
  document.getElementById('crop-hint').textContent =
    Math.round(x*100)+'%, '+Math.round(y*100)+'%  —  '+Math.round(w*100)+'% × '+Math.round(h*100)+'%';
}

// renderOverlays redraws dim dashed boxes for all saved crops except the active one.
function renderOverlays(){
  if(!imgDisp.w) return;
  const stage = cropStageEl();
  stage.querySelectorAll('.crop-dim,.crop-dim-label').forEach(el=>el.remove());
  for(const [fmt, rect] of Object.entries(allCrops)){
    if(fmt===cropFmt) continue;
    const bx = imgDisp.x + rect.x*imgDisp.w;
    const by = imgDisp.y + rect.y*imgDisp.h;
    const bw = rect.w*imgDisp.w, bh = rect.h*imgDisp.h;
    const div = document.createElement('div');
    div.className = 'crop-dim';
    div.style.cssText = 'left:'+bx+'px;top:'+by+'px;width:'+bw+'px;height:'+bh+'px';
    const lbl = document.createElement('span');
    lbl.className = 'crop-dim-label';
    lbl.textContent = fmt;
    div.appendChild(lbl);
    stage.appendChild(div);
  }
}

// ── drag ─────────────────────────────────────────────────────────────────────
function clamp(v,lo,hi){ return Math.max(lo,Math.min(hi,v)); }

function applyDrag(e){
  if(!drag||!imgDisp.w) return;
  const dx=(e.clientX-drag.sx)/imgDisp.w;
  const dy=(e.clientY-drag.sy)/imgDisp.h;
  let {x,y,w,h}=drag.cr0;

  if(drag.type==='move'){
    x=clamp(x+dx, 0, 1-w);
    y=clamp(y+dy, 0, 1-h);
    cropRect={x,y,w,h};
  } else {
    // corner resize — anchor is opposite corner
    const c=drag.corner;
    const ax = c.includes('r') ? x     : x+w;  // anchor x
    const ay = c.includes('b') ? y     : y+h;  // anchor y
    let fx  = c.includes('r') ? x+w+dx : x+dx;
    let fy  = c.includes('b') ? y+h+dy : y+dy;
    fx=clamp(fx,0,1); fy=clamp(fy,0,1);

    let nw=Math.abs(fx-ax), nh=Math.abs(fy-ay);
    // normAR: the w/h ratio in normalised coords that produces visual cropAR.
    // (nw * imgDisp.w) / (nh * imgDisp.h) = cropAR  =>  nw/nh = cropAR / cropImgAR
    const normAR = cropAR / cropImgAR;
    if(nw/normAR > nh){ nh=nw/normAR; } else { nw=nh*normAR; }
    // clamp to image bounds from anchor, then re-enforce ratio
    nw=Math.min(nw, c.includes('r') ? 1-ax : ax);
    nh=Math.min(nh, c.includes('b') ? 1-ay : ay);
    if(nw/normAR > nh){ nw=nh*normAR; } else { nh=nw/normAR; }

    const nx = c.includes('r') ? ax     : ax-nw;
    const ny = c.includes('b') ? ay     : ay-nh;
    cropRect={x:nx,y:ny,w:nw,h:nh};
  }
  renderBox();
}

function cropHandleAt(x,y){
  for(const h of cropBoxEl().querySelectorAll('.ch')){
    const r=h.getBoundingClientRect();
    if(x>=r.left&&x<=r.right&&y>=r.top&&y<=r.bottom) return h.dataset.h;
  }
  return null;
}

function pointInCropBox(x,y){
  const r=cropBoxEl().getBoundingClientRect();
  return x>=r.left&&x<=r.right&&y>=r.top&&y<=r.bottom;
}

function beginCropDrag(clientX,clientY){
  if(!cropModal().classList.contains('open')||!imgDisp.w) return false;
  const corner=cropHandleAt(clientX,clientY);
  if(corner){
    drag={type:'corner',corner,sx:clientX,sy:clientY,cr0:{...cropRect}};
  }else if(pointInCropBox(clientX,clientY)){
    drag={type:'move',sx:clientX,sy:clientY,cr0:{...cropRect}};
  }else{
    return false;
  }
  return true;
}

function onCropDragMove(e){
  if(!drag) return;
  applyDrag(e);
  e.preventDefault();
}

function onCropTouchMove(e){
  if(!drag||!e.touches.length) return;
  applyDrag({clientX:e.touches[0].clientX,clientY:e.touches[0].clientY});
  e.preventDefault();
}

function endCropDrag(){
  drag=null;
  document.removeEventListener('pointermove',onCropDragMove);
  document.removeEventListener('pointerup',endCropDrag);
  document.removeEventListener('pointercancel',endCropDrag);
  document.removeEventListener('touchmove',onCropTouchMove);
  document.removeEventListener('touchend',endCropDrag);
  document.removeEventListener('touchcancel',endCropDrag);
}

function onStagePointerDown(e){
  if(e.pointerType==='touch') return; // touchstart handles touch; avoids double drag
  if(!beginCropDrag(e.clientX,e.clientY)) return;
  e.preventDefault();
  document.addEventListener('pointermove',onCropDragMove,{passive:false});
  document.addEventListener('pointerup',endCropDrag);
  document.addEventListener('pointercancel',endCropDrag);
}

function onStageTouchStart(e){
  if(e.touches.length!==1) return;
  const t=e.touches[0];
  if(!beginCropDrag(t.clientX,t.clientY)) return;
  e.preventDefault();
  document.addEventListener('touchmove',onCropTouchMove,{passive:false});
  document.addEventListener('touchend',endCropDrag);
  document.addEventListener('touchcancel',endCropDrag);
}

(function initCropDrag(){
  const stage=cropStageEl();
  const modal=cropModal();
  stage.addEventListener('pointerdown',onStagePointerDown);
  stage.addEventListener('touchstart',onStageTouchStart,{passive:false});
  // Stop iOS from scrolling the album grid behind the modal.
  modal.addEventListener('touchmove',e=>{
    if(modal.classList.contains('open')) e.preventDefault();
  },{passive:false});
})();
window.addEventListener('resize',()=>{ if(imgDisp.w){ layoutImg(); renderBox(); renderOverlays(); } });
</script>
</body>
</html>`
