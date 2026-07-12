package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"joyous-hub/inkjoybridge"
	"joyous-hub/protocol"
)

func buildImageRefreshPayload(mac string) []byte {
	return buildActionPayloadFor(mac, "image_refresh", nil)
}

func buildWifiSleepPayloadFromBody(mac string, body json.RawMessage) []byte {
	var req struct {
		BeginTime string `json:"beginTime"`
		EndTime   string `json:"endTime"`
		Mode      int    `json:"mode"`
	}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &req)
	}
	if req.Mode == 0 {
		req.Mode = 2
	}
	return buildActionPayloadFor(mac, "wifi_sleep", map[string]any{
		"beginTime": req.BeginTime,
		"endTime":   req.EndTime,
		"mode":      req.Mode,
	})
}

// buildActionPayloadFor builds a cloud→frame command payload.
func buildActionPayloadFor(mac, action string, data map[string]any) []byte {
	if data == nil {
		data = map[string]any{}
	}
	msg := map[string]any{
		"action":   action,
		"msgid":    fmt.Sprintf("%d", time.Now().UnixMilli()),
		"stamac":   mac,
		"clientid": mac,
		"data":     data,
	}
	b, _ := json.Marshal(msg)
	return b
}

// handleRedirect sends mqtt_config to redirect a frame (via inkjoy bridge).
func (h *Hub) handleRedirect(w http.ResponseWriter, r *http.Request, deviceID string) {
	dev, ok := h.devices.Get(deviceID)
	if !ok || dev.Type != DeviceTypeInkJoy {
		http.Error(w, "inkjoy device required", http.StatusBadRequest)
		return
	}
	var cfg inkjoybridge.UpstreamConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil || cfg.Host == "" {
		http.Error(w, "body: {host,port,usr,pwd} required", http.StatusBadRequest)
		return
	}
	body, _ := json.Marshal(cfg)
	if h.bridgeCoord != nil {
		if err := h.bridgeCoord.PublishCommand(string(DeviceTypeInkJoy), protocol.CmdPayload{
			Cmd:      protocol.CmdRedirect,
			DeviceID: deviceID,
			Body:     body,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		payload := inkjoybridge.BuildMQTTConfigPayload(dev.MAC, cfg)
		if err := h.publisher.Publish("/inkjoyap/"+dev.MAC, payload); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func localAddr(httpAddr string) string {
	_, port, _ := net.SplitHostPort(httpAddr)
	if port == "" {
		port = "8080"
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "localhost:" + port
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				s := ip4.String()
				if !strings.HasPrefix(s, "169.") {
					return s + ":" + port
				}
			}
		}
	}
	return "localhost:" + port
}
