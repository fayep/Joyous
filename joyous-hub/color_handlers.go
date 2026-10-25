package main

import (
	"encoding/json"
	"errors"
	"net/http"
)

// handleTransitions serves GET /api/transitions for the device settings: the
// refresh transitions each panel kind supports ("choices"), and each device's
// kind ("devices", only devices that have one; see Device.TransitionKind).
func (h *Hub) handleTransitions(w http.ResponseWriter, r *http.Request) {
	kinds := map[string]string{}
	for _, d := range h.devices.List() {
		if k := d.TransitionKind(); k != "" {
			kinds[d.ID] = k
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"choices": map[string][]WipeChoice{
			TransitionKindInkJoy:   inkJoyWipeChoices(),
			TransitionKindInkJoy28: inkJoy28WipeChoices(h.imagesDataDir()),
		},
		"devices": kinds,
	})
}

// validateTransition checks a transition against the device's panel kind and
// returns it normalized; "" (the standard transition) is always valid.
func validateTransition(dev *Device, transition string) (string, error) {
	kind := dev.TransitionKind()
	if kind == "" {
		return "", errNoTransition
	}
	if transition == "" {
		return "", nil
	}
	if kind == TransitionKindInkJoy28 {
		return normalizeInkJoy28Wipe(transition), validateInkJoy28Wipe(transition)
	}
	return normalizeInkJoyWipe(transition), validateInkJoyWipe(transition)
}

var errNoTransition = errors.New("this panel has no refresh transition")

// imagesDataDir is the hub data dir ("" without an image store).
func (h *Hub) imagesDataDir() string {
	if h.images == nil {
		return ""
	}
	return h.images.dir
}

func (h *Hub) colorPipeline() ColorPipeline { return standardColorPipeline() }

func (h *Hub) colorPipelineForImage(imageID string) ColorPipeline {
	if h.images != nil {
		return h.images.colorPipelineForID(imageID)
	}
	return h.colorPipeline()
}
