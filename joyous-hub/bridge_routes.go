package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"joyous-hub/protocol"
)

func registerBridgeRoutes(mux *http.ServeMux, hub *Hub) {
	mux.HandleFunc("GET /api/bridge/{kind}/ui", func(w http.ResponseWriter, r *http.Request) {
		hub.handleBridgeUI(w, r, r.PathValue("kind"))
	})
	mux.HandleFunc("POST /api/bridge/{kind}/ui", func(w http.ResponseWriter, r *http.Request) {
		hub.handleBridgeUIAction(w, r, r.PathValue("kind"))
	})
	mux.HandleFunc("GET /api/bridges", hub.handleBridgesList)

	methods := []string{"GET", "POST", "PATCH", "DELETE", "PUT"}
	for _, bridgeID := range protocol.BridgeKinds() {
		for _, method := range methods {
			mux.HandleFunc(fmt.Sprintf("%s /%s/{path...}", method, bridgeID), bridgeProxyHandler(hub, bridgeID))
		}
		mux.HandleFunc("GET /"+bridgeID, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/"+bridgeID+"/", http.StatusFound)
		})
	}
}

func bridgeProxyHandler(hub *Hub, bridgeID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hub.handleBridgeProxy(w, r, bridgeID, r.PathValue("path"))
	}
}

func (h *Hub) handleBridgeProxy(w http.ResponseWriter, r *http.Request, bridgeID, subPath string) {
	if !protocol.IsBridgeKind(bridgeID) {
		http.NotFound(w, r)
		return
	}
	if bridgeID == protocol.KindInkJoy && isInkJoyFrameBinProxyPath(subPath) {
		log.Printf("access: rejected inkjoy bin MQTT proxy %s %s (serve from hub cache)", r.Method, r.URL.Path)
		http.Error(w, "InkJoy frame .bin files are served by the hub cache, not the bridge MQTT tunnel", http.StatusNotFound)
		return
	}
	if bridgeID == protocol.KindSamsung && isSamsungFramePullProxyPath(subPath) {
		log.Printf("access: rejected samsung frame-pull MQTT proxy %s %s (serve from hub cache)", r.Method, r.URL.Path)
		http.Error(w, "Samsung frame pull routes are served by the hub cache, not the bridge MQTT tunnel", http.StatusNotFound)
		return
	}
	bridgePath := "/" + bridgeID + "/"
	if subPath != "" {
		bridgePath = "/" + bridgeID + "/" + subPath
	}
	h.proxyBridgeHTTP(w, r, bridgeID, bridgePath)
}

func (h *Hub) handleBridgeUI(w http.ResponseWriter, r *http.Request, kind string) {
	if h.bridgeCoord == nil {
		http.Error(w, "bridge coordinator unavailable", http.StatusServiceUnavailable)
		return
	}
	state, ok := h.bridgeCoord.UIState(kind)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"revision": 0, "state": map[string]any{}})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

func (h *Hub) handleBridgeUIAction(w http.ResponseWriter, r *http.Request, kind string) {
	if h.bridgeCoord == nil {
		http.Error(w, "bridge coordinator unavailable", http.StatusServiceUnavailable)
		return
	}
	var action protocol.UIActionPayload
	if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.bridgeCoord.PublishUIAction(kind, action); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (h *Hub) handleBridgesList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if h.bridgeCoord == nil {
		json.NewEncoder(w).Encode(map[string]any{"bridges": []any{}})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"bridges": h.bridgeCoord.ListBridgeStatus()})
}

func (h *Hub) proxyBridgeHTTP(w http.ResponseWriter, r *http.Request, bridgeID, bridgePath string) {
	if h.bridgeCoord == nil {
		http.Error(w, "bridge coordinator unavailable", http.StatusServiceUnavailable)
		return
	}

	body, _ := io.ReadAll(r.Body)
	headers := map[string]string{}
	for k, vs := range r.Header {
		if len(vs) == 0 {
			continue
		}
		kl := strings.ToLower(k)
		if kl == "host" || kl == "connection" || kl == "content-length" {
			continue
		}
		headers[k] = vs[0]
	}

	timeout := 20 * time.Second
	if strings.Contains(bridgePath, "/ble/") {
		timeout = 90 * time.Second
	} else if bridgeID == protocol.KindInkJoy && strings.Contains(bridgePath, "/inkjoy/api/") {
		timeout = 45 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	resp, err := h.bridgeCoord.ProxyBridgeHTTP(ctx, bridgeID, protocol.UIHTTPRequestPayload{
		Method:  r.Method,
		Path:    bridgePath,
		Headers: headers,
		Body:    body,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	if ct := resp.ContentType; ct != "" && w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", ct)
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(resp.Body)
}
