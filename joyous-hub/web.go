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
	devices        *DeviceRegistry
	samsungBattery *SamsungBatteryStore
	samsungAliases *samsungFrameAliases
	images         *ImageStore
	displayPreview *DisplayPreviewStore
	inkjoy         *InkJoyCache
	samsung        *SamsungStore
	sendDelivery   *SendDeliveryTracker
	overlay        *OverlayStore
	color          *ColorStore
	weather        weatherClient
	hubIP      string // resolved non-loopback LAN IP, used for play URLs and BLE adoption
	publisher  MQTTPublisher
	serverAddr string // e.g. "192.168.1.5:8080" — used in play URLs
	mqttPort   int    // MQTT broker port the frame should connect to (e.g. 11883)
	mqttLog    *MQTTLogBuffer
}

// handleMQTTLogs serves GET /api/mqtt/logs — last N messages per side for the web UI.
func (h *Hub) handleMQTTLogs(w http.ResponseWriter, r *http.Request) {
	local, upstream := h.mqttLog.Snapshot()
	if local == nil {
		local = []MQTTLogEntry{}
	}
	if upstream == nil {
		upstream = []MQTTLogEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"local":    local,
		"upstream": upstream,
	})
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
		if devs[i].Type == DeviceTypeSamsung {
			applySamsungBatterySummary(&devs[i], h.samsungBatterySummary(devs[i].ID, 8))
			if frameID := SamsungFrameID(&devs[i]); frameID != "" {
				if cfg, err := h.samsung.LoadConfig(frameID); err == nil {
					devs[i].DeepSleepActive = cfg.DeepSleepActive
				}
			}
		}
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

