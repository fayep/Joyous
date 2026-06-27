package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"os"
	"strings"
	"time"
)

func (h *Hub) handleOverlayGet(w http.ResponseWriter, r *http.Request) {
	if h.overlay == nil {
		http.Error(w, "overlay not configured", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.overlay.Config())
}

func (h *Hub) handleOverlayPut(w http.ResponseWriter, r *http.Request) {
	if h.overlay == nil {
		http.Error(w, "overlay not configured", http.StatusInternalServerError)
		return
	}
	var cfg OverlayConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := h.overlay.Save(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (h *Hub) handleOverlayPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ImageID  string         `json:"image_id"`
		Portrait bool           `json:"portrait"`
		Config   *OverlayConfig `json:"config,omitempty"`
	}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
	} else {
		body.ImageID = strings.TrimSpace(r.URL.Query().Get("image_id"))
		body.Portrait = r.URL.Query().Get("portrait") == "1"
	}
	if body.ImageID == "" {
		http.Error(w, "image_id required", http.StatusBadRequest)
		return
	}
	jpeg, err := h.renderOverlayPreviewJPEG(r.Context(), body.ImageID, body.Portrait, body.Config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(jpeg)
}

func (h *Hub) handleOverlayMetrics(w http.ResponseWriter, r *http.Request) {
	if h.overlay == nil {
		http.Error(w, "overlay not configured", http.StatusInternalServerError)
		return
	}
	var body struct {
		Config *OverlayConfig `json:"config,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	cfg := mergeOverlayConfig(h.overlay.Config(), body.Config)
	weather, err := h.fetchOverlayWeather(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	m := overlayMetrics(cfg, weather)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}

func (h *Hub) handleOverlaySend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceID   string `json:"device_id"`
		ImageID    string `json:"image_id"`
		UseCurrent bool   `json:"use_current"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DeviceID == "" {
		http.Error(w, "device_id required", http.StatusBadRequest)
		return
	}
	dev, ok := h.devices.Get(body.DeviceID)
	if !ok {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	imageID := strings.TrimSpace(body.ImageID)
	if body.UseCurrent || imageID == "" {
		if dev.LastImageID != "" {
			imageID = dev.LastImageID
		}
	}
	if imageID == "" {
		http.Error(w, "no tracked album image on frame — send from Album first, or pick a preview image", http.StatusBadRequest)
		return
	}
	if !h.overlay.Active() {
		http.Error(w, "overlay is disabled or empty", http.StatusBadRequest)
		return
	}
	sendID, err := h.SendImageToDeviceWithOverlay(body.DeviceID, imageID)
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
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "send_id": sendID, "image_id": imageID})
}

func (h *Hub) SendImageToDeviceWithOverlay(deviceID, imageID string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg := h.overlay.Config()
	weather, err := h.fetchOverlayWeather(ctx)
	if err != nil {
		return "", err
	}
	dev, ok := h.devices.Get(deviceID)
	if !ok {
		return "", fmt.Errorf("device %q not found", deviceID)
	}
	token := cfg.sendToken(weather, dev.Portrait)
	if dev.Type == DeviceTypeInkJoy {
		if _, err := h.ensureOverlayBin(imageID, dev.Portrait, cfg, weather); err != nil {
			return "", err
		}
	}
	return h.SendImageToDevice(deviceID, imageID, token)
}

// SendImageToDevice pushes an album image to any registered frame type.
// overlayToken is empty for a plain send; otherwise the frame pulls a composited bin/png.
func (h *Hub) SendImageToDevice(deviceID, imageID, overlayToken string) (string, error) {
	dev, ok := h.devices.Get(deviceID)
	if !ok {
		return "", fmt.Errorf("device %q not found", deviceID)
	}
	if h.sendDelivery == nil {
		h.sendDelivery = NewSendDeliveryTracker()
	}
	send := h.sendDelivery.Register(deviceID, imageID)
	var err error
	switch dev.Type {
	case DeviceTypeInkJoy:
		err = h.sendInkJoyImage(dev, imageID, overlayToken, send.ID)
	case DeviceTypeSamsung:
		err = h.sendSamsungImage(dev, imageID, overlayToken, send.ID)
	default:
		err = fmt.Errorf("unsupported device type %q", dev.Type)
	}
	if err != nil {
		h.sendDelivery.Fail(send.ID)
		return "", err
	}
	return send.ID, nil
}

func (h *Hub) sendImageToDeviceAuto(deviceID, imageID string) (string, error) {
	if h.overlay != nil && h.overlay.Active() {
		return h.SendImageToDeviceWithOverlay(deviceID, imageID)
	}
	return h.SendImageToDevice(deviceID, imageID, "")
}

func (h *Hub) prepareSamsungPNG(imageID, overlayToken string, dev *Device) ([]byte, error) {
	raw, err := os.ReadFile(h.images.rawPath(imageID))
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	frameID := SamsungFrameID(dev)
	profile := h.samsungDisplayProfile(dev, frameID)
	crops, _ := h.images.GetCrops(imageID)
	crop, hasCrop := cropForFormat(crops, profile.CropFormat)
	img, err := prepareSamsungFrameRGBA(raw, profile, crop, hasCrop)
	if err != nil {
		return nil, err
	}
	if overlayToken != "" {
		cfg := h.overlay.Config()
		weather, err := h.fetchOverlayWeather(context.Background())
		if err != nil {
			return nil, err
		}
		img = drawWeatherOverlay(img, cfg, weather, dev.Portrait)
	}
	return encodeSamsungPNGFromRGBA(img)
}

func prepareSamsungFrameRGBA(raw []byte, profile SamsungDisplayProfile, crop CropRect, hasCrop bool) (image.Image, error) {
	tw, th := profile.Width, profile.Height
	if tw <= 0 || th <= 0 {
		tw, th = samsungW, samsungH
	}
	img, err := decodeAnyImage(raw)
	if err != nil {
		return nil, err
	}
	if hasCrop && crop.W > 0 && crop.H > 0 {
		img = applyCrop(img, crop)
	} else {
		img = centerCropToSize(img, tw, th)
	}
	return resizeTo(img, tw, th), nil
}

func encodeSamsungPNGFromRGBA(img image.Image) ([]byte, error) {
	indices := StuckiTwoPalette(img, PaletteSamsungDisplay, UniqueColors(img) > 6)
	out := RenderIndicesToRGB(indices, PaletteSamsungSend)
	return encodePNG(out), nil
}
