package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

const (
	// play_ack 113 can arrive before the panel finishes the wipe; hold before the next play.
	inkjoyPrePlayTimeout     = 5 * time.Minute
	inkjoyPrePlaySettleDelay = 90 * time.Second
)

// waitInkJoyPlayComplete waits for play_ack 113, then an extra settle period so the
// physical transition can finish before another play is queued.
func waitInkJoyPlayComplete(h *Hub, sendID string, playTimeout, settleDelay time.Duration) bool {
	if h.sendDelivery != nil {
		if h.sendDelivery.Wait(sendID, playTimeout) != sendStatusDelivered {
			return false
		}
	}
	if settleDelay <= 0 {
		return true
	}
	time.Sleep(settleDelay)
	return true
}

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
	calID, err := h.images.ensureInkJoyCalibrationID()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("inkjoy calibration: sending primaries chart (green swatch uses lo=248)")

	sendID, err := h.SendImageToDeviceSession(body.DeviceID, calID, "", requestSessionID(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"send_id":  sendID,
		"image_id": calID,
	})
}

func (h *Hub) handleInkJoyBlack248CalibrationSend(w http.ResponseWriter, r *http.Request) {
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
	primeID, err := h.images.ensureInkJoyBlackUniform248ID()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("inkjoy calibration: black uniform lo=248 prime")
	sendID, err := h.SendImageToDeviceSession(body.DeviceID, primeID, "", requestSessionID(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"send_id":  sendID,
		"image_id": primeID,
	})
}

func (h *Hub) handleInkJoyLoLadderCalibrationSend(w http.ResponseWriter, r *http.Request) {
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
	calID, err := h.images.ensureInkJoyLoLadderPrimariesID()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("inkjoy calibration: lo-ladder primaries × lo grid (play 2/2)")
	sendID, err := h.SendImageToDeviceSession(body.DeviceID, calID, "", requestSessionID(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"send_id":  sendID,
		"image_id": calID,
	})
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
	if dev == nil {
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
	if err := h.requireSamsungBridge(); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	var sendID string
	if h.sendDelivery != nil {
		if etag, _, ok := h.samsung.PNGInfo(frameID); ok {
			send := h.sendDelivery.RegisterWithSession(dev.ID, "", requestSessionID(r))
			h.sendDelivery.BindSamsung(send.ID, frameID, etag)
			sendID = send.ID
			h.publishSendEvent(sendID)
		}
	}
	if err := h.publishSamsungPushCmd(dev.ID, sendID); err != nil {
		if sendID != "" {
			h.sendDelivery.Fail(sendID)
			h.publishSendEvent(sendID)
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	out := map[string]any{"ok": true, "delegated": "samsung-bridge"}
	if sendID != "" {
		out["send_id"] = sendID
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
