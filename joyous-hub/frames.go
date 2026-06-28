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

func (h *Hub) sendInkJoyImage(dev *Device, imageID, overlayToken, sendID string) error {
	addr := h.serverAddr
	if addr == "" {
		addr = "localhost:8080"
	}
	_, port, _ := net.SplitHostPort(addr)
	if port == "" {
		port = "8080"
	}
	ip := dev.HubIP
	if ip == "" {
		ip = h.hubIP
	}
	if ip != "" {
		addr = net.JoinHostPort(ip, port)
	}
	portrait := dev.Portrait
	if overlayToken != "" {
		if _, err := os.Stat(h.images.overlayCacheFile(imageID, overlayToken, portrait)); err != nil {
			return fmt.Errorf("overlay bin: %w", err)
		}
	} else if _, err := h.images.ServeBinOrientation(imageID, portrait); err != nil {
		return fmt.Errorf("image convert: %w", err)
	}
	file := imageBinFilename(imageID, overlayToken, portrait)
	imgURL := fmt.Sprintf("http://%s/images/%s", addr, file)
	payload, msgid := buildPlayPayload(dev.MAC, imgURL)
	if h.sendDelivery != nil {
		h.sendDelivery.UnbindInkJoy(sendID)
		h.sendDelivery.BindInkJoy(sendID, msgid)
	}
	registerInjectedPlay(msgid)
	topic := "/inkjoyap/" + dev.MAC
	logOutbound("mqtt publish topic=%s image=%s url=%s portrait=%v overlay=%s", topic, imageID, imgURL, portrait, overlayToken)
	err := h.publisher.Publish(topic, payload)
	logFrameSend(dev.ID, imageID, "inkjoy", err)
	if err == nil {
		h.displayPreview.Clear(dev.MAC)
		h.devices.SetLastImage(dev.ID, imageID, overlayToken)
	}
	return err
}

func (h *Hub) sendSamsungImage(dev *Device, imageID, overlayToken, sendID string) error {
	frameID := SamsungFrameID(dev)
	meta, err := h.images.readMeta(imageID)
	if err != nil {
		return err
	}
	pngData, err := h.prepareSamsungPNG(imageID, overlayToken, dev)
	if err != nil {
		return fmt.Errorf("convert samsung png: %w", err)
	}
	if err := h.samsung.writePNGLocked(frameID, pngData); err != nil {
		return err
	}
	etag, _, _ := h.samsung.PNGInfo(frameID)
	if h.sendDelivery != nil {
		h.sendDelivery.BindSamsung(sendID, frameID, etag)
	}
	_ = meta
	logOutbound("samsung prepare frame=%s ip=%s png=%dB overlay=%s", frameID, dev.IP, len(pngData), overlayToken)
	err = h.pushSamsungFrame(frameID, dev)
	logFrameSend(dev.ID, imageID, "samsung", err)
	if err == nil {
		h.devices.SetLastImage(dev.ID, imageID, overlayToken)
	}
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
		ip := d.IP
		pin := d.MDCPin
		go h.ensureSamsungMAC(ip, pin)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":        true,
		"found":     len(added),
		"ssdp_seen": ssdpSeen,
		"devices":   added,
	})
}
