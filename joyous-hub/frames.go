package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
)

// SendImageToDevice pushes an album image to any registered frame type.
func (h *Hub) SendImageToDevice(deviceID, imageID string) error {
	dev, ok := h.devices.Get(deviceID)
	if !ok {
		return fmt.Errorf("device %q not found", deviceID)
	}
	switch dev.Type {
	case DeviceTypeInkJoy:
		return h.sendInkJoyImage(dev, imageID)
	case DeviceTypeSamsung:
		return h.sendSamsungImage(dev, imageID)
	default:
		return fmt.Errorf("unsupported device type %q", dev.Type)
	}
}

func (h *Hub) sendInkJoyImage(dev *Device, imageID string) error {
	addr := h.serverAddr
	if addr == "" {
		addr = "localhost:8080"
	}
	imgURL := fmt.Sprintf("http://%s/images/%s.bin", addr, imageID)
	payload := buildPlayPayload(dev.MAC, imgURL, addr)
	topic := "/inkjoyap/" + dev.MAC
	logOutbound("mqtt publish topic=%s image=%s url=%s", topic, imageID, imgURL)
	err := h.publisher.Publish(topic, payload)
	logFrameSend(dev.ID, imageID, "inkjoy", err)
	return err
}

func (h *Hub) sendSamsungImage(dev *Device, imageID string) error {
	frameID := SamsungFrameID(dev)
	meta, err := h.images.readMeta(imageID)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(h.images.rawPath(imageID))
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}
	profile := h.samsungDisplayProfile(dev, frameID)
	crops, _ := h.images.GetCrops(imageID)
	crop, hasCrop := cropForFormat(crops, profile.CropFormat)
	logOutbound("samsung crop format=%s saved=%t size=%dx%d", profile.CropFormat, hasCrop, profile.Width, profile.Height)
	pngData, err := convertToSamsungPNG(raw, profile, crop, hasCrop)
	if err != nil {
		return fmt.Errorf("convert samsung png: %w", err)
	}
	if err := h.samsung.writePNGLocked(frameID, pngData); err != nil {
		return err
	}
	_ = meta // name retained for future manifest metadata

	addr := h.serverAddr
	if addr == "" {
		addr = "localhost:8080"
	}
	fileID := newSamsungPushFileID()
	setSamsungPushFileID(frameID, fileID)
	contentURL := samsungMDCContentURL(addr, dev.IP, frameID)
	logOutbound("samsung prepare frame=%s ip=%s file_id=%s content=%s png=%dB", frameID, dev.IP, fileID, contentURL, len(pngData))
	err = SendMDCContentDownload(dev.IP, contentURL, dev.MDCPin, dev.MDCMAC)
	logFrameSend(dev.ID, imageID, "samsung", err)
	return err
}

// handleDiscover runs SSDP discovery and merges results into the device registry.
func (h *Hub) handleDiscover(w http.ResponseWriter, r *http.Request) {
	logOutbound("discover start subnets=%v", discoverSubnets)
	frames, ssdpSeen, err := DiscoverPhotoFrames(0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	added := make([]Device, 0, len(frames))
	for _, sd := range frames {
		d := h.devices.UpsertSamsung(sd)
		added = append(added, *d)
	}
	if err := h.devices.Save(); err != nil {
		log.Printf("warn: save devices after discover: %v", err)
	}
	log.Printf("discover: ssdp=%d frames=%d", ssdpSeen, len(added))
	for _, d := range added {
		logOutbound("discover found type=%s id=%s ip=%s", d.Type, d.ID, d.IP)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":        true,
		"found":     len(added),
		"ssdp_seen": ssdpSeen,
		"devices":   added,
	})
}
