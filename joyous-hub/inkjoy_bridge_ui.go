//go:build inkjoybridge

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"joyous-hub/bridgehub"
	"joyous-hub/inkjoybridge"
)

type inkjoyBridgeUI struct {
	devices  *DeviceRegistry
	srv      *inkjoybridge.Server
	mqttLog  *MQTTLogBuffer
	mqttHost string
	mqttPort int
	mux      *http.ServeMux
}

func newInkJoyBridgeUI(mux *http.ServeMux, devices *DeviceRegistry, srv *inkjoybridge.Server, mqttLog *MQTTLogBuffer, mqttHost string, mqttPort int) *inkjoyBridgeUI {
	ui := &inkjoyBridgeUI{
		devices:  devices,
		srv:      srv,
		mqttLog:  mqttLog,
		mqttHost: mqttHost,
		mqttPort: mqttPort,
		mux:      mux,
	}
	ui.registerRoutes()
	return ui
}

func (ui *inkjoyBridgeUI) Handler() http.Handler { return ui.mux }

func (ui *inkjoyBridgeUI) MQTTHandler() bridgehub.UIHTTPHandler { return ui }

func (ui *inkjoyBridgeUI) ServeUIHTTP(method, path string, headers map[string]string, body []byte) (int, string, map[string]string, []byte) {
	req, err := http.NewRequest(method, path, bytes.NewReader(body))
	if err != nil {
		return http.StatusBadRequest, "text/plain", nil, []byte(err.Error())
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := &responseRecorder{header: make(http.Header)}
	ui.mux.ServeHTTP(rr, req)
	return rr.status, rr.contentType(), rr.headerMap(), rr.body.Bytes()
}

type responseRecorder struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (r *responseRecorder) Header() http.Header { return r.header }

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(b)
}

func (r *responseRecorder) WriteHeader(code int) { r.status = code }

func (r *responseRecorder) contentType() string {
	if ct := r.header.Get("Content-Type"); ct != "" {
		return ct
	}
	return "text/plain; charset=utf-8"
}

func (r *responseRecorder) headerMap() map[string]string {
	out := make(map[string]string, len(r.header))
	for k, vs := range r.header {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

func (ui *inkjoyBridgeUI) registerRoutes() {
	ui.mux.HandleFunc("GET /inkjoy/", ui.handlePage)
	ui.mux.HandleFunc("GET /inkjoy", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/inkjoy/", http.StatusFound)
	})
	ui.mux.HandleFunc("GET /inkjoy/api/devices", ui.handleDevices)
	ui.mux.HandleFunc("PATCH /inkjoy/api/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		ui.handleDevicePatch(w, r, r.PathValue("id"))
	})
	ui.mux.HandleFunc("DELETE /inkjoy/api/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		ui.handleDeviceDelete(w, r, r.PathValue("id"))
	})
	ui.mux.HandleFunc("POST /inkjoy/api/devices/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
		ui.handleRefresh(w, r, r.PathValue("id"))
	})
	ui.mux.HandleFunc("POST /inkjoy/api/devices/{id}/sleep", func(w http.ResponseWriter, r *http.Request) {
		ui.handleSleep(w, r, r.PathValue("id"))
	})
	ui.mux.HandleFunc("POST /inkjoy/api/ble/scan", ui.handleBLEScan)
	ui.mux.HandleFunc("POST /inkjoy/api/ble/adopt", ui.handleBLEAdopt)
	ui.mux.HandleFunc("GET /inkjoy/api/mqtt/logs", ui.handleMQTTLogs)
}

func (ui *inkjoyBridgeUI) handlePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(inkjoyBridgePageHTML))
}

func (ui *inkjoyBridgeUI) inkjoyDevices() []Device {
	all := ui.devices.List()
	out := make([]Device, 0)
	for _, d := range all {
		if d.Type == DeviceTypeInkJoy {
			d.SleepBeginTime = utcHHMMToLocal(d.SleepBeginTime)
			d.SleepEndTime = utcHHMMToLocal(d.SleepEndTime)
			out = append(out, d)
		}
	}
	return out
}