// handleImageRename serves PATCH /api/images/{id} — updates display name in metadata only.
func (h *Hub) handleImageRename(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	meta, err := h.images.Rename(id, body.Name)
	if err != nil {
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			code = http.StatusNotFound
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(meta)
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
	sendID, err := h.sendImageToDeviceAuto(deviceID, body.ImageID)
	if err != nil {
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "frame did not wake") {
			code = http.StatusGatewayTimeout
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "send_id": sendID})
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
// Ack result bitfield: see inkjoy_ack.go

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

// buildAckPayloadFor builds a frame→cloud ack matching real frame shape:
// clientid, stamac, data.ack_msgid, and result (default inkjoyAckAccepted).
func buildAckPayloadFor(mac, ackAction, ackMsgid string, data map[string]any) []byte {
	if data == nil {
		data = map[string]any{}
	}
	if ackMsgid != "" {
		data["ack_msgid"] = ackMsgid
	}
	if _, ok := data["result"]; !ok {
		data["result"] = inkjoyAckAccepted
	}
	msg := map[string]any{
		"action":   ackAction,
		"clientid": mac,
		"msgid":    fmt.Sprintf("%d", time.Now().UnixMilli()),
		"stamac":   mac,
		"data":     data,
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
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Caveat:wght@500;600&display=swap" rel="stylesheet">
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
  .samsung-icon-btn{padding:.35rem .45rem;line-height:0;display:inline-flex;align-items:center;justify-content:center;background:#fff;border:1px solid #ccc;color:#333}
  .samsung-icon-btn:hover{background:#f5f5f5}
  .samsung-icon-btn svg{width:18px;height:18px}
  .samsung-icon-btn.wake{color:#5a67d8}
  .samsung-icon-btn.sleep{color:#555}
  .img-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:1rem}
  .img-card{background:#fff;border-radius:8px;overflow:hidden;box-shadow:0 1px 3px #0002;text-align:center}
  .img-card img{width:100%;aspect-ratio:4/3;object-fit:contain;display:block;background:#eee}
  .img-card .card-body{padding:.5rem .75rem}
  .img-card .name{font-size:.8rem;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;margin-bottom:.4rem}
  /* album wall */
  #tab-album.album-wall{
    margin:-2rem calc(50% - 50vw) 0;
    width:100vw;
    max-width:100vw;
    min-height:calc(100vh - 64px);
    background:#d9d2c4;
    background-image:radial-gradient(120% 90% at 50% 0%,#e7e1d4 0%,#d4ccbc 55%,#c6bda9 100%);
    color:#22252e;
  }
  .album-upload-wrap{max-width:1180px;margin:0 auto;padding:34px 40px 8px}
  #upload-zone.album-upload{
    display:flex;align-items:center;justify-content:center;gap:10px;
    border:2px dashed #b3a994;border-radius:12px;padding:18px;margin-bottom:0;
    color:#6e6450;font-size:15px;background:rgba(255,255,255,.28);cursor:pointer;
  }
  #upload-zone.album-upload.drag{border-color:#8a8069;background:rgba(255,255,255,.45)}
  .album-upload-mono{font-family:ui-monospace,Menlo,monospace;font-size:12.5px;color:#8a8069}
  .album-grid{
    display:flex;flex-wrap:wrap;justify-content:center;align-items:center;gap:0;
    padding:30px 56px 90px;max-width:1240px;margin:0 auto;
  }
  .album-outer{
    width:auto;flex:0 0 auto;margin:-18px -12px -18px 0;position:relative;cursor:pointer;
    transform:translateY(var(--dy,0)) rotate(var(--rot,0deg));transform-origin:center 60%;
    transition:transform .26s cubic-bezier(.2,.8,.3,1.25);z-index:var(--z,1);
  }
  .album-outer-portrait{margin-top:-34px;margin-bottom:-34px}
  @media (hover:hover){
    .album-outer:hover{transform:translateY(-6px) rotate(0deg) scale(1.075);z-index:1000}
    .album-outer:hover .album-print{
      box-shadow:0 26px 46px -8px rgba(40,28,12,.42),0 4px 10px rgba(0,0,0,.18);
    }
    .album-outer:hover .album-menu{opacity:1;transform:translateY(0);pointer-events:auto}
  }
  .album-outer-selected{
    transform:translateY(-6px) rotate(0deg) scale(1.075)!important;z-index:1000!important;
  }
  .album-outer-selected .album-print{
    box-shadow:0 26px 46px -8px rgba(40,28,12,.42),0 4px 10px rgba(0,0,0,.18);
  }
  .album-outer-selected .album-menu{opacity:1;transform:translateY(0);pointer-events:auto}
  .album-print{
    width:fit-content;background:#fdfbf6;padding:12px 12px 0;border-radius:2px;position:relative;
    box-shadow:0 7px 16px -4px rgba(50,38,20,.28),0 1px 3px rgba(0,0,0,.12);
    transition:box-shadow .26s ease;
  }
  .album-img{
    position:relative;width:var(--photo-w);height:var(--photo-h);background:#ece8e0;overflow:hidden;
  }
  .album-img img{width:100%;height:100%;object-fit:contain;display:block}
  .album-menu{
    position:absolute;left:0;right:0;bottom:9px;display:flex;justify-content:center;gap:6px;
    opacity:0;transform:translateY(8px);pointer-events:none;
    transition:opacity .2s ease,transform .2s ease;
  }
  .album-btn{border:0;cursor:pointer;font:600 12px/1 inherit;color:#fff;background:#1f2740;padding:7px 11px;border-radius:6px}
  .album-btn-delete{background:#e1483f;padding:7px 10px;font-size:13px;line-height:1}
  .album-caption{
    font-family:'Caveat',cursive;font-size:21px;line-height:1;color:#4a4135;text-align:center;
    padding:11px 6px 13px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;
    max-width:var(--photo-w,100%);box-sizing:border-box;min-width:0;
  }
  .album-outer-selected .album-caption,
  .album-outer:hover .album-caption{cursor:text}
  .album-caption-editing{
    outline:none;overflow:visible;text-overflow:clip;
    border-bottom:1px dashed #b8ad98;white-space:nowrap;
  }
  .album-caption-editing:focus{border-bottom-color:#4a4135}
  .album-empty{text-align:center;color:#6e6450;padding:3rem 1.5rem;font-size:15px;margin:0}
  @media (max-width:640px){
    .album-upload-wrap{padding:20px 16px 8px}
    .album-grid{padding:20px 12px 60px}
  }
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
  #upload-zone.drag{border-color:#1a1a2e;background:#f0f0ff}
  input[type=file]{display:none}
  #send-picker{display:none;position:fixed;z-index:5000;background:#fff;border:1px solid #ccc;border-radius:6px;box-shadow:0 4px 12px #0002;min-width:160px;max-height:min(50vh,320px);overflow-y:auto;-webkit-overflow-scrolling:touch}
  .send-picker-item{padding:.5rem .85rem;cursor:pointer;font-size:.9rem;white-space:nowrap}
  .send-picker-item:hover{background:#f0f0ff}
  .mqtt-grid{display:grid;grid-template-columns:1fr 1fr;gap:1rem}
  @media(max-width:900px){.mqtt-grid{grid-template-columns:1fr}}
  .mqtt-col h2{font-size:1rem;margin:0 0 .75rem;color:#333}
  .mqtt-log{max-height:70vh;overflow-y:auto;overscroll-behavior:contain;-webkit-overflow-scrolling:touch;display:flex;flex-direction:column;gap:.5rem}
  .mqtt-entry{border:1px solid #e0e0e0;border-radius:6px;padding:.6rem .75rem;font-size:.8rem;background:#fafafa}
  .mqtt-entry.clampable{cursor:pointer}
  .mqtt-entry.clampable:not(.expanded):hover{border-color:#bbb;background:#f5f5f5}
  .mqtt-entry .meta{display:flex;flex-wrap:wrap;gap:.35rem .6rem;align-items:center;margin-bottom:.35rem;color:#555}
  .mqtt-entry .time{font-family:monospace;color:#888}
  .mqtt-entry .dir{font-weight:600;color:#1a1a2e}
  .mqtt-entry .action{background:#e8eaf6;color:#1a1a2e;padding:1px 6px;border-radius:4px;font-family:monospace;font-size:.75rem}
  .mqtt-entry .note{color:#856404;background:#fff3cd;padding:1px 6px;border-radius:4px;font-size:.75rem}
  .mqtt-entry .topic{font-family:monospace;font-size:.72rem;color:#666;word-break:break-all;margin-bottom:.25rem}
  .mqtt-entry pre{margin:0;white-space:pre-wrap;word-break:break-word;font-size:.72rem;line-height:1.35;background:#fff;border:1px solid #eee;border-radius:4px;padding:.4rem .5rem}
  .mqtt-entry.clampable:not(.expanded) pre{display:-webkit-box;-webkit-line-clamp:6;-webkit-box-orient:vertical;overflow:hidden}
  .mqtt-expand-hint{font-size:.68rem;color:#888;margin-top:.2rem}
  .mqtt-empty{color:#888;font-size:.9rem;margin:0}
</style>
</head>
<body>
<header>
  <h1>Joyous</h1>
  <nav>
    <button class="active" onclick="showTab('devices',this)">Devices</button>
    <button onclick="showTab('album',this)">Album</button>
    <button onclick="showTab('overlays',this)">Overlays</button>
    <button onclick="showTab('color',this)">Color</button>
    <button onclick="showTab('inkjoy',this)">InkJoy</button>
    <button onclick="showTab('mqtt',this)">MQTT</button>
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
  <div id="tab-album" class="album-wall" style="display:none">
    <div class="album-upload-wrap">
      <div id="upload-zone" class="album-upload" onclick="document.getElementById('file-input').click()">
        Drop images here or click to upload <span class="album-upload-mono">(.bin, .png, .jpg)</span>
      </div>
    </div>
    <div id="image-grid" class="album-grid"></div>
    <input type="file" id="file-input" accept=".bin,.png,.jpg,.jpeg" multiple>
  </div>
  <div id="tab-overlays" style="display:none">
    <div style="display:flex;gap:1.5rem;align-items:flex-start;flex-wrap:wrap">
      <div class="card" style="flex:1;min-width:280px;max-width:420px">
        <div class="section-label" style="margin-top:0">Settings</div>
        <label style="display:flex;align-items:center;gap:.5rem;margin:.5rem 0;font-size:.9rem">
          <input type="checkbox" id="ovl-enabled"> Enable overlay on send
        </label>
        <label style="display:block;margin:.5rem 0;font-size:.85rem">Location (city)
          <input id="ovl-location" placeholder="Seattle" style="width:100%;box-sizing:border-box;padding:.35rem .5rem;margin-top:.25rem;border:1px solid #ccc;border-radius:4px">
        </label>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:.5rem;margin:.5rem 0">
          <label style="font-size:.85rem">Latitude
            <input id="ovl-lat" type="number" step="any" placeholder="optional" style="width:100%;box-sizing:border-box;padding:.35rem .5rem;margin-top:.25rem;border:1px solid #ccc;border-radius:4px">
          </label>
          <label style="font-size:.85rem">Longitude
            <input id="ovl-lon" type="number" step="any" placeholder="optional" style="width:100%;box-sizing:border-box;padding:.35rem .5rem;margin-top:.25rem;border:1px solid #ccc;border-radius:4px">
          </label>
        </div>
        <label style="display:block;margin:.5rem 0;font-size:.85rem">Timezone (optional)
          <input id="ovl-timezone" placeholder="America/Los_Angeles" style="width:100%;box-sizing:border-box;padding:.35rem .5rem;margin-top:.25rem;border:1px solid #ccc;border-radius:4px">
        </label>
        <div class="section-label">Overlay template</div>
        <p style="font-size:.8rem;color:#666;margin:.25rem 0 .5rem;line-height:1.4">One line per row on the frame. Go <code>text/template</code> syntax — fields and helpers below.</p>
        <textarea id="ovl-template" rows="6" spellcheck="false" style="width:100%;box-sizing:border-box;padding:.5rem;font-family:ui-monospace,Menlo,monospace;font-size:.8rem;line-height:1.35;border:1px solid #ccc;border-radius:4px;resize:vertical" oninput="debouncedOverlayPreview()"></textarea>
        <div id="ovl-template-metrics" style="margin:.5rem 0;font-size:.8rem;color:#555"></div>
        <details style="margin:.5rem 0;font-size:.8rem;color:#555">
          <summary style="cursor:pointer;margin-bottom:.35rem">Fields &amp; helpers</summary>
          <div style="line-height:1.5;font-family:ui-monospace,Menlo,monospace;font-size:.75rem">
            <div><b>.City</b> · <b>.Condition</b></div>
            <div><b>.Temperature.Current</b> · <b>.Temperature.Min</b> · <b>.Temperature.Max</b></div>
            <div><b>.Precipitation.Hour</b> (this hour %) · <b>.Precipitation.Max</b> (daily max %)</div>
            <div><b>{{date .Date .DateStyle}}</b> · <b>{{fahrenheit .Temperature.Min}}</b> · <b>{{celsius .Temperature.Current}}</b> · <b>{{pct .Precipitation.Hour}}</b></div>
            <div style="margin-top:.35rem;color:#666">Example: <code>{{fahrenheit .Temperature.Min}}-{{fahrenheit .Temperature.Max}}  {{pct .Precipitation.Max}}</code></div>
          </div>
        </details>
        <label style="display:block;margin:.75rem 0 .35rem;font-size:.85rem">Weather display
          <select id="ovl-weather-style" onchange="debouncedOverlayPreview()" style="width:100%;padding:.35rem;margin-top:.25rem;border:1px solid #ccc;border-radius:4px">
            <option value="box">Opaque box</option>
            <option value="outline">Bordered text</option>
          </select>
        </label>
        <label style="display:flex;align-items:center;gap:.5rem;margin:.35rem 0;font-size:.9rem"><input type="checkbox" id="ovl-fahrenheit" checked onchange="debouncedOverlayPreview()"> Use Fahrenheit in templates</label>
        <label style="display:block;margin:.75rem 0 .35rem;font-size:.85rem">Date format
          <select id="ovl-date-style" onchange="debouncedOverlayPreview()" style="width:100%;padding:.35rem;margin-top:.25rem;border:1px solid #ccc;border-radius:4px">
            <option value="1">Jun 20, 2026</option>
            <option value="2">20 Jun 2026</option>
            <option value="3">June 20 2026</option>
          </select>
        </label>
        <div class="section-label" style="margin-top:.75rem">Photo name</div>
        <label style="display:flex;align-items:center;gap:.5rem;margin:.35rem 0;font-size:.9rem">
          <input type="checkbox" id="ovl-show-photo-name" onchange="debouncedOverlayPreview()"> Show filename (without extension)
        </label>
        <label style="display:block;margin:.35rem 0 .75rem;font-size:.85rem">Position
          <select id="ovl-photo-name-position" onchange="debouncedOverlayPreview()" style="width:100%;padding:.35rem;margin-top:.25rem;border:1px solid #ccc;border-radius:4px">
            <option value="bottom_right">Bottom right</option>
            <option value="bottom_center">Bottom center</option>
          </select>
        </label>
        <p style="font-size:.8rem;color:#666;margin:0 0 .5rem;line-height:1.4">Caveat handwriting (matches album captions), black or white with a thin opposite outline, no background panel.</p>
        <button class="btn btn-primary btn-sm" style="margin-top:.75rem" onclick="saveOverlayConfig()">Save settings</button>
        <div id="ovl-save-status" style="font-size:.85rem;color:#666;margin-top:.5rem"></div>
      </div>
      <div class="card" style="flex:2;min-width:320px">
        <div class="section-label" style="margin-top:0">Preview</div>
        <div style="display:flex;gap:.75rem;flex-wrap:wrap;align-items:center;margin-bottom:.75rem">
          <label style="font-size:.85rem">Preview image
            <select id="ovl-preview-image" onchange="refreshOverlayPreview()" style="display:block;min-width:220px;margin-top:.25rem;padding:.35rem;border:1px solid #ccc;border-radius:4px"></select>
          </label>
          <label style="display:flex;align-items:center;gap:.4rem;font-size:.85rem;margin-top:1.1rem">
            <input type="checkbox" id="ovl-preview-portrait" onchange="refreshOverlayPreview()"> Portrait
          </label>
          <button class="btn btn-sm" style="margin-top:1.1rem" onclick="refreshOverlayPreview()">Refresh preview</button>
        </div>
        <div id="ovl-preview-wrap"><p style="color:#888;font-size:.9rem">Pick an album image to preview.</p></div>
        <div class="section-label">Send with overlay</div>
        <p style="font-size:.85rem;color:#666;margin:.25rem 0 .75rem">Re-sends each frame’s current album image with a fresh weather overlay. Album → Send also adds the overlay when enabled.</p>
        <div id="ovl-send-list"><p style="color:#888;font-size:.9rem">Loading frames…</p></div>
      </div>
    </div>
  </div>
  <div id="tab-color" style="display:none">
    <div class="card" style="max-width:760px">
      <div class="section-label" style="margin-top:0">Pre-dither LAB processing</div>
      <p style="font-size:.85rem;color:#666;margin:.25rem 0 .75rem">Optional steps before Stucki dithering (skipped for calibration swatches and ≤6-color images). Chroma, highlight, and shadow are independent.</p>
      <label style="display:flex;align-items:center;gap:.5rem;margin:.5rem 0;font-size:.9rem">
        <input type="checkbox" id="clr-lab-chroma"> Chroma boost (pulls neutrals toward their hue)
      </label>
      <label style="display:block;margin:.35rem 0 .75rem;font-size:.85rem">Chroma strength
        <input type="range" id="clr-lab-chroma-strength" min="0" max="3" step="0.1" value="1" style="width:100%;margin-top:.35rem" oninput="document.getElementById('clr-lab-chroma-strength-val').textContent=this.value">
        <span id="clr-lab-chroma-strength-val" style="font-family:monospace">1</span>
      </label>
      <label style="display:flex;align-items:center;gap:.5rem;margin:.5rem 0;font-size:.9rem">
        <input type="checkbox" id="clr-lab-highlight"> Highlight rolloff (L channel — tames near-whites)
      </label>
      <label style="display:block;margin:.35rem 0 .75rem;font-size:.85rem">Highlight strength
        <input type="range" id="clr-lab-highlight-strength" min="0" max="3" step="0.1" value="1" style="width:100%;margin-top:.35rem" oninput="document.getElementById('clr-lab-highlight-strength-val').textContent=this.value">
        <span id="clr-lab-highlight-strength-val" style="font-family:monospace">1</span>
      </label>
      <label style="display:flex;align-items:center;gap:.5rem;margin:.5rem 0;font-size:.9rem">
        <input type="checkbox" id="clr-lab-shadow"> Shadow lift (L channel — brightens dark areas)
      </label>
      <label style="display:block;margin:.35rem 0 .75rem;font-size:.85rem">Shadow strength
        <input type="range" id="clr-lab-shadow-strength" min="0" max="3" step="0.1" value="1" style="width:100%;margin-top:.35rem" oninput="document.getElementById('clr-lab-shadow-strength-val').textContent=this.value">
        <span id="clr-lab-shadow-strength-val" style="font-family:monospace">1</span>
      </label>
      <div class="section-label">InkJoy dither palette (P2)</div>
      <label style="display:block;margin:.5rem 0 .35rem;font-size:.85rem">Preset
        <select id="clr-inkjoy-display-preset" onchange="onColorPresetChange('inkjoy_display')" style="width:100%;padding:.35rem;margin-top:.25rem;border:1px solid #ccc;border-radius:4px"></select>
      </label>
      <div id="clr-inkjoy-display-swatches"></div>
      <div class="section-label">Samsung dither palette (P2)</div>
      <label style="display:block;margin:.5rem 0 .35rem;font-size:.85rem">Preset
        <select id="clr-samsung-display-preset" onchange="onColorPresetChange('samsung_display')" style="width:100%;padding:.35rem;margin-top:.25rem;border:1px solid #ccc;border-radius:4px"></select>
      </label>
      <div id="clr-samsung-display-swatches"></div>
      <div class="section-label">Samsung send palette (P1 PNG)</div>
      <label style="display:block;margin:.5rem 0 .35rem;font-size:.85rem">Preset
        <select id="clr-samsung-send-preset" onchange="onColorPresetChange('samsung_send')" style="width:100%;padding:.35rem;margin-top:.25rem;border:1px solid #ccc;border-radius:4px"></select>
      </label>
      <div id="clr-samsung-send-swatches"></div>
      <button class="btn btn-primary btn-sm" style="margin-top:1rem" onclick="saveColorConfig()">Save color settings</button>
      <div id="clr-save-status" style="font-size:.85rem;color:#666;margin-top:.5rem"></div>
      <p style="font-size:.8rem;color:#888;margin-top:.75rem">Saving clears the converted .bin cache. Re-send album images to frames to apply new palettes.</p>
    </div>
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
            <button class="btn btn-sm" style="background:#6c757d;color:#fff" onclick="ijRefresh()">Refresh display</button>
          </div>
          <p style="font-size:.8rem;color:#888;margin:.5rem 0 0">To send from the shared album, use <strong>Album → Send</strong> and pick this frame.</p>
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
  <div id="tab-mqtt" style="display:none">
    <p style="color:#666;font-size:.9rem;margin-top:0">Last 20 messages per side. Newest at top. Scroll the column; click a long message to expand its body.</p>
    <div class="mqtt-grid">
      <div class="mqtt-col card">
        <h2>Frame ↔ Hub</h2>
        <div id="mqtt-local" class="mqtt-log"><p class="mqtt-empty">No messages yet.</p></div>
      </div>
      <div class="mqtt-col card">
        <h2>Hub ↔ Cloud</h2>
        <div id="mqtt-upstream" class="mqtt-log"><p class="mqtt-empty">No messages yet.</p></div>
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
            <span style="margin-left:auto;display:flex;gap:.35rem">
              <button type="button" class="btn btn-sm samsung-icon-btn wake" id="samsung-wake-btn" onclick="samsungWake()" title="Wake display" style="display:none" aria-label="Wake display">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
              </button>
              <button type="button" class="btn btn-sm samsung-icon-btn sleep" id="samsung-sleep-btn" onclick="samsungSleep()" title="Sleep display" style="display:none" aria-label="Sleep display">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M12 2v10"/><path d="M18.36 6.64a9 9 0 1 1-12.73 0"/></svg>
              </button>
            </span>
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
            <button class="btn btn-primary btn-sm" onclick="samsungPushCurrent()">Push to display</button>
          </div>
          <div id="samsung-upload-zone" style="border:2px dashed #ccc;border-radius:8px;padding:1.5rem;text-align:center;cursor:pointer;margin-top:1rem" onclick="document.getElementById('samsung-file-input').click()">
            Drop image to upload for this frame
          </div>
          <p style="font-size:.8rem;color:#888;margin:.5rem 0 0">To send from the shared album, use <strong>Album → Send</strong> and pick this frame.</p>
          <input type="file" id="samsung-file-input" accept=".png,.jpg,.jpeg,.heic" style="display:none">
          <div id="samsung-status" style="font-size:.85rem;color:#666;margin-top:.75rem"></div>
        </div>
        <div class="card">
          <div class="section-label" style="margin-top:0">Display settings</div>
          <div style="display:flex;flex-wrap:wrap;gap:1rem;align-items:end">
            <label style="font-size:.9rem">WiFi MAC (wake)<br><input type="text" id="samsung-wifi-mac" placeholder="AA:BB:CC:DD:EE:FF" style="width:11rem;padding:.3rem;margin-top:.25rem;font-family:monospace"></label>
            <label style="font-size:.9rem">Poll interval (min)<br><input type="number" id="samsung-poll" min="1" value="60" style="width:5rem;padding:.3rem;margin-top:.25rem"></label>
            <label style="font-size:.9rem">Inactive begin<br><input type="time" id="samsung-inactive-begin" style="padding:.3rem;margin-top:.25rem"></label>
            <label style="font-size:.9rem">Inactive end<br><input type="time" id="samsung-inactive-end" style="padding:.3rem;margin-top:.25rem"></label>
            <label style="font-size:.9rem">Daily refresh<br><input type="time" id="samsung-daily-refresh" style="padding:.3rem;margin-top:.25rem" title="Frame e-ink refresh time"></label>
            <button type="button" class="btn btn-sm" id="samsung-daily-refresh-sync" onclick="samsungSyncDailyRefresh()" title="Set frame daily refresh to inactive end">Sync to inactive end</button>
            <span id="samsung-daily-refresh-status" style="font-size:.8rem;color:#666;align-self:end;padding-bottom:.3rem"></span>
            <label style="display:flex;align-items:center;gap:.4rem;font-size:.9rem;cursor:pointer;padding-bottom:.3rem">
              <input type="checkbox" id="samsung-overnight-deep-sleep" checked>
              Overnight deep sleep
            </label>
            <label style="display:flex;align-items:center;gap:.4rem;font-size:.9rem;cursor:pointer;padding-bottom:.3rem">
              <input type="checkbox" id="samsung-portrait">
              Portrait orientation
            </label>
            <label style="font-size:.9rem">Width<br><input type="number" id="samsung-display-width" min="0" placeholder="2560" style="width:6rem;padding:.3rem;margin-top:.25rem"></label>
            <label style="font-size:.9rem">Height<br><input type="number" id="samsung-display-height" min="0" placeholder="1440" style="width:6rem;padding:.3rem;margin-top:.25rem"></label>
            <label style="font-size:.9rem;display:flex;align-items:center;gap:.35rem;margin-top:1.1rem">
              <input type="checkbox" id="samsung-auto-sleep" checked>
              Sleep after send
            </label>
            <label style="font-size:.9rem">Sleep delay (sec)<br><input type="number" id="samsung-sleep-delay" min="5" value="15" style="width:5rem;padding:.3rem;margin-top:.25rem"></label>
            <button class="btn btn-sm btn-primary" onclick="saveSamsungConfig()">Save settings</button>
          </div>
          <p style="font-size:.8rem;color:#888;margin:.5rem 0 0">The frame should stay <strong>asleep</strong> between pushes. On send: wake → deliver image → sleep (after delay). WiFi MAC required for remote wake. Moon/power are for manual override only.</p>
          <p style="font-size:.8rem;color:#888;margin:.5rem 0 0">Set inactive begin and end to the <strong>same time</strong> (e.g. 00:00–00:00) to disable hub-managed network sleep — the frame keeps its current deep sleep or network standby mode.</p>
          <p style="font-size:.8rem;color:#888;margin:.75rem 0 0"><strong>Overnight deep sleep:</strong> at inactive begin the hub wakes the frame, turns off network standby, and sleeps it (lower battery drain). A send during inactive hours needs a <strong>3s power-button wake</strong> and returns to deep sleep after; outside those hours the hub restores network standby for remote wake.</p>
          <p style="font-size:.8rem;color:#888;margin:.5rem 0 0"><strong>Daily refresh:</strong> the frame pre-refreshes a few minutes before the scheduled time, then refreshes at inactive end. While it is awake for that cycle, the hub tries to restore network standby from <em>10 minutes before</em> inactive end through inactive end (e.g. 8:50–9:00). Sync daily refresh to <em>inactive end</em>.</p>
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
let devices=[], images=[], activeTab='devices';

function showTab(name,btn){
  activeTab=name;
  document.querySelectorAll('[id^=tab-]').forEach(e=>e.style.display='none');
  document.getElementById('tab-'+name).style.display='';
  document.querySelectorAll('nav button').forEach(b=>b.classList.remove('active'));
  btn.classList.add('active');
  stopTabRefresh();
  if(name==='devices') startTabRefresh(5000, refreshDevicesTab);
  else if(name==='inkjoy') startTabRefresh(5000, refreshInkjoyTab);
  else if(name==='samsung') startTabRefresh(60000, refreshSamsungTab);
  else if(name==='album') loadImages();
  else if(name==='overlays') loadOverlaysTab();
  else if(name==='color') loadColorTab();
  if(name==='mqtt') startMQTTLogPoll();
  else stopMQTTLogPoll();
}

let mqttLogTimer=null;
const mqttExpanded=new Set();
function mqttEntryKey(e){ return e.time+'\0'+e.topic+'\0'+e.dir; }
function startMQTTLogPoll(){
  if(mqttLogTimer) return;
  loadMQTTLogs();
  mqttLogTimer=setInterval(loadMQTTLogs, 1000);
}
function stopMQTTLogPoll(){
  if(mqttLogTimer){ clearInterval(mqttLogTimer); mqttLogTimer=null; }
}
function esc(s){
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}
function mqttBodyLong(body){
  return body && (body.length>180 || body.split('\n').length>6);
}
function renderMQTTEntries(entries){
  if(!entries||!entries.length) return '<p class="mqtt-empty">No messages yet.</p>';
  const list=entries.slice().reverse();
  return list.map(e=>{
    const note=e.note?'<span class="note">'+esc(e.note)+'</span>':'';
    const action=e.action?'<span class="action">'+esc(e.action)+'</span>':'';
    const longBody=mqttBodyLong(e.body);
    const key=mqttEntryKey(e);
    const expanded=mqttExpanded.has(key);
    const cls='mqtt-entry'+(longBody?' clampable':'')+(expanded?' expanded':'');
    const body=e.body?'<pre>'+esc(e.body)+'</pre>':'';
    const hint=longBody&&!expanded?'<div class="mqtt-expand-hint">Click to expand</div>':'';
    const dataKey=longBody?' data-key="'+encodeURIComponent(key)+'" onclick="toggleMQTTEntry(this)"':'';
    return '<div class="'+cls+'"'+dataKey+'><div class="meta"><span class="time">'+esc(e.time)+'</span><span class="dir">'+esc(e.dir)+'</span>'+action+note+'</div><div class="topic">'+esc(e.topic)+'</div>'+body+hint+'</div>';
  }).join('');
}
function toggleMQTTEntry(el){
  const key=decodeURIComponent(el.dataset.key);
  if(mqttExpanded.has(key)) mqttExpanded.delete(key);
  else mqttExpanded.add(key);
  el.classList.toggle('expanded');
  const hint=el.querySelector('.mqtt-expand-hint');
  if(hint) hint.textContent=el.classList.contains('expanded')?'Click to collapse':'Click to expand';
}
function setMQTTColumn(id,html){
  const el=document.getElementById(id);
  const y=el.scrollTop;
  el.innerHTML=html;
  el.scrollTop=y;
}
async function loadMQTTLogs(){
  try{
    const r=await fetch('/api/mqtt/logs');
    if(!r.ok) return;
    const data=await r.json();
    setMQTTColumn('mqtt-local',renderMQTTEntries(data.local));
    setMQTTColumn('mqtt-upstream',renderMQTTEntries(data.upstream));
  }catch(_){}
}

let tabRefreshTimer=null;
let tabRefreshFn=null;
let tabRefreshIntervalMs=0;
function startTabRefresh(intervalMs, fn){
  tabRefreshFn=fn;
  tabRefreshIntervalMs=intervalMs;
  resumeTabRefresh();
}
function resumeTabRefresh(){
  if(tabRefreshTimer){ clearInterval(tabRefreshTimer); tabRefreshTimer=null; }
  if(!tabRefreshFn||document.hidden) return;
  tabRefreshFn();
  tabRefreshTimer=setInterval(tabRefreshFn, tabRefreshIntervalMs);
}
function stopTabRefresh(){
  if(tabRefreshTimer){ clearInterval(tabRefreshTimer); tabRefreshTimer=null; }
  tabRefreshFn=null;
  tabRefreshIntervalMs=0;
}
document.addEventListener('visibilitychange',()=>{
  if(document.hidden){
    if(tabRefreshTimer){ clearInterval(tabRefreshTimer); tabRefreshTimer=null; }
  }else{
    resumeTabRefresh();
  }
});

async function refreshDevicesTab(){
  await loadDevicesInner();
}

async function refreshInkjoyTab(){
  await loadDevicesInner();
  loadIJFrames();
}

async function refreshSamsungTab(){
  await loadSamsungFrames();
}

async function ensureDevicesForSend(){
  await loadDevicesInner();
}

async function loadDevices(){
  await loadDevicesInner();
  if(activeTab==='inkjoy') loadIJFrames();
  else if(activeTab==='samsung') await loadSamsungFrames();
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

function escHtml(s){
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function albumPhotoBaseHeightPx(){
  return window.matchMedia('(max-width:640px)').matches ? 120 : 148;
}

function albumPhotoMinDimPx(){
  return window.matchMedia('(max-width:640px)').matches ? 140 : 168;
}

function albumPhotoSize(img){
  const iw=img.width|0, ih=img.height|0;
  const baseH=albumPhotoBaseHeightPx();
  const minDim=albumPhotoMinDimPx();
  let photoW, photoH;
  if(iw>0&&ih>0){
    if(ih>iw){
      // Portrait: wide enough for buttons, height follows the photo.
      photoW=minDim;
      photoH=Math.round(photoW*ih/iw);
    }else{
      photoH=baseH;
      photoW=Math.round(photoH*iw/ih);
      const smallest=Math.min(photoW, photoH);
      if(smallest<minDim){
        const scale=minDim/smallest;
        photoW=Math.round(photoW*scale);
        photoH=Math.round(photoH*scale);
      }
    }
  }else{
    photoW=Math.round(baseH*4/3);
    photoH=baseH;
    const smallest=Math.min(photoW, photoH);
    if(smallest<minDim){
      const scale=minDim/smallest;
      photoW=Math.round(photoW*scale);
      photoH=Math.round(photoH*scale);
    }
  }
  return {w:photoW, h:photoH, portrait:iw>0&&ih>iw};
}

function albumStackVars(i){
  const rot=((i*41)%11)-5;
  const dy=(((i*29)%9)-4)*1.6;
  return '--rot:'+rot+'deg;--dy:'+dy+'px;--z:'+(i+1);
}

let albumSelectedId=null;
let albumCaptionEditingId=null;

function albumCardInForeground(card){
  if(!card) return false;
  if(card.classList.contains('album-outer-selected')) return true;
  return window.matchMedia('(hover:hover)').matches&&card.matches(':hover');
}

function setAlbumSelected(id){
  const next=id||null;
  if(albumSelectedId===next) return;
  if(albumSelectedId){
    const prev=document.getElementById('card-'+albumSelectedId);
    if(prev) prev.classList.remove('album-outer-selected');
  }
  albumSelectedId=next;
  if(albumSelectedId){
    const el=document.getElementById('card-'+albumSelectedId);
    if(el) el.classList.add('album-outer-selected');
  }
}

function albumGridTap(e){
  if(e.target.closest('.album-btn')) return;
  const card=e.target.closest('.album-outer');
  if(!card) return;
  const imageId=card.id.slice(5);
  if(e.target.closest('.album-caption')){
    albumCaptionClick(e,imageId);
    return;
  }
  setAlbumSelected(imageId);
}

function albumCaptionClick(e,imageId){
  e.stopPropagation();
  if(albumCaptionEditingId) return;
  const card=document.getElementById('card-'+imageId);
  if(!albumCardInForeground(card)) return;
  startAlbumCaptionEdit(imageId);
}

function updateAlbumCaptionElement(imageId,name){
  const cap=document.querySelector('#card-'+imageId+' .album-caption');
  if(!cap) return;
  cap.textContent=name;
  cap.title=name;
}

function syncOverlayPreviewImageOption(imageId,name){
  const sel=document.getElementById('ovl-preview-image');
  if(!sel) return;
  const opt=sel.querySelector('option[value="'+imageId+'"]');
  if(opt) opt.textContent=name||imageId;
}

async function saveAlbumCaption(imageId,name){
  name=String(name||'').trim();
  const img=images.find(i=>i.id===imageId);
  if(!name){
    if(img) updateAlbumCaptionElement(imageId,img.name);
    return;
  }
  if(img&&img.name===name){
    updateAlbumCaptionElement(imageId,name);
    return;
  }
  try{
    const r=await fetch('/api/images/'+imageId,{
      method:'PATCH',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({name})
    });
    if(!r.ok) throw new Error(await r.text());
    const meta=await r.json();
    if(img) img.name=meta.name;
    updateAlbumCaptionElement(imageId,meta.name);
    syncOverlayPreviewImageOption(imageId,meta.name);
  }catch(err){
    alert('Rename failed: '+err.message);
    if(img) updateAlbumCaptionElement(imageId,img.name);
  }
}

function startAlbumCaptionEdit(imageId){
  const cap=document.querySelector('#card-'+imageId+' .album-caption');
  if(!cap||albumCaptionEditingId) return;
  const img=images.find(i=>i.id===imageId);
  albumCaptionEditingId=imageId;
  cap.contentEditable='true';
  cap.classList.add('album-caption-editing');
  cap.textContent=img?img.name:cap.textContent;
  cap.focus();
  const range=document.createRange();
  range.selectNodeContents(cap);
  range.collapse(false);
  const sel=window.getSelection();
  sel.removeAllRanges();
  sel.addRange(range);
  function finish(save){
    cap.contentEditable='false';
    cap.classList.remove('album-caption-editing');
    cap.removeEventListener('blur',onBlur);
    cap.removeEventListener('keydown',onKey);
    cap.removeEventListener('paste',onPaste);
    albumCaptionEditingId=null;
    if(save) saveAlbumCaption(imageId,cap.textContent);
    else if(img) updateAlbumCaptionElement(imageId,img.name);
  }
  function onBlur(){ finish(true); }
  function onKey(ev){
    if(ev.key==='Enter'){ ev.preventDefault(); finish(true); }
    if(ev.key==='Escape'){ ev.preventDefault(); finish(false); }
  }
  function onPaste(ev){
    ev.preventDefault();
    const text=(ev.clipboardData||window.clipboardData).getData('text').replace(/\r?\n/g,' ').trim();
    document.execCommand('insertText',false,text);
  }
  cap.addEventListener('blur',onBlur);
  cap.addEventListener('keydown',onKey);
  cap.addEventListener('paste',onPaste);
}

function initAlbumGridTap(){
  const grid=document.getElementById('image-grid');
  if(!grid||grid.dataset.albumTap) return;
  grid.dataset.albumTap='1';
  grid.addEventListener('click',albumGridTap);
}

async function loadImages(){
  const r=await fetch('/api/images'); images=await r.json();
  images.forEach(img=>{ imageCropsCache[img.id]=img.crops||{}; });
  const el=document.getElementById('image-grid');
  if(!images||!images.length){
    albumSelectedId=null;
    el.innerHTML='<p class="album-empty">No images uploaded yet.</p>';
    return;
  }
  el.innerHTML=images.map((img,i)=>{
    const name=escHtml(img.name);
    const sz=albumPhotoSize(img);
    return '<div class="album-outer'+(sz.portrait?' album-outer-portrait':'')+'" id="card-'+img.id+'" style="'+albumStackVars(i)+'">'+
      '<div class="album-print" style="--photo-w:'+sz.w+'px;--photo-h:'+sz.h+'px">'+
        '<div class="album-img">'+
          '<img src="/images/'+img.id+'/preview" alt="'+name+'" loading="lazy">'+
          '<div class="album-menu">'+
            '<button type="button" class="album-btn" onclick="event.stopPropagation();openCrop(\''+img.id+'\')">Frame</button>'+
            '<button type="button" class="album-btn" id="send-btn-'+img.id+'" onclick="sendImageToFrame(event,\''+img.id+'\')">Send</button>'+
            '<button type="button" class="album-btn album-btn-delete" onclick="event.stopPropagation();deleteImg(\''+img.id+'\')">&times;</button>'+
          '</div>'+
        '</div>'+
        '<div class="album-caption" title="'+name+'">'+name+'</div>'+
      '</div></div>';
  }).join('');
  initAlbumGridTap();
  if(albumSelectedId){
    if(images.some(img=>img.id===albumSelectedId)){
      const el=document.getElementById('card-'+albumSelectedId);
      if(el) el.classList.add('album-outer-selected');
    }else{
      albumSelectedId=null;
    }
  }
}

const sendPicker=document.getElementById('send-picker');
let sendPickerImageId=null;
function closePickers(){
  sendPicker.style.display='none';
  sendPicker.style.maxHeight='';
  sendPicker.style.overflowY='';
  sendPickerImageId=null;
}
function positionSendPicker(anchor, opts){
  opts=opts||{};
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
  let top;
  if(opts.preferAbove && anchor.top-4-pickerH>=margin){
    top=anchor.top-pickerH-4;
  }else{
    top=anchor.bottom+4;
    if(top+pickerH>viewH-margin && anchor.top-4-pickerH>=margin){
      top=anchor.top-pickerH-4;
    }
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

function samsungDeepSleep(d,rec,s){
  return !!((s&&s.deep_sleep_active)||(rec&&rec.deep_sleep_active)||(d&&d.deep_sleep_active)||
    (d&&d.last_action==='mdc_deep_sleep')||(rec&&rec.last_action==='mdc_deep_sleep'));
}

const SAMSUNG_MANUAL_WAKE_HINT_MS=20000;
const SAMSUNG_MANUAL_WAKE_MSG='Press power button on frame…';
const SAMSUNG_DEEP_SLEEP_WAKE_MSG='Press power button on frame for 3s to wake frame';

function samsungDeepSleepWakeHint(d,rec,s){
  return samsungDeepSleep(d,rec,s)?SAMSUNG_DEEP_SLEEP_WAKE_MSG:null;
}

async function postDisplay(deviceId,imageId,onLongWait){
  const hintTimer=setTimeout(()=>{if(onLongWait) onLongWait();},SAMSUNG_MANUAL_WAKE_HINT_MS);
  try{
    const r=await fetch('/api/devices/'+encodeURIComponent(deviceId)+'/display',{
      method:'POST',headers:{'Content-Type':'application/json'},
      body:JSON.stringify({image_id:imageId})
    });
    if(!r.ok) throw new Error(await r.text());
    const j=await r.json();
    return j.send_id||'';
  }finally{
    clearTimeout(hintTimer);
  }
}

const SEND_DELIVERY_TIMEOUT_MS=180000;

async function waitSendDelivered(sendId,onTick){
  if(!sendId) return false;
  const deadline=Date.now()+SEND_DELIVERY_TIMEOUT_MS;
  while(Date.now()<deadline){
    const waitSec=Math.min(30, Math.max(1, Math.ceil((deadline-Date.now())/1000)));
    const r=await fetch('/api/send/'+encodeURIComponent(sendId)+'?wait='+waitSec);
    if(r.status===404) return false;
    if(!r.ok) throw new Error(await r.text());
    const j=await r.json();
    if(j.status==='delivered') return true;
    if(j.status==='failed') throw new Error('Frame did not finish downloading');
    if(onTick) onTick(j);
  }
  return false;
}

async function doSend(imageId, deviceId, feedbackBtn){
  closePickers();
  const orig=feedbackBtn?feedbackBtn.textContent:'';
  const dev=devices.find(d=>d.id===deviceId);
  const deepHint=samsungDeepSleepWakeHint(dev);
  try{
    if(feedbackBtn){
      feedbackBtn.disabled=true;
      feedbackBtn.textContent=deepHint||'Sending…';
    }else if(deepHint){
      alert(deepHint);
    }
    const sendId=await postDisplay(deviceId,imageId,()=>{
      if(dev&&dev.type==='samsung'&&feedbackBtn&&!deepHint){
        feedbackBtn.textContent=SAMSUNG_MANUAL_WAKE_MSG;
      }
    });
    if(sendId&&feedbackBtn) feedbackBtn.textContent='Downloading…';
    const delivered=sendId?await waitSendDelivered(sendId):false;
    if(feedbackBtn){
      feedbackBtn.textContent=delivered?'✓ Sent':(sendId?'Sent (unconfirmed)':'✓ Sent');
      setTimeout(()=>{feedbackBtn.textContent=orig;feedbackBtn.disabled=false;},2000);
    }
  }catch(e){
    alert('Send failed: '+e.message);
    if(feedbackBtn){feedbackBtn.textContent=orig;feedbackBtn.disabled=false;}
  }
}

function sendImageToFrame(evt, imageId){
  if(evt) evt.stopPropagation();
  const btn=document.getElementById('send-btn-'+imageId);
  ensureDevicesForSend().then(()=>{
    const frameDevices=devices.filter(d=>d.type==='inkjoy'||d.type==='samsung');
    if(!frameDevices.length){alert('No frames registered — check Devices tab or Discover.');return;}
    if(frameDevices.length===1){doSend(imageId,frameDevices[0].id,btn);return;}
    if(sendPickerImageId===imageId){closePickers();return;}
    sendPickerImageId=imageId;
    sendPicker.innerHTML=frameDevices.map(d=>{
      const label=d.name||(d.mac?d.mac:d.ip)||d.id;
      return '<div class="send-picker-item" onclick="doSend(\''+imageId+'\',\''+d.id+'\',document.getElementById(\'send-btn-'+imageId+'\'))">'+label+'</div>';
    }).join('');
    positionSendPicker(btn.getBoundingClientRect(),{preferAbove:!!btn.closest('.album-menu')});
  });
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
  if(albumSelectedId===id) albumSelectedId=null;
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
let ijDevices=[], ijCurrentId=null, ijPreviewKey=null;

function ijPreviewSpec(d){
  if(d.last_image_id) return 'album:'+d.last_image_id;
  if(d.display_preview_at) return 'ext:'+d.display_preview_at;
  return '';
}

function ijPreviewURL(d){
  if(d.last_image_id){
    const q=new URLSearchParams();
    if(d.portrait) q.set('portrait','1');
    if(d.last_overlay_hash) q.set('overlay', d.last_overlay_hash);
    const qs=q.toString();
    return '/images/'+encodeURIComponent(d.last_image_id)+'/frame-preview'+(qs?'?'+qs:'');
  }
  if(d.display_preview_at){
    return '/api/devices/'+encodeURIComponent(d.id)+'/display-preview?t='+encodeURIComponent(d.display_preview_at);
  }
  return '';
}

function ijFrameListSig(inkjoy){
  return inkjoy.map(d=>d.id+'\x1f'+(d.name||d.mac||d.id)).join('\n');
}

function patchIJFrameListItem(d){
  const li=document.getElementById('ijli-'+d.id);
  if(!li) return;
  li.classList.toggle('selected', d.id===ijCurrentId);
  const dot=li.querySelector('.dot');
  if(dot) dot.className='dot '+(d.connected?'online':'offline');
  const label=d.name||d.mac||d.id;
  const title=li.querySelector('.ij-frame-label');
  if(title) title.textContent=label;
  let bat=li.querySelector('.ij-frame-battery');
  if(d.battery){
    if(!bat){
      bat=document.createElement('span');
      bat.className='ij-frame-battery';
      bat.style.cssText='margin-left:auto;font-size:.8rem;color:#666';
      li.appendChild(bat);
    }
    bat.textContent='🔋'+d.battery+'%';
  }else if(bat){
    bat.remove();
  }
}

function loadIJFrames(){
  const inkjoy=devices.filter(d=>d.type==='inkjoy');
  ijDevices=inkjoy;
  const el=document.getElementById('inkjoy-frame-list');
  if(!inkjoy.length){
    el.innerHTML='<p style="color:#888;font-size:.9rem">No InkJoy frames yet.</p>';
    el.dataset.ijSig='';
    return;
  }
  const sig=ijFrameListSig(inkjoy);
  if(el.dataset.ijSig!==sig){
    el.dataset.ijSig=sig;
    el.innerHTML=inkjoy.map(d=>{
      const label=d.name||d.mac||d.id;
      const sel=d.id===ijCurrentId?' selected':'';
      const online=d.connected;
      return '<div class="frame-list-item'+sel+'" onclick="openIJFrame(\''+d.id+'\')" id="ijli-'+d.id+'">'+
        '<div class="dot '+(online?'online':'offline')+'"></div>'+
        '<span class="ij-frame-label" style="font-weight:500">'+label+'</span>'+
        (d.battery?'<span class="ij-frame-battery" style="margin-left:auto;font-size:.8rem;color:#666">🔋'+d.battery+'%</span>':'')+
        '</div>';
    }).join('');
  }else{
    inkjoy.forEach(patchIJFrameListItem);
  }
  // On periodic refresh, only update status/info — never form inputs.
  if(ijCurrentId){
    const d=ijDevices.find(x=>x.id===ijCurrentId);
    if(d) updateIJEditorStatus(d);
  }
}

function openIJFrame(id){
  ijCurrentId=id;
  ijPreviewKey=null;
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
  const key=ijPreviewSpec(d);
  const url=ijPreviewURL(d);
  if(!key){
    if(ijPreviewKey!==null){
      ijPreviewKey=null;
      li.innerHTML='<p style="color:#888;font-size:.9rem">Nothing sent yet.</p>';
    }
    return;
  }
  const img=document.getElementById('ij-preview');
  if(!img||img.parentElement!==li){
    li.innerHTML='<img class="last-image-preview" id="ij-preview" src="'+url+'" alt="currently displayed">';
    ijPreviewKey=key;
  }else if(key!==ijPreviewKey){
    ijPreviewKey=key;
    refreshIJPreview(url);
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
      ? (d.connected?'<span class="badge online">active</span>':'<span class="badge offline">'+samsungOfflineLabel(d)+'</span>')
      : (d.connected?'<span class="badge online">online</span>':'<span class="badge offline">offline</span>');
    const meta=type==='inkjoy'
      ? ((d.firmware?'fw '+d.firmware+' ':'')+(d.battery?'🔋'+d.battery+'% ':'')+(d.rssi?'📶'+d.rssi+'dBm ':''))
      : (d.ip?d.ip+' ':'')+(d.battery?'🔋'+d.battery+'% ':'')+(d.power_source?('<span style="color:#666">'+d.power_source+' </span>'):'')+(d.display_crop_format?('<span style="color:#666">'+d.display_crop_format+(d.display_width?(' · '+d.display_width+'×'+d.display_height):'')+'</span> '):'')+(d.usn?'<span style="color:#888;font-size:.8rem">'+d.usn.split('::')[0]+'</span>':'');
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

loadImages();
showTab('devices', document.querySelector('nav button.active'));

// ── Overlays tab ────────────────────────────────────────────────────────────
let overlayConfig=null;
let overlayPreviewTimer=null;

function debouncedOverlayPreview(){
  clearTimeout(overlayPreviewTimer);
  overlayPreviewTimer=setTimeout(()=>{
    refreshOverlayMetrics();
    refreshOverlayPreview();
  }, 400);
}

function renderOverlayMetrics(m){
  const el=document.getElementById('ovl-template-metrics');
  if(!el) return;
  if(m.error){
    el.innerHTML='<p style="color:#c00;margin:0">'+esc(m.error)+'</p>';
    return;
  }
  const lines=(m.lines||[]).map(ln=>
    '<div style="margin:.35rem 0;padding:.4rem .55rem;background:#f6f7f9;border:1px solid #e8eaed;border-radius:4px;font-family:ui-monospace,Menlo,monospace;font-size:.75rem;line-height:1.35">'+
    '<div style="color:#666;margin-bottom:.15rem">Line '+ln.index+' · '+ln.font_size+'px · ~'+ln.width_px+' px wide · +'+ln.step_px+' px tall</div>'+
    '<div style="color:#222">'+esc(ln.text)+'</div></div>'
  ).join('');
  el.innerHTML=
    '<div style="margin-bottom:.35rem;color:#666">Rendered lines (approximate, using live weather)</div>'+
    lines+
    '<div style="margin-top:.55rem;padding:.45rem .55rem;background:#fff;border:1px dashed #ccc;border-radius:4px;line-height:1.5;color:#444">'+
    '<div><b>Content</b> (max line × sum of line heights): ~'+m.content.width_px+' × '+m.content.height_px+' px</div>'+
    (m.style==='outline'
      ? '<div><b>Style</b> bordered text (no background panel)</div>'
      : '<div><b>Box</b> (+'+m.box.border_px+' px border each side): '+m.box.width_px+' × '+m.box.height_px+' px on all frames</div>')+
    '</div>';
}

async function refreshOverlayMetrics(){
  const el=document.getElementById('ovl-template-metrics');
  if(!el) return;
  try{
    const r=await fetch('/api/overlay/metrics',{
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({config:overlayFormValues()})
    });
    if(!r.ok) throw new Error(await r.text());
    renderOverlayMetrics(await r.json());
  }catch(e){
    el.innerHTML='<p style="color:#c00;margin:0">Size estimate failed: '+esc(e.message)+'</p>';
  }
}

async function loadOverlayConfig(){
  const r=await fetch('/api/overlay');
  if(!r.ok) throw new Error(await r.text());
  overlayConfig=await r.json();
}

function applyOverlayForm(cfg){
  document.getElementById('ovl-enabled').checked=!!cfg.enabled;
  document.getElementById('ovl-location').value=cfg.location||'';
  document.getElementById('ovl-lat').value=cfg.latitude||'';
  document.getElementById('ovl-lon').value=cfg.longitude||'';
  document.getElementById('ovl-timezone').value=cfg.timezone||'';
  document.getElementById('ovl-template').value=cfg.template||'';
  document.getElementById('ovl-weather-style').value=cfg.weather_style||'box';
  document.getElementById('ovl-fahrenheit').checked=cfg.use_fahrenheit!==false;
  document.getElementById('ovl-date-style').value=String(cfg.date_style||1);
  document.getElementById('ovl-show-photo-name').checked=!!cfg.show_photo_name;
  document.getElementById('ovl-photo-name-position').value=cfg.photo_name_position||'bottom_right';
}

function overlayFormValues(){
  const lat=parseFloat(document.getElementById('ovl-lat').value);
  const lon=parseFloat(document.getElementById('ovl-lon').value);
  return {
    enabled: document.getElementById('ovl-enabled').checked,
    layout: 'bottom_bar',
    location: document.getElementById('ovl-location').value.trim(),
    latitude: Number.isFinite(lat)?lat:0,
    longitude: Number.isFinite(lon)?lon:0,
    timezone: document.getElementById('ovl-timezone').value.trim(),
    template: document.getElementById('ovl-template').value,
    weather_style: document.getElementById('ovl-weather-style').value,
    use_fahrenheit: document.getElementById('ovl-fahrenheit').checked,
    date_style: parseInt(document.getElementById('ovl-date-style').value,10)||1,
    show_photo_name: document.getElementById('ovl-show-photo-name').checked,
    photo_name_position: document.getElementById('ovl-photo-name-position').value
  };
}

async function saveOverlayConfig(){
  const st=document.getElementById('ovl-save-status');
  st.textContent='Saving…';
  try{
    const r=await fetch('/api/overlay',{
      method:'PUT',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify(overlayFormValues())
    });
    if(!r.ok) throw new Error(await r.text());
    overlayConfig=overlayFormValues();
    st.style.color='#28a745';
    st.textContent='Saved.';
    refreshOverlayPreview();
    renderOverlaySendList();
  }catch(e){
    st.style.color='#dc3545';
    st.textContent='Save failed: '+e.message;
  }
}

function overlayPreviewImageId(){
  const sel=document.getElementById('ovl-preview-image');
  return sel&&sel.value?sel.value:'';
}

async function refreshOverlayPreview(){
  const wrap=document.getElementById('ovl-preview-wrap');
  const imageId=overlayPreviewImageId();
  if(!imageId){
    wrap.innerHTML='<p style="color:#888;font-size:.9rem">Upload images to the album first.</p>';
    return;
  }
  const portrait=document.getElementById('ovl-preview-portrait').checked;
  wrap.innerHTML='<p style="color:#888;font-size:.9rem">Loading preview…</p>';
  try{
    const r=await fetch('/api/overlay/preview',{
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({
        image_id:imageId,
        portrait:portrait,
        config:overlayFormValues()
      })
    });
    if(!r.ok) throw new Error(await r.text());
    const blob=await r.blob();
    const url=URL.createObjectURL(blob);
    wrap.innerHTML='<img class="last-image-preview" src="'+url+'" alt="overlay preview">';
  }catch(e){
    wrap.innerHTML='<p style="color:#c00;font-size:.9rem">Preview failed: '+esc(e.message)+'</p>';
  }
}

function overlaySendImageForDevice(d){
  return d.last_image_id||'';
}

function renderOverlaySendList(){
  const el=document.getElementById('ovl-send-list');
  const frames=devices.filter(d=>d.type==='inkjoy'||d.type==='samsung');
  if(!frames.length){
    el.innerHTML='<p style="color:#888;font-size:.9rem">No frames registered yet.</p>';
    return;
  }
  el.innerHTML=frames.map(d=>{
    const label=d.name||d.mac||d.id;
    const src=overlaySendImageForDevice(d);
    const hint=src?'current frame image':'no tracked image — send from Album first';
    return '<div style="display:flex;align-items:center;gap:.75rem;margin:.5rem 0;flex-wrap:wrap">'+
      '<span class="badge" style="background:#eee;color:#333">'+d.type+'</span>'+
      '<strong>'+esc(label)+'</strong>'+
      '<span style="color:#888;font-size:.85rem">'+esc(hint)+'</span>'+
      '<button class="btn btn-sm btn-primary" onclick="overlaySend(\''+d.id+'\')"'+(!src?' disabled':'')+'>Send with overlay</button>'+
      '</div>';
  }).join('');
}

async function overlaySend(deviceId){
  await loadDevicesInner();
  const d=devices.find(x=>x.id===deviceId);
  if(!d||!d.last_image_id){
    alert('No hub-tracked image on this frame. Send a photo from Album first (overlay will apply automatically when enabled).');
    return;
  }
  if(!overlayConfig||!overlayConfig.enabled){
    alert('Enable overlay in settings first.');
    return;
  }
  const btn=event&&event.target;
  const orig=btn?btn.textContent:'';
  if(btn){btn.disabled=true;btn.textContent='Sending…';}
  try{
    const r=await fetch('/api/overlay/send',{
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({device_id:deviceId,use_current:true})
    });
    if(!r.ok) throw new Error(await r.text());
    const j=await r.json();
    if(btn) btn.textContent='Downloading…';
    const delivered=j.send_id?await waitSendDelivered(j.send_id):false;
    if(btn){
      btn.textContent=delivered?'✓ Sent':'Sent (unconfirmed)';
      setTimeout(()=>{btn.textContent=orig;btn.disabled=false;},2000);
    }
    await loadDevicesInner();
    if(activeTab==='overlays') renderOverlaySendList();
  }catch(e){
    alert('Overlay send failed: '+e.message);
    if(btn){btn.textContent=orig;btn.disabled=false;}
  }
}

async function loadOverlaysTab(){
  try{
    if(!overlayConfig) await loadOverlayConfig();
    applyOverlayForm(overlayConfig||{});
    if(!images.length) await loadImages();
    const sel=document.getElementById('ovl-preview-image');
    sel.innerHTML=images.map(i=>'<option value="'+i.id+'">'+esc(i.name||i.id)+'</option>').join('');
    if(!sel.value&&images.length) sel.selectedIndex=0;
    renderOverlaySendList();
    refreshOverlayMetrics();
    refreshOverlayPreview();
  }catch(e){
    document.getElementById('ovl-save-status').textContent='Load failed: '+e.message;
  }
}

// ── Color tab ───────────────────────────────────────────────────────────────
let colorConfig=null, colorPresets=null;
const COLOR_PRESET_LABELS={
  calibrated:'Calibrated (on-panel P2)',
  legacy:'Legacy physical ink',
  srgb:'Pure sRGB primaries',
  reflection:'Reflection Spectra 6',
  custom:'Custom'
};
const COLOR_GROUPS=[
  {key:'inkjoy_display',presetId:'clr-inkjoy-display-preset',swatchId:'clr-inkjoy-display-swatches',cfgPreset:'inkjoy_display_preset',cfgRGB:'inkjoy_display'},
  {key:'samsung_display',presetId:'clr-samsung-display-preset',swatchId:'clr-samsung-display-swatches',cfgPreset:'samsung_display_preset',cfgRGB:'samsung_display'},
  {key:'samsung_send',presetId:'clr-samsung-send-preset',swatchId:'clr-samsung-send-swatches',cfgPreset:'samsung_send_preset',cfgRGB:'samsung_send'}
];

function fillColorPresetSelect(sel){
  sel.innerHTML=Object.entries(COLOR_PRESET_LABELS).map(([v,l])=>'<option value="'+v+'">'+l+'</option>').join('');
}

function renderColorSwatches(groupKey, containerId){
  const el=document.getElementById(containerId);
  const names=colorPresets&&colorPresets.names?colorPresets.names:['black','white','yellow','red','blue','green'];
  el.innerHTML='<table style="width:100%;font-size:.8rem;border-collapse:collapse;margin:.35rem 0 .75rem"><thead><tr><th style="text-align:left;padding:.2rem 0">Ink</th><th>R</th><th>G</th><th>B</th><th></th></tr></thead><tbody>'+
    names.map((n,i)=>{
      const id=groupKey+'-'+i;
      return '<tr><td style="padding:.2rem .35rem .2rem 0">'+n+'</td>'+
        '<td><input id="clr-'+id+'-r" type="number" min="0" max="255" style="width:3.2rem;padding:.2rem;border:1px solid #ccc;border-radius:3px"></td>'+
        '<td><input id="clr-'+id+'-g" type="number" min="0" max="255" style="width:3.2rem;padding:.2rem;border:1px solid #ccc;border-radius:3px"></td>'+
        '<td><input id="clr-'+id+'-b" type="number" min="0" max="255" style="width:3.2rem;padding:.2rem;border:1px solid #ccc;border-radius:3px"></td>'+
        '<td><span id="clr-'+id+'-sw" style="display:inline-block;width:1.4rem;height:1.1rem;border:1px solid #ccc;border-radius:3px;vertical-align:middle"></span></td></tr>';
    }).join('')+'</tbody></table>';
}

function readPaletteGroup(groupKey){
  const names=colorPresets&&colorPresets.names?colorPresets.names:[];
  const out=[];
  for(let i=0;i<6;i++){
    const r=parseInt(document.getElementById('clr-'+groupKey+'-'+i+'-r').value,10)||0;
    const g=parseInt(document.getElementById('clr-'+groupKey+'-'+i+'-g').value,10)||0;
    const b=parseInt(document.getElementById('clr-'+groupKey+'-'+i+'-b').value,10)||0;
    out.push([Math.max(0,Math.min(255,r)),Math.max(0,Math.min(255,g)),Math.max(0,Math.min(255,b))]);
    const sw=document.getElementById('clr-'+groupKey+'-'+i+'-sw');
    if(sw) sw.style.background='rgb('+out[i].join(',')+')';
  }
  return out;
}

function writePaletteGroup(groupKey, palette){
  if(!palette) return;
  for(let i=0;i<6;i++){
    const rgb=palette[i]||[0,0,0];
    const r=document.getElementById('clr-'+groupKey+'-'+i+'-r');
    const g=document.getElementById('clr-'+groupKey+'-'+i+'-g');
    const b=document.getElementById('clr-'+groupKey+'-'+i+'-b');
    if(r){r.value=rgb[0];g.value=rgb[1];b.value=rgb[2];}
  }
  readPaletteGroup(groupKey);
}

function onColorPresetChange(groupKey){
  const g=COLOR_GROUPS.find(x=>x.key===groupKey);
  if(!g||!colorPresets) return;
  const preset=document.getElementById(g.presetId).value;
  if(preset!=='custom'){
    writePaletteGroup(groupKey, colorPresets.presets[groupKey][preset]);
  }
}

function applyColorForm(cfg){
  document.getElementById('clr-lab-chroma').checked=!!cfg.lab_chroma_enabled;
  const chromaStrength=cfg.lab_chroma_strength||1;
  document.getElementById('clr-lab-chroma-strength').value=chromaStrength;
  document.getElementById('clr-lab-chroma-strength-val').textContent=String(chromaStrength);
  document.getElementById('clr-lab-highlight').checked=!!cfg.lab_highlight_enabled;
  const highlightStrength=cfg.lab_highlight_strength||1;
  document.getElementById('clr-lab-highlight-strength').value=highlightStrength;
  document.getElementById('clr-lab-highlight-strength-val').textContent=String(highlightStrength);
  document.getElementById('clr-lab-shadow').checked=!!cfg.lab_shadow_enabled;
  const shadowStrength=cfg.lab_shadow_strength||1;
  document.getElementById('clr-lab-shadow-strength').value=shadowStrength;
  document.getElementById('clr-lab-shadow-strength-val').textContent=String(shadowStrength);
  for(const g of COLOR_GROUPS){
    document.getElementById(g.presetId).value=cfg[g.cfgPreset]||'calibrated';
    const preset=cfg[g.cfgPreset]||'calibrated';
    const pal=preset==='custom'?cfg[g.cfgRGB]:(colorPresets&&colorPresets.presets[g.key][preset]);
    writePaletteGroup(g.key, pal);
  }
}

function colorFormValues(){
  const out={
    lab_chroma_enabled:document.getElementById('clr-lab-chroma').checked,
    lab_chroma_strength:parseFloat(document.getElementById('clr-lab-chroma-strength').value)||1,
    lab_highlight_enabled:document.getElementById('clr-lab-highlight').checked,
    lab_highlight_strength:parseFloat(document.getElementById('clr-lab-highlight-strength').value)||1,
    lab_shadow_enabled:document.getElementById('clr-lab-shadow').checked,
    lab_shadow_strength:parseFloat(document.getElementById('clr-lab-shadow-strength').value)||1,
    inkjoy_display_preset:document.getElementById('clr-inkjoy-display-preset').value,
    samsung_display_preset:document.getElementById('clr-samsung-display-preset').value,
    samsung_send_preset:document.getElementById('clr-samsung-send-preset').value
  };
  for(const g of COLOR_GROUPS){
    out[g.cfgRGB]=readPaletteGroup(g.key);
    if(document.getElementById(g.presetId).value==='custom'){
      out[g.cfgPreset]='custom';
    }
  }
  return out;
}

async function loadColorConfig(){
  const r=await fetch('/api/color');
  if(!r.ok) throw new Error(await r.text());
  colorConfig=await r.json();
}

async function loadColorPresets(){
  const r=await fetch('/api/color/presets');
  if(!r.ok) throw new Error(await r.text());
  colorPresets=await r.json();
}

async function loadColorTab(){
  try{
    if(!colorPresets) await loadColorPresets();
    for(const g of COLOR_GROUPS){
      fillColorPresetSelect(document.getElementById(g.presetId));
      renderColorSwatches(g.key, g.swatchId);
    }
    if(!colorConfig) await loadColorConfig();
    applyColorForm(colorConfig||{});
  }catch(e){
    document.getElementById('clr-save-status').textContent='Load failed: '+e.message;
  }
}

async function saveColorConfig(){
  const st=document.getElementById('clr-save-status');
  st.textContent='Saving…';
  try{
    const body=colorFormValues();
    for(const g of COLOR_GROUPS){
      if(document.getElementById(g.presetId).value!=='custom'){
        body[g.cfgPreset]=document.getElementById(g.presetId).value;
      }else{
        body[g.cfgPreset]='custom';
      }
    }
    const r=await fetch('/api/color',{
      method:'PUT',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify(body)
    });
    if(!r.ok) throw new Error(await r.text());
    colorConfig=body;
    st.style.color='#28a745';
    st.textContent='Saved. Bin cache cleared — re-send images to apply.';
  }catch(e){
    st.style.color='#dc3545';
    st.textContent='Save failed: '+e.message;
  }
}

// ── Samsung tab ─────────────────────────────────────────────────────────────
let samsungFrames=[], samsungCurrentId=null, samsungStatusCache=null, samsungPreviewKey=null;

function samsungOfflineLabel(d,rec,s){
  return samsungDeepSleep(d,rec,s)?'deep sleep':'asleep';
}

function samsungFrameRecord(frameId){
  return samsungFrames.find(x=>x.id===frameId);
}

function samsungDeviceForFrame(frameId){
  const rec=samsungFrameRecord(frameId);
  if(rec&&rec.device_id) return devices.find(d=>d.id===rec.device_id);
  return devices.find(d=>d.type==='samsung'&&samsungFrameIDFromDevice(d)===frameId);
}

function samsungFrameIDFromDevice(d){
  if(d.mdc_mac){
    const m=d.mdc_mac.replace(/[:\-\.]/g,'').toUpperCase();
    if(/^[0-9A-F]{12}$/.test(m)) return m;
  }
  if(d.mac){
    const m=d.mac.replace(/[:\-\.]/g,'').toUpperCase();
    if(/^[0-9A-F]{12}$/.test(m)) return m;
  }
  if(d.ip) return d.ip.replace(/\./g,'-');
  const id=d.id||'';
  if(id.startsWith('samsung:')){
    const suffix=id.slice(8);
    const m=suffix.replace(/[:\-\.]/g,'').toUpperCase();
    if(/^[0-9A-F]{12}$/.test(m)) return m;
    return suffix.replace(/\./g,'-');
  }
  return id.replace(/\./g,'-');
}

function samsungFrameListSig(frames){
  return frames.map(f=>f.id+'\x1f'+(f.name||f.ip||f.id)).join('\n');
}

function patchSamsungFrameListItem(f){
  const li=document.getElementById('samli-'+f.id);
  if(!li) return;
  li.classList.toggle('selected', f.id===samsungCurrentId);
  const dot=li.querySelector('.dot');
  if(dot) dot.className='dot '+(f.connected?'online':'offline');
  const label=f.name||f.ip||f.id;
  const title=li.querySelector('.sam-frame-label');
  if(title) title.textContent=label;
  let bat=li.querySelector('.sam-frame-battery');
  if(f.battery){
    if(!bat){
      bat=document.createElement('span');
      bat.className='sam-frame-battery';
      bat.style.cssText='margin-left:auto;font-size:.8rem;color:#666';
      li.appendChild(bat);
    }
    bat.textContent='🔋'+f.battery+'%'+(f.battery_push_delta!=null&&f.battery_samples>=2?(f.battery_push_delta===0?'':' '+(f.battery_push_delta>0?'+':'')+f.battery_push_delta+'%'):'');
  }else if(bat){
    bat.remove();
  }
}

function samsungBatteryMeta(obj){
  const pct=obj&&(obj.battery||(obj.battery===0?0:null));
  if(pct==null) return '—';
  const src=obj.power_source;
  let s=pct+'%'+(src?' · '+src:'');
  const pushDelta=obj.battery_push_delta;
  if(pushDelta!=null&&obj.battery_samples>=2){
    s+=(pushDelta===0?' · unchanged since last push':(' · '+(pushDelta>0?'+':'')+pushDelta+'% since last push'));
  }else if(obj.battery_delta!=null&&obj.battery_samples>=2){
    const d=obj.battery_delta;
    s+=(d===0?' · unchanged since last reading':(' · '+(d>0?'+':'')+d+'% since last reading'));
  }
  if(obj.battery_samples>1) s+=' · '+obj.battery_samples+' readings';
  return s;
}

function samsungBatteryHistoryHTML(history){
  if(!history||!history.length) return '';
  return history.slice().reverse().map(h=>{
    const when=h.at?timeAgo(h.at):'—';
    const src=h.source==='pre_sleep'?'push':(h.source||'');
    return '<div style="font-size:.8rem;color:#666">'+when+' · '+h.percent+'%'+(h.power_source?' · '+h.power_source:'')+(src?' · '+src:'')+'</div>';
  }).join('');
}

function samsungPreviewSpec(s){
  if(s&&s.has_image&&!s.locked) return 'img:'+(s.etag||'');
  if(s&&s.locked) return 'locked';
  return 'empty';
}

function samsungPreviewEmptyHTML(s){
  return '<p style="color:#888;font-size:.9rem">'+(s&&s.locked?'Image locked.':'No image uploaded yet.')+'</p>';
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
    el.dataset.samsungSig='';
    return;
  }
  const sig=samsungFrameListSig(samsungFrames);
  if(el.dataset.samsungSig!==sig){
    el.dataset.samsungSig=sig;
    el.innerHTML=samsungFrames.map(f=>{
      const label=f.name||f.ip||f.id;
      const sel=f.id===samsungCurrentId?' selected':'';
      const online=f.connected;
      return '<div class="frame-list-item'+sel+'" onclick="openSamsungFrame(\''+f.id+'\')" id="samli-'+f.id+'">'+
        '<div class="dot '+(online?'online':'offline')+'"></div>'+
        '<span class="sam-frame-label" style="font-weight:500">'+label+'</span>'+
        (f.battery?'<span class="sam-frame-battery" style="margin-left:auto;font-size:.8rem;color:#666">🔋'+f.battery+'%</span>':'')+
        '</div>';
    }).join('');
  }else{
    samsungFrames.forEach(patchSamsungFrameListItem);
  }
  if(samsungCurrentId){
    const rec=samsungFrameRecord(samsungCurrentId);
    if(rec) updateSamsungEditorStatus(null,rec,samsungStatusCache);
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
  samsungPreviewKey=null;
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
  samsungLoadDailyRefresh();
}

function samsungInactiveScheduleEnabled(begin,end){
  if(!begin||!end) return false;
  return begin.slice(0,5)!==end.slice(0,5);
}

function renderSamsungEditor(d,s,rec){
  updateSamsungEditorStatus(d,rec,s);
  document.getElementById('samsung-name-input').value=(s&&s.name)||(rec&&rec.name)||(d&&d.name)||'';
  document.getElementById('samsung-wifi-mac').value=(s&&s.wifi_mac)||(rec&&rec.wifi_mac)||(d&&d.mdc_mac)||'';
  document.getElementById('samsung-poll').value=(s&&s.poll_interval_minutes)||60;
  document.getElementById('samsung-inactive-begin').value=(s&&s.inactive_begin)||'';
  document.getElementById('samsung-inactive-end').value=(s&&s.inactive_end)||'';
  const dr=document.getElementById('samsung-daily-refresh');
  if(dr) dr.value=(s&&s.daily_refresh_time)||'';
  const drs=document.getElementById('samsung-daily-refresh-status');
  if(drs) drs.textContent='';
  const ods=document.getElementById('samsung-overnight-deep-sleep');
  if(ods) ods.checked=(s&&s.overnight_deep_sleep!==false);
  const fmt=(s&&s.crop_format)||'16:9';
  document.getElementById('samsung-portrait').checked=(fmt==='9:16'||fmt==='3:4');
  document.getElementById('samsung-display-width').value=(s&&s.display_width)||'';
  document.getElementById('samsung-display-height').value=(s&&s.display_height)||'';
  document.getElementById('samsung-auto-sleep').checked=(s&&s.auto_sleep_after_push!==false);
  document.getElementById('samsung-sleep-delay').value=(s&&s.sleep_after_push_seconds)||15;
}

function updateSamsungEditorStatus(d,rec,s){
  const label=(s&&s.name)||(rec&&rec.name)||(d&&d.name)||(rec&&rec.ip)||(d&&d.ip)||samsungCurrentId||'';
  document.getElementById('samsung-frame-title').textContent=label;
  const online=!!((d&&d.connected)||(rec&&rec.connected));
  document.getElementById('samsung-dot').className='dot '+(online?'online':'offline');
  const badge=document.getElementById('samsung-status-badge');
  badge.className='badge '+(online?'online':'offline');
  badge.textContent=online?'active':samsungOfflineLabel(d,rec,s);
  const wakeBtn=document.getElementById('samsung-wake-btn');
  const sleepBtn=document.getElementById('samsung-sleep-btn');
  if(wakeBtn) wakeBtn.style.display=online?'none':'inline-flex';
  if(sleepBtn) sleepBtn.style.display=online?'inline-flex':'none';
  const lastSeen=(d&&d.last_seen)||(rec&&rec.last_seen);
  const ago=lastSeen?timeAgo(lastSeen):'—';
  const ip=(d&&d.ip)||(rec&&rec.ip)||'—';
  const lastAction=(d&&d.last_action)||(rec&&rec.last_action)||'—';
  const batteryVal=(d&&d.battery)||(rec&&rec.battery);
  const batteryObj={
    battery:batteryVal,
    power_source:(d&&d.power_source)||(rec&&rec.power_source),
    battery_delta:(d&&d.battery_delta)!=null?d.battery_delta:(rec&&rec.battery_delta),
    battery_push_delta:(d&&d.battery_push_delta)!=null?d.battery_push_delta:(rec&&rec.battery_push_delta),
    battery_samples:(d&&d.battery_samples)||(rec&&rec.battery_samples)||0
  };
  const history=(d&&d.battery_history)||(rec&&rec.battery_history);
  document.getElementById('samsung-info').innerHTML=
    '<span class="label">Frame ID</span><span style="font-family:monospace">'+samsungCurrentId+'</span>'+
    '<span class="label">IP</span><span>'+ip+'</span>'+
    '<span class="label">Battery</span><span>'+samsungBatteryMeta(batteryObj)+'</span>'+
    (history&&history.length?('<span class="label">History</span><span>'+samsungBatteryHistoryHTML(history)+'</span>'):'')+
    '<span class="label">Crop</span><span>'+((s&&s.crop_format)||(rec&&rec.crop_format)||'—')+((s&&s.display_width)?(' · '+s.display_width+'×'+s.display_height):'')+'</span>'+
    '<span class="label">Poll</span><span>'+((s&&s.poll_interval_minutes)?(s.poll_interval_minutes+' min'):((rec&&rec.poll_interval_minutes)?(rec.poll_interval_minutes+' min'):'—'))+'</span>'+
    (s&&s.daily_refresh_time?('<span class="label">Daily refresh</span><span>'+s.daily_refresh_time+'</span>'):'')+
    '<span class="label">Last seen</span><span>'+ago+'</span>'+
    '<span class="label">Last action</span><span>'+lastAction+'</span>';
  const wrap=document.getElementById('samsung-preview-wrap');
  const key=samsungPreviewSpec(s);
  if(key.startsWith('img:')){
    const url='/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/preview';
    const img=document.getElementById('samsung-preview');
    if(!img||img.parentElement!==wrap){
      wrap.innerHTML='<img class="last-image-preview" id="samsung-preview" src="'+url+'" alt="current image">';
      samsungPreviewKey=key;
    }else if(key!==samsungPreviewKey){
      samsungPreviewKey=key;
      refreshSamsungPreview();
    }
  }else if(key!==samsungPreviewKey){
    samsungPreviewKey=key;
    wrap.innerHTML=samsungPreviewEmptyHTML(s);
  }
  const st=document.getElementById('samsung-status');
  if(s){
    st.textContent=(s.has_image?'Image etag '+s.etag:'No image yet')+(s.locked?' (locked)':'');
    if(samsungInactiveScheduleEnabled(s.inactive_begin,s.inactive_end)) st.textContent+=' · inactive '+s.inactive_begin+'–'+s.inactive_end;
    if(samsungDeepSleep(d,rec,samsungStatusCache)) st.textContent+=' · deep sleep (button wake)';
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

function samsungConfigBody(extra){
  const begin=document.getElementById('samsung-inactive-begin').value;
  const end=document.getElementById('samsung-inactive-end').value;
  return Object.assign({
    name:document.getElementById('samsung-name-input').value.trim(),
    wifi_mac:document.getElementById('samsung-wifi-mac').value.trim(),
    poll_interval_minutes:parseInt(document.getElementById('samsung-poll').value,10)||60,
    inactive_begin:begin?begin.slice(0,5):'',
    inactive_end:end?end.slice(0,5):'',
    overnight_deep_sleep:document.getElementById('samsung-overnight-deep-sleep').checked,
    crop_format:samsungCropFormat(),
    display_width:parseInt(document.getElementById('samsung-display-width').value,10)||0,
    display_height:parseInt(document.getElementById('samsung-display-height').value,10)||0,
    auto_sleep_after_push:document.getElementById('samsung-auto-sleep').checked,
    sleep_after_push_seconds:parseInt(document.getElementById('samsung-sleep-delay').value,10)||15
  }, extra||{});
}

async function saveSamsungName(){
  if(!samsungCurrentId)return;
  const name=document.getElementById('samsung-name-input').value.trim();
  const r=await fetch('/samsung/'+encodeURIComponent(samsungCurrentId)+'/status');
  const s=await r.json();
  await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/config',{
    method:'PUT',headers:{'Content-Type':'application/json'},
    body:JSON.stringify(samsungConfigBody({
      poll_interval_minutes:s.poll_interval_minutes||60,
      inactive_begin:s.inactive_begin||'',
      inactive_end:s.inactive_end||'',
      overnight_deep_sleep:s.overnight_deep_sleep!==false,
      crop_format:s.crop_format||'16:9',
      display_width:s.display_width||0,
      display_height:s.display_height||0,
      auto_sleep_after_push:s.auto_sleep_after_push!==false,
      sleep_after_push_seconds:s.sleep_after_push_seconds||15
    }))
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
  const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/config',{
    method:'PUT',headers:{'Content-Type':'application/json'},
    body:JSON.stringify(samsungConfigBody())
  });
  if(!r.ok){alert('Save failed: '+(await r.text()));return;}
  await reloadSamsungFrame();
  await loadDevicesInner();
}

async function samsungLoadDailyRefresh(){
  if(!samsungCurrentId) return;
  const st=document.getElementById('samsung-daily-refresh-status');
  try{
    const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/daily-refresh');
    if(!r.ok) throw new Error(await r.text());
    const j=await r.json();
    const dr=document.getElementById('samsung-daily-refresh');
    if(dr&&j.daily_refresh_time) dr.value=j.daily_refresh_time;
    if(st) st.textContent=j.query_error?('query: '+j.query_error):'';
    if(samsungStatusCache) samsungStatusCache.daily_refresh_time=j.daily_refresh_time||'';
  }catch(e){
    if(st) st.textContent='load failed';
  }
}

async function samsungSyncDailyRefresh(){
  if(!samsungCurrentId) return;
  const st=document.getElementById('samsung-daily-refresh-status');
  if(st) st.textContent='Syncing…';
  try{
    const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/daily-refresh/sync-inactive',{method:'POST'});
    if(!r.ok) throw new Error(await r.text());
    const j=await r.json();
    const dr=document.getElementById('samsung-daily-refresh');
    if(dr&&j.daily_refresh_time) dr.value=j.daily_refresh_time;
    if(st) st.textContent='Synced to inactive end';
    if(samsungStatusCache) samsungStatusCache.daily_refresh_time=j.daily_refresh_time||'';
    await reloadSamsungFrame();
  }catch(e){
    alert('Sync failed: '+e.message);
    if(st) st.textContent='';
  }
}

async function samsungWake(){
  if(!samsungCurrentId)return;
  const st=document.getElementById('samsung-status');
  st.textContent='Waking display…';
  try{
    const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/wake',{method:'POST'});
    if(!r.ok) throw new Error(await r.text());
    await loadDevicesInner();
    await reloadSamsungFrame();
    st.textContent='Wake sent';
  }catch(e){
    alert('Wake failed: '+e.message);
    st.textContent='';
  }
}

async function samsungSleep(){
  if(!samsungCurrentId)return;
  const st=document.getElementById('samsung-status');
  st.textContent='Sending sleep…';
  try{
    const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/sleep',{method:'POST'});
    if(!r.ok) throw new Error(await r.text());
    await loadDevicesInner();
    await reloadSamsungFrame();
    st.textContent='Sleep sent';
  }catch(e){
    alert('Sleep failed: '+e.message);
    st.textContent='';
  }
}

async function samsungPushCurrent(){
  if(!samsungCurrentId)return;
  const rec=samsungFrameRecord(samsungCurrentId);
  const dev=samsungDeviceForFrame(samsungCurrentId);
  if(!rec&&!dev){alert('Frame is not registered on the hub — click Discover displays first.');return;}
  const st=document.getElementById('samsung-status');
  const deepHint=samsungDeepSleepWakeHint(dev,rec,samsungStatusCache);
  if(st) st.textContent=deepHint||'Pushing…';
  try{
    const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/push',{method:'POST'});
    if(!r.ok) throw new Error(await r.text());
    const j=await r.json();
    if(st) st.textContent='Downloading…';
    const delivered=j.send_id?await waitSendDelivered(j.send_id):false;
    if(document.getElementById('samsung-auto-sleep').checked){
      const delay=parseInt(document.getElementById('samsung-sleep-delay').value,10)||15;
      if(st) st.textContent=delivered?('✓ Pushed — frame will sleep in ~'+delay+'s…'):'Pushed (unconfirmed)';
    }else if(st){
      st.textContent=delivered?'✓ Pushed':'Pushed (unconfirmed)';
    }
    await loadDevicesInner();
    loadSamsungFrames();
    await reloadSamsungFrame();
  }catch(e){
    alert('Push failed: '+e.message);
    if(st) st.textContent='';
  }
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
  samsungPreviewKey=null;
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
  const url='/images/'+encodeURIComponent(id)+'/preview';
  t.src='';
  t.src=url;
}

function refreshSamsungPreview(){
  const t=document.getElementById('samsung-preview');
  if(!t||!samsungCurrentId) return;
  const url='/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/preview';
  t.src='';
  t.src=url;
}

function refreshIJPreview(url){
  const t=document.getElementById('ij-preview');
  if(!t) return;
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
