//go:build samsungbridge

package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"

	"joyous-hub/bridgehub"
)

// samsungContentTransferProgress is the real payload shape a physical Frame TV POSTs — this
// diverges from the decompiled phone-companion-app's ContentTransferProgress model
// (Samsung/epaper-decompiled: ConfigWebServerRoutingKt$configureWebServerRouting$1$5) in both
// field names (snake_case, not camelCase) and a couple of types; the TV firmware evidently
// implements its own client independent of the phone app's Kotlin model. Confirmed against a
// real transfer's logged raw body:
//
//	{
//	  "content_id": "B0F2F657D5CD", "content_name": "joyous-hub",
//	  "current_image_status": {
//	    "error_message": "", "image_id": "B0F2F657D5CD", "image_name": "B0F2F657D5CD.png",
//	    "progress": "100", "status": "Successful"
//	  },
//	  "device_id": "0WPSHNPY800618B", "error_message": "", "progress": "100", "status": "Successful"
//	}
//
// content_id is the same string the hub told the frame to fetch in content.json's manifest
// "id" (see buildContentJSON in samsung_mdc.go and reportProgress below) — for the bridge-driven
// send path (CmdSendImage / scheduled sends) that ends up being the frame's own ID. progress is
// a quoted string on the wire, not a number. There is no top-level "total_progress" — top-level
// and nested "progress" share the same key name.
//
// The TV never completes the transfer (and keeps displaying its previous content) without a
// 200 response here, so ServeUIHTTP always acks regardless of whether this mapping matches.
type samsungContentTransferProgress struct {
	ContentID          string                     `json:"content_id"`
	ContentName        string                     `json:"content_name"`
	CurrentImageStatus samsungImageDownloadStatus `json:"current_image_status"`
	DeviceID           string                     `json:"device_id"`
	ErrorMessage       string                     `json:"error_message"`
	Progress           string                     `json:"progress"` // quoted on the wire, e.g. "100"
	Status             string                     `json:"status"`   // Downloading|Successful|Failed|… (observed values only, not exhaustively confirmed)
}

type samsungImageDownloadStatus struct {
	ErrorMessage string `json:"error_message"`
	ImageID      string `json:"image_id"`
	ImageName    string `json:"image_name"`
	Progress     string `json:"progress"` // quoted on the wire, e.g. "100"
	Status       string `json:"status"`
}

// samsungBridgeHTTPHandler serves the extra root-level HTTP paths this bridge registers via
// HelloPayload.HTTPPaths (see BridgeForPath in bridgehub/coordinator.go) — vendor protocol
// callbacks a physical frame makes to what it believes is a server root, not namespaced under
// the bridge's own /samsung/ proxy prefix.
type samsungBridgeHTTPHandler struct {
	hub *Hub
	// client is nil until bridgehub.Connect returns (set by main() right after); a
	// content-transfer-progress POST arriving in that narrow startup window still gets
	// correctly acked, it just won't relay device state to the hub that one time.
	client *bridgehub.Client
}

func (h *samsungBridgeHTTPHandler) ServeUIHTTP(method, path string, _ map[string]string, body []byte) (status int, contentType string, respHeaders map[string]string, respBody []byte) {
	switch path {
	case "/content-transfer-progress":
		return h.serveContentTransferProgress(method, body)
	default:
		return http.StatusNotFound, "text/plain", nil, []byte("not found")
	}
}

// serveContentTransferProgress acks the frame's transfer-progress callback so it completes
// the transfer and switches its displayed art. See the samsungContentTransferProgress doc
// comment for why an unrecognized response leaves the frame showing stale content.
func (h *samsungBridgeHTTPHandler) serveContentTransferProgress(method string, body []byte) (int, string, map[string]string, []byte) {
	if method != http.MethodPost {
		return http.StatusMethodNotAllowed, "text/plain", nil, []byte("method not allowed")
	}
	// Always log the raw body, unconditionally — the real phone app's server does the same
	// (see "RAW REQUEST BODY: " in the decompiled handler) and it's the only way to nail the
	// frame's exact field shape if samsungContentTransferProgress's reverse-engineered mapping
	// turns out to be wrong for a given firmware version.
	log.Printf("samsung-bridge content-transfer-progress: raw body: %s", body)

	var progress samsungContentTransferProgress
	if err := json.Unmarshal(body, &progress); err != nil {
		log.Printf("samsung-bridge content-transfer-progress: invalid json: %v", err)
		return http.StatusBadRequest, "text/plain", nil, []byte("invalid json")
	}
	if progress.ContentID == "" || progress.DeviceID == "" {
		// Don't block the ack on this — if our field-name mapping doesn't match this
		// firmware's actual shape, failing the request here would leave the frame worse off
		// than before (still no valid transfer completion). Log for diagnosis and still echo
		// a 200 below; only state-relay (reportProgress) is skipped.
		log.Printf("samsung-bridge content-transfer-progress: parsed but content_id/device_id empty, parsed=%+v", progress)
	} else {
		log.Printf("samsung-bridge content-transfer-progress content=%s device=%s status=%s progress=%s",
			progress.ContentID, progress.DeviceID, progress.Status, progress.Progress)
		h.reportProgress(progress)
	}

	// Echo the request body back verbatim — matches the real phone app's embedded server,
	// which re-serializes and returns the exact object it received.
	return http.StatusOK, "application/json", nil, body
}

// reportProgress relays transfer state to the main hub's device registry, the same way
// InkJoy frames' own MQTT state feedback drives their status in the Devices UI.
//
// content.json's manifest "id" (samsungContentFileID in samsung_mdc.go) is derived from the
// frame id and its current PNG content, not looked up from any state recorded at push time —
// see that function's doc comment for why. The frame echoes that id back here as content_id, so
// resolving it back to a device means recomputing the same function for each known frame (the
// bridge and the hub share the same PNG files on disk — see hub_data_dir) and matching against
// content_id, rather than treating content_id as if it were the frame id directly.
func (h *samsungBridgeHTTPHandler) reportProgress(progress samsungContentTransferProgress) {
	dev := h.resolveSamsungFrameByContentID(progress.ContentID)
	if dev == nil || dev.IP == "" {
		return
	}
	action := "content_transfer_" + strings.ToLower(progress.Status)
	h.hub.devices.TouchSamsung(dev.IP, action)
	if h.client != nil {
		_ = h.client.PublishDevices(bridgeDevicesFromRegistry(h.hub.devices, DeviceTypeSamsung))
	}
}

// resolveSamsungFrameByContentID finds which known frame a content-transfer-progress callback's
// content_id belongs to by recomputing samsungContentFileID for each frame's current PNG and
// matching — see reportProgress for why this can't be a simple map lookup.
func (h *samsungBridgeHTTPHandler) resolveSamsungFrameByContentID(contentID string) *Device {
	if contentID == "" || h.hub.samsung == nil {
		return nil
	}
	frameIDs, err := h.hub.samsung.ListFrames()
	if err != nil {
		return nil
	}
	for _, frameID := range frameIDs {
		data, err := os.ReadFile(h.hub.samsung.pngPath(frameID))
		if err != nil {
			continue
		}
		if samsungContentFileID(frameID, data) == contentID {
			return h.hub.samsungDeviceByFrameID(frameID)
		}
	}
	return nil
}