func (ui *inkjoyBridgeUI) handleDevices(w http.ResponseWriter, r *http.Request) {
	devs := ui.inkjoyDevices()
	if devs == nil {
		devs = []Device{}
	}
	writeJSON(w, devs)
}

func (ui *inkjoyBridgeUI) handleDevicePatch(w http.ResponseWriter, r *http.Request, id string) {
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
		found = ui.devices.SetName(id, *body.Name)
	}
	if body.Portrait != nil {
		found = ui.devices.SetPortrait(id, *body.Portrait)
	}
	if !found {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	ui.devices.Save()
	writeJSON(w, map[string]any{"ok": true})
}

func (ui *inkjoyBridgeUI) handleDeviceDelete(w http.ResponseWriter, r *http.Request, id string) {
	if !ui.devices.Delete(id) {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	ui.devices.Save()
	writeJSON(w, map[string]any{"ok": true})
}

func (ui *inkjoyBridgeUI) handleRefresh(w http.ResponseWriter, r *http.Request, id string) {
	dev, ok := ui.devices.Get(id)
	if !ok || dev.Type != DeviceTypeInkJoy {
		http.Error(w, "inkjoy device required", http.StatusBadRequest)
		return
	}
	payload := buildActionPayloadFor(dev.MAC, "image_refresh", nil)
	if err := ui.srv.PublishToFrame(dev.MAC, payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]any{"ok": true})
}

func (ui *inkjoyBridgeUI) handleSleep(w http.ResponseWriter, r *http.Request, id string) {
	dev, ok := ui.devices.Get(id)
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
	utcBegin := localHHMMToUTC(body.BeginTime)
	utcEnd := localHHMMToUTC(body.EndTime)
	payload := buildActionPayloadFor(dev.MAC, "wifi_sleep", map[string]any{
		"beginTime": utcBegin,
		"endTime":   utcEnd,
		"mode":      body.Mode,
	})
	if err := ui.srv.PublishToFrame(dev.MAC, payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ui.devices.UpdateSleep(dev.MAC, utcBegin, utcEnd)
	ui.devices.Save()
	writeJSON(w, map[string]any{"ok": true})
}

func (ui *inkjoyBridgeUI) handleBLEScan(w http.ResponseWriter, r *http.Request) {
	frames, err := ScanBLEFrames(8 * time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, frames)
}

func (ui *inkjoyBridgeUI) handleBLEAdopt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Address string `json:"address"`
		SSID    string `json:"ssid"`
		WifiPwd string `json:"wifi_pwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Address == "" || body.SSID == "" {
		http.Error(w, "address, ssid and wifi_pwd required", http.StatusBadRequest)
		return
	}
	host := ui.mqttHost
	if host == "" {
		host = "127.0.0.1"
	}
	if err := AdoptBLEFrame(body.Address, body.SSID, body.WifiPwd, host, ui.mqttPort, "inkjoy", "inkjoy"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (ui *inkjoyBridgeUI) handleMQTTLogs(w http.ResponseWriter, r *http.Request) {
	local, upstream := ui.mqttLog.Snapshot()
	if local == nil {
		local = []MQTTLogEntry{}
	}
	if upstream == nil {
		upstream = []MQTTLogEntry{}
	}
	writeJSON(w, map[string]any{"local": local, "upstream": upstream})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func bridgeMQTTHost(listenMQTT string) string {
	host, _, err := net.SplitHostPort(listenMQTT)
	if err != nil {
		if strings.Contains(listenMQTT, ":") {
			return "127.0.0.1"
		}
		return listenMQTT
	}
	if host == "" || host == "0.0.0.0" || host == "[::]" {
		return "127.0.0.1"
	}
	return host
}

func bridgeMQTTPort(listenMQTT string) int {
	_, portStr, err := net.SplitHostPort(listenMQTT)
	if err != nil || portStr == "" {
		return DefaultInkJoyFrameMQTTPort
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	if port == 0 {
		return DefaultInkJoyFrameMQTTPort
	}
	return port
}
