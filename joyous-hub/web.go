package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"joyous-hub/bridgehub"
	"joyous-hub/inkjoybridge"
	"joyous-hub/protocol"
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
	inkjoyRetry    *InkJoySendRetry
	overlay        *OverlayStore
	color          *ColorStore
	scheduledSends *ScheduledSendStore
	events         *EventBus
	weather        weatherClient
	bridgeCoord    *bridgehub.Coordinator
	hubIP          string
	publisher      MQTTPublisher
	serverAddr     string
	mqttLog        *MQTTLogBuffer // inkjoy-bridge frame MQTT (unused on hub)
	joyousMQTTLog  *MQTTLogBuffer // hub broker: bridge↔hub joyous protocol
}

// handleMQTTLogs serves GET /api/mqtt/logs — hub↔bridge Joyous MQTT on the hub broker.
func (h *Hub) handleMQTTLogs(w http.ResponseWriter, r *http.Request) {
	var local, upstream []MQTTLogEntry
	if h.joyousMQTTLog != nil {
		local, upstream = h.joyousMQTTLog.Snapshot()
	}
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

// handleSamsungLogs serves GET /api/samsung/logs — bridge discover/MDC/WoL ring buffer.
// Proxies to samsung-bridge when online (that process owns LAN probes). Photos stay on hub HTTP.
func (h *Hub) handleSamsungLogs(w http.ResponseWriter, r *http.Request) {
	if h.bridgeCoord != nil && h.bridgeCoord.BridgeOnline(string(DeviceTypeSamsung)) {
		h.proxyBridgeHTTP(w, r, string(DeviceTypeSamsung), "/api/samsung/logs")
		return
	}
	ents := samsungHandshakeLog.Snapshot()
	if ents == nil {
		ents = []SamsungLogEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"entries": ents})
}

