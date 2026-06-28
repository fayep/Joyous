package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (h *Hub) handleCalibrationPNG(w http.ResponseWriter, r *http.Request, kind string) {
	if !calibrationKindValid(kind) {
		http.Error(w, "unknown calibration kind", http.StatusNotFound)
		return
	}
	data, contentType, err := calibrationPNG(kind)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(data)
}

func (h *Hub) handleInkJoyCalibrationSend(w http.ResponseWriter, r *http.Request) {
	if h.images == nil {
		http.Error(w, "images not configured", http.StatusInternalServerError)
		return
	}
	var body struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DeviceID == "" {
		http.Error(w, "device_id required", http.StatusBadRequest)
		return
	}
	imageID, err := h.images.ensureInkJoyCalibrationID()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sendID, err := h.SendImageToDevice(body.DeviceID, imageID, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "send_id": sendID, "image_id": imageID})
}

func (h *Hub) handleSamsungCalibration(w http.ResponseWriter, r *http.Request, frameID string) {
	if !validFrameID(frameID) {
		http.Error(w, "invalid frame id", http.StatusBadRequest)
		return
	}
	if h.samsung == nil {
		http.Error(w, "samsung not configured", http.StatusInternalServerError)
		return
	}
	frameID = h.resolveSamsungFrameID(frameID)
	dev := h.samsungDeviceByFrameID(frameID)
	if dev == nil || dev.IP == "" {
		http.Error(w, "frame not registered on hub", http.StatusNotFound)
		return
	}
	pngData, err := samsungCalibrationPNG()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.samsung.StorePNG(frameID, pngData); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var sendID string
	if h.sendDelivery != nil {
		if etag, _, ok := h.samsung.PNGInfo(frameID); ok {
			send := h.sendDelivery.Register(dev.ID, "")
			h.sendDelivery.BindSamsung(send.ID, frameID, etag)
			sendID = send.ID
		}
	}
	if err := h.pushSamsungFrame(frameID, dev); err != nil {
		if sendID != "" {
			h.sendDelivery.Fail(sendID)
		}
		code := http.StatusBadGateway
		if strings.Contains(err.Error(), "frame did not wake") {
			code = http.StatusGatewayTimeout
		}
		http.Error(w, err.Error(), code)
		return
	}
	out := map[string]any{"ok": true}
	if sendID != "" {
		out["send_id"] = sendID
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
