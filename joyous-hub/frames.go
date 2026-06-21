package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// injectedPlays tracks msgIDs of play messages we sent so we can suppress
// the frame's play_ack from being forwarded upstream (the real broker never
// sent the play, so it must never see the ack).
var injectedPlays sync.Map

func registerInjectedPlay(msgid string) {
	injectedPlays.Store(msgid, struct{}{})
	time.AfterFunc(5*time.Minute, func() { injectedPlays.Delete(msgid) })
}

func isInjectedPlay(ackMsgid string) bool {
	_, ok := injectedPlays.Load(ackMsgid)
	return ok
}

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
	_, port, _ := net.SplitHostPort(addr)
	if port == "" {
		port = "8080"
	}
	// Use the IP from the frame's live MQTT socket; fall back to the startup-resolved LAN IP.
	ip := dev.HubIP
	if ip == "" {
		ip = h.hubIP
	}
	if ip != "" {
		addr = net.JoinHostPort(ip, port)
	}
	portrait := dev.Portrait
	if _, err := h.images.ServeBinOrientation(imageID, portrait); err != nil {
		return fmt.Errorf("image convert: %w", err)
	}
	suffix := ".bin"
	if portrait {
		suffix = "-p.bin"
	}
	imgURL := fmt.Sprintf("http://%s/images/%s%s", addr, imageID, suffix)
	payload, msgid := buildPlayPayload(dev.MAC, imgURL)
	registerInjectedPlay(msgid)
	topic := "/inkjoyap/" + dev.MAC
	logOutbound("mqtt publish topic=%s image=%s url=%s portrait=%v", topic, imageID, imgURL, portrait)
	err := h.publisher.Publish(topic, payload)
	logFrameSend(dev.ID, imageID, "inkjoy", err)
	if err == nil {
		h.displayPreview.Clear(dev.MAC)
		h.devices.SetLastImage(dev.MAC, imageID)
	}
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
	cfg, _ := h.samsung.LoadConfig(frameID)
	wifiMAC := h.samsungWakeMAC(frameID, dev)
	autoSleep := samsungAutoSleepAfterPush(cfg)
	sleepAfter := samsungSleepAfterPushSec(cfg)
	err = PushSamsungContent(dev.IP, contentURL, dev.MDCPin, wifiMAC, autoSleep, sleepAfter, h.sleepSamsungDisplay)
	if err == nil {
		h.devices.TouchSamsung(dev.IP, "mdc_push")
	}
	logFrameSend(dev.ID, imageID, "samsung", err)
	return err
}

// resolvedLANIP extracts the host from addr (host:port) and resolves it to a
// non-loopback LAN IPv4. Falls back to scanning interface addresses if DNS
// returns only loopback (e.g. hubhost.local → 127.0.0.1 on the hub itself).
func resolvedLANIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
		return ip.String()
	}
	if ips, err := net.LookupHost(host); err == nil {
		for _, s := range ips {
			if ip := net.ParseIP(s); ip != nil && !ip.IsLoopback() && ip.To4() != nil {
				return s
			}
		}
	}
	// DNS only returned loopback — scan local interfaces.
	if ifaces, err := net.InterfaceAddrs(); err == nil {
		for _, a := range ifaces {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ip4 := ipnet.IP.To4(); ip4 != nil && !strings.HasPrefix(ip4.String(), "169.") {
					return ip4.String()
				}
			}
		}
	}
	return host
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
