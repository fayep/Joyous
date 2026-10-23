package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// handleDeviceColorGet serves GET /api/devices/{id}/color — the per-device ink
// palette override (nil if the device just follows the bridge default) plus
// the bridge's current P2 dither-target palette for this device's type, so
// the UI can pre-fill unset fields with the value they'd otherwise resolve to.
// This is always the P2 (dithering input) palette — InkJoyDisplay or
// SamsungDisplay — never the fixed P1/output palette the panel firmware
// itself expects (SamsungSend, or InkJoy's fixed hi-byte ink indices).
func (h *Hub) handleDeviceColorGet(w http.ResponseWriter, r *http.Request, id string) {
	dev, ok := h.devices.Get(id)
	if !ok {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	pipe := h.colorPipeline()
	bridgeDefault := pipe.InkJoyDisplay[:]
	names := dev.InkNames()
	switch {
	case dev.Type == DeviceTypeSamsung:
		bridgeDefault = pipe.SamsungDisplay[:]
	case len(names) == len(paletteInkJoy28Display):
		// 8-ink panel: the measured 28.5" set (the only 8-ink measurement so
		// far), not the 10"'s 6.
		bridgeDefault = paletteInkJoy28Display[:]
	case dev.DeviceReportedP2 != nil:
		// Non-Samsung device that reported its own P2 at login (e.g. a Seeed
		// clone, whose panel doesn't match the real-InkJoy-panel default) —
		// pre-fill from that instead of the generic InkJoy display palette.
		p := paletteFromRGB(*dev.DeviceReportedP2)
		bridgeDefault = p[:]
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"names":          names,
		"bridge_default": paletteSliceToJSON(bridgeDefault),
		"override":       dev.ColorOverride,
	})
}

// handleDeviceColorPut serves PUT /api/devices/{id}/color. Body {"palette": [[r,g,b], ...]},
// one entry per panel ink (len(dev.InkNames()): 6, or 8 for the 28.5"), saves a
// whole-palette snapshot override; {"palette": null} clears it, reverting
// the device to the bridge's default. Either way, that device's cached .bin
// renders are invalidated so the next send re-encodes with the new palette.
func (h *Hub) handleDeviceColorPut(w http.ResponseWriter, r *http.Request, id string) {
	dev, ok := h.devices.Get(id)
	if !ok {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	var body struct {
		Palette []PaletteRGB `json:"palette"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.Palette != nil {
		if want := len(dev.InkNames()); len(body.Palette) != want {
			http.Error(w, fmt.Sprintf("palette needs %d inks for this panel (%s), got %d",
				want, strings.Join(dev.InkNames(), ", "), len(body.Palette)), http.StatusBadRequest)
			return
		}
		for _, c := range body.Palette {
			for _, v := range c {
				if v < 0 || v > 255 {
					http.Error(w, "RGB values must be between 0 and 255", http.StatusBadRequest)
					return
				}
			}
		}
	}
	h.devices.SetColorOverride(id, body.Palette)
	h.devices.Save()
	if h.images != nil {
		_ = h.images.ClearDeviceBinCache(id)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