// handleDevices serves GET /api/devices.
func (h *Hub) handleDevices(w http.ResponseWriter, r *http.Request) {
	devs := h.devices.List()
	if devs == nil {
		devs = []Device{}
	}
	// Convert stored UTC sleep times → server local time for display.
	for i := range devs {
		devs[i].SleepBeginTime, devs[i].SleepEndTime =
			sleepWindowToLocal(devs[i].SleepBeginTime, devs[i].SleepEndTime)
	}
	h.applySamsungFriendlyNames(devs)
	h.applyInkJoyCurrentDisplay(devs)
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

// devicesSnapshotForUI returns a List() copy with Samsung Connected recomputed
// and InkJoy sleep-window times converted UTC → local. SSE must use this (not
// raw List()) so live badges match GET /api/devices — TouchSamsung used to
// leave Connected stale as false after a sleep while LastAction already
// proved the frame was awake again; separately, skipping the UTC→local
// conversion here used to make the sleep-window chip flip back to raw UTC on
// every live update after showing correct local time on initial page load.
func (h *Hub) devicesSnapshotForUI() []Device {
	devs := h.devices.List()
	if devs == nil {
		return []Device{}
	}
	for i := range devs {
		devs[i].SleepBeginTime, devs[i].SleepEndTime =
			sleepWindowToLocal(devs[i].SleepBeginTime, devs[i].SleepEndTime)
		if devs[i].Type == DeviceTypeSamsung {
			ApplySamsungConnected(&devs[i])
		}
	}
	h.applyInkJoyCurrentDisplay(devs)
	return devs
}

// applyInkJoyCurrentDisplay sets what each InkJoy frame is showing from disk
// rather than the registry: sends and relayed cloud plays happen in the
// bridge process, so the hub's registry never hears of most of them. The
// newest cached play wins — a hub image shows its frame preview
// (last_image_id); a relayed cloud play (not a hub image) shows the preview
// JPEG the relay saved (display_preview_at).
func (h *Hub) applyInkJoyCurrentDisplay(devs []Device) {
	if h.inkjoy == nil {
		return
	}
	for i := range devs {
		d := &devs[i]
		if d.Type != DeviceTypeInkJoy {
			continue
		}
		imageID, token, sentAt, ok := h.inkjoy.LatestBin(d.MAC)
		previewAt := h.displayPreview.ModTime(d.MAC)
		hubImage := false
		if ok && h.images != nil {
			_, err := h.images.readMeta(imageID)
			hubImage = err == nil
		}
		if ok && h.latestPlayIsIJI3(d.MAC) && !previewAt.After(sentAt) {
			// The hub's frame preview renders the 10" layout; an IJI3 play
			// (28.5") is previewed from the played file itself instead.
			d.LastImageID, d.LastOverlayHash, d.DisplayPreviewAt = "", "", sentAt
			continue
		}
		switch {
		case hubImage && !previewAt.After(sentAt):
			d.LastImageID, d.LastOverlayHash, d.DisplayPreviewAt = imageID, token, time.Time{}
		case !previewAt.IsZero():
			d.LastImageID, d.LastOverlayHash, d.DisplayPreviewAt = "", "", previewAt
		}
	}
}

// utcHHMMToLocal converts a "HH:MM" string from UTC to the server's local timezone.
// Returns the input unchanged on any parse error or if it's empty/00:00.
func utcHHMMToLocal(hhmm string) string {
	// Don't special-case "00:00" — a schedule can legitimately start at
	// midnight local time. Disabled-schedule detection lives elsewhere
	// (withinWindow: beginMin == endMin), which still holds after converting
	// both fields with the same offset, so this only needs to guard the
	// genuinely-unparseable empty case.
	if hhmm == "" {
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

// sleepWindowToLocal converts a stored UTC begin/end sleep-window pair to
// local time for display. A disabled window (begin == end — see the
// firmware's withinWindow, which treats equal begin/end as "no schedule",
// including the "0:00"/"0:00" a never-configured device reports) is left
// unconverted, so it still reads as a recognizable "not set" pair rather
// than converting to some arbitrary-looking equal local time.
func sleepWindowToLocal(begin, end string) (string, string) {
	if begin == end {
		return begin, end
	}
	return utcHHMMToLocal(begin), utcHHMMToLocal(end)
}

// localHHMMToUTC converts a "HH:MM" string from the server's local timezone to UTC.
func localHHMMToUTC(hhmm string) string {
	// See utcHHMMToLocal — no "00:00" special case; a schedule can
	// legitimately start at local midnight.
	if hhmm == "" {
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
	if h.scheduledSends != nil {
		if err := h.scheduledSends.Delete(id); err != nil {
			log.Printf("device delete %s: remove scheduled send config: %v", id, err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleImages serves GET /api/images.
func (h *Hub) handleImages(w http.ResponseWriter, r *http.Request) {
	albumID, f := parseImageListFilter(r)
	imgs, err := h.images.ListImagesQuery(albumID, f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
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
	if meta, err := h.images.GetMeta(id); err == nil {
		h.publishImagesUpdated(meta)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": id, "name": name})
}

// handleImageDelete serves DELETE /api/images/{id}.
func (h *Hub) handleImageDelete(w http.ResponseWriter, r *http.Request, id string) {
	h.images.DeleteImage(id)
	h.publishImagesRemoved(id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleImageGet serves GET /api/images/{id} — metadata for one album image.
func (h *Hub) handleImageGet(w http.ResponseWriter, r *http.Request, id string) {
	meta, err := h.images.GetMeta(id)
	if err != nil {
		code := http.StatusNotFound
		if !strings.Contains(err.Error(), "not found") {
			code = http.StatusInternalServerError
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(meta)
}

// handleImagePatch serves PATCH /api/images/{id} — updates display name, chroma override,
// tags, and/or rotation override.
func (h *Hub) handleImagePatch(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Name            *string   `json:"name"`
		ChromaBoostMode *string   `json:"chroma_boost_mode"`
		Tags            *[]string `json:"tags"`
		RotateOverride  *int      `json:"rotate_override"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.Name == nil && body.ChromaBoostMode == nil && body.Tags == nil && body.RotateOverride == nil {
		http.Error(w, "name, chroma_boost_mode, tags, or rotate_override required", http.StatusBadRequest)
		return
	}
	chromaMode := ""
	if body.ChromaBoostMode != nil {
		switch *body.ChromaBoostMode {
		case "global", "on", "off":
			chromaMode = *body.ChromaBoostMode
		default:
			http.Error(w, "chroma_boost_mode must be global, on, or off", http.StatusBadRequest)
			return
		}
	}
	meta, err := h.images.PatchMeta(id, body.Name, chromaMode, body.Tags, body.RotateOverride)
	if err != nil {
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			code = http.StatusNotFound
		}
		http.Error(w, err.Error(), code)
		return
	}
	h.publishImagesUpdated(meta)
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
	sendID, err := h.sendImageToDeviceAutoSession(deviceID, body.ImageID, requestSessionID(r))
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
	if meta, err := h.images.GetMeta(id); err == nil {
		h.publishImagesUpdated(meta)
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
	if meta, err := h.images.GetMeta(id); err == nil {
		h.publishImagesUpdated(meta)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleStatic serves the embedded SPA for any non-API route, except for paths a connected
// bridge has registered as its own via HelloPayload.HTTPPaths (see BridgeForPath) — those are
// forwarded to the bridge instead. This covers vendor protocol callbacks that hit what a
// physical device believes is a server root (e.g. Samsung frames POSTing
// /content-transfer-progress, not namespaced under /samsung/…) rather than requiring the hub
// to hardcode every such quirk itself.
func (h *Hub) handleStatic(w http.ResponseWriter, r *http.Request) {
	if h.bridgeCoord != nil {
		if bridgeID, ok := h.bridgeCoord.BridgeForPath(r.URL.Path); ok {
			h.proxyBridgeHTTP(w, r, bridgeID, r.URL.Path)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(uiRevisionHTML()))
}

// ── MQTT payload helpers ─────────────────────────────────────────────────────
// Ack result bitfield: see inkjoy_ack.go

// buildAckPayloadFor builds a frame→cloud ack matching real frame shape.
func buildAckPayloadFor(mac, ackAction, ackMsgid string, data map[string]any) []byte {
	return inkjoybridge.BuildAckPayload(mac, ackAction, ackMsgid, data)
}

// buildPlayPayload returns the MQTT payload and the msgid it embedded.
// If msgid is empty a fresh unix-ms id is minted; otherwise that id is reused
// (same message replay — frame skips redo for duplicate msgid).
// Callers should register the msgid via registerInjectedPlay so the
// corresponding play_ack can be suppressed from upstream forwarding.
// hash is the SD-cache content key (see ContentHash) for clone firmware
// with PNG/ContentCache support (inkjoybridge.InkJoyPNGProtocolT) — pass ""
// for real InkJoy hardware and older clone builds, which don't understand
// the field (see content_cache.h's parsePlay comment: absent hash is
// treated as "no cache key available", same as before this field existed).
func buildPlayPayload(mac, imgURL, msgid, hash string, overlay *protocol.OverlaySendBody) ([]byte, string) {
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
	if msgid == "" {
		msgid = fmt.Sprintf("%d", time.Now().UnixMilli())
	}
	img0 := map[string]any{"imgid": "local-0", "imgurl": path}
	if hash != "" {
		img0["hash"] = hash
	}
	data := map[string]any{
		"host":     host,
		"port":     port,
		"imgs":     []any{img0},
		"mode":     2,
		"strategy": 1,
	}
	if overlay != nil {
		data["overlay"] = map[string]any{
			"lines":   overlay.Lines,
			"caption": overlay.Caption,
		}
	}
	b, _ := json.Marshal(map[string]any{
		"action": "play",
		"msgid":  msgid,
		"stamac": mac,
		"data":   data,
	})
	return b, msgid
}

// ── Embedded static HTML ─────────────────────────────────────────────────────

// The hub page lives in web/ (served from an embedded FS, see web_assets.go).
