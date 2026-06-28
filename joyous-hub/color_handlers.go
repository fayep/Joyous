package main

import (
	"encoding/json"
	"net/http"
)

func (h *Hub) handleColorGet(w http.ResponseWriter, r *http.Request) {
	if h.color == nil {
		http.Error(w, "color not configured", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.color.Config())
}

func (h *Hub) handleColorPut(w http.ResponseWriter, r *http.Request) {
	if h.color == nil {
		http.Error(w, "color not configured", http.StatusInternalServerError)
		return
	}
	var cfg ColorConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := h.color.Save(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.images != nil {
		_ = h.images.ClearBinCache()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (h *Hub) handleColorPresets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"presets": colorPresetCatalog(),
		"names":   colorNames,
	})
}

func (h *Hub) colorPipeline() ColorPipeline {
	if h.color != nil {
		return h.color.Pipeline()
	}
	return defaultColorPipeline()
}

func (h *Hub) colorPipelineForImage(imageID string) ColorPipeline {
	if h.images != nil {
		return h.images.colorPipelineForID(imageID)
	}
	return h.colorPipeline()
}
