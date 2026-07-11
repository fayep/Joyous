package main

import (
	"encoding/json"
	"net/http"

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
	if h.bridgeCoord == nil {
		json.NewEncoder(w).Encode(map[string]any{"bridges": []any{}})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"bridges": h.bridgeCoord.ListBridges()})
}
