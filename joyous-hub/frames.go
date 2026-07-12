package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"joyous-hub/protocol"
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

func (h *Hub) sendInkJoyImage(dev *Device, imageID, overlayToken, sendID string, overlayCfg *OverlayConfig, overlayWeather *WeatherSnapshot) error {
	if h.bridgeCoord == nil || !h.bridgeCoord.BridgeOnline(string(DeviceTypeInkJoy)) {
		return fmt.Errorf("inkjoy bridge offline")
	}
	body, err := buildSendImageBody(h, imageID, overlayToken, sendID, overlayCfg, overlayWeather)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	logOutbound("bridge cmd send.image device=%s image=%s crops=%d", dev.ID, imageID, len(body.Crops))
	err = h.bridgeCoord.PublishCommand(string(DeviceTypeInkJoy), protocol.CmdPayload{
		Cmd:      protocol.CmdSendImage,
		DeviceID: dev.ID,
		Body:     payload,
	})
	logFrameSend(dev.ID, imageID, "inkjoy", err)
	if err == nil {
		h.displayPreview.Clear(dev.MAC)
		h.devices.SetLastImage(dev.ID, imageID, overlayToken)
	}
	return err
}

func (h *Hub) sendSamsungImage(dev *Device, imageID, overlayToken, sendID string, overlayCfg *OverlayConfig, overlayWeather *WeatherSnapshot) error {
	if h.bridgeCoord == nil || !h.bridgeCoord.BridgeOnline(string(DeviceTypeSamsung)) {
		return fmt.Errorf("samsung bridge offline")
	}
	body, err := buildSendImageBody(h, imageID, overlayToken, sendID, overlayCfg, overlayWeather)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	logOutbound("bridge cmd send.image device=%s image=%s crops=%d", dev.ID, imageID, len(body.Crops))
	err = h.bridgeCoord.PublishCommand(string(DeviceTypeSamsung), protocol.CmdPayload{
		Cmd:      protocol.CmdSendImage,
		DeviceID: dev.ID,
		Body:     payload,
	})
	logFrameSend(dev.ID, imageID, "samsung", err)
	if err == nil {
		h.devices.SetLastImage(dev.ID, imageID, overlayToken)
	}
	return err
}

// sendNixplayImage uploads an album image to the Nixplay playlist ("gallery")
// identified by dev.ID. Unlike InkJoy/Samsung, Nixplay is a cloud target: the
// bridge uploads to Nixplay's own S3 bucket, there is no frame pull step, and
// crops/overlay are not applied (Nixplay handles per-frame-model resizing
// server-side).
func (h *Hub) sendNixplayImage(dev *Device, imageID, overlayToken, sendID string, overlayCfg *OverlayConfig, overlayWeather *WeatherSnapshot) error {
	if h.bridgeCoord == nil || !h.bridgeCoord.BridgeOnline(string(DeviceTypeNixplay)) {
		return fmt.Errorf("nixplay bridge offline")
	}
	body, err := buildSendImageBody(h, imageID, overlayToken, sendID, overlayCfg, overlayWeather)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	logOutbound("bridge cmd send.image device=%s image=%s", dev.ID, imageID)
	err = h.bridgeCoord.PublishCommand(string(DeviceTypeNixplay), protocol.CmdPayload{
		Cmd:      protocol.CmdSendImage,
		DeviceID: dev.ID,
		Body:     payload,
	})
	logFrameSend(dev.ID, imageID, "nixplay", err)
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
	if h.bridgeCoord != nil && h.bridgeCoord.BridgeOnline(string(DeviceTypeSamsung)) {
		if err := h.bridgeCoord.PublishCommand(string(DeviceTypeSamsung), protocol.CmdPayload{
			Cmd: protocol.CmdDiscover,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "delegated": "samsung-bridge"})
		return
	}
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
