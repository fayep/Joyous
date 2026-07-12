//go:build samsungbridge

package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"joyous-hub/bridgehub"
)

// samsungContentTransferProgress mirrors the real Samsung ePaper companion app's
// ContentTransferProgress model, reverse-engineered from the decompiled APK
// (Samsung/epaper-decompiled: ConfigWebServerRoutingKt$configureWebServerRouting$1$5,
// package com.samsung.android.ePaper.data.network.embedded_server.model). A physical Frame
// TV POSTs this to what it believes is the paired phone app's embedded server root — the real
// server there just validates deviceSerialNumber/contentId are non-empty, records the update,
// and echoes the same object back as its 200 response. Without a recognized response the frame
// never completes the transfer and keeps displaying its previous content.
type samsungContentTransferProgress struct {
	DeviceSerialNumber string                     `json:"deviceSerialNumber"`
	ContentID          string                     `json:"contentId"`
	ContentName        string                     `json:"contentName"`
	Status             string                     `json:"status"` // Pending|Downloading|PreparingShowImage|Successful|Failed|Initializing|Unknown|Cancelled
	ErrorMessage       *string                    `json:"errorMessage"`
	CurrentImageStatus samsungImageDownloadStatus `json:"currentImageStatus"`
	TotalProgress      int                        `json:"totalProgress"`
}

type samsungImageDownloadStatus struct {
	ImageID      string  `json:"imageId"`
	ImageName    string  `json:"imageName"`
	Status       string  `json:"status"`
	Progress     *int    `json:"progress"`
	ErrorMessage *string `json:"errorMessage"`
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
	var progress samsungContentTransferProgress
	if err := json.Unmarshal(body, &progress); err != nil {
		log.Printf("samsung-bridge content-transfer-progress: bad body: %v", err)
		return http.StatusBadRequest, "text/plain", nil, []byte("invalid json")
	}
	if progress.ContentID == "" || progress.DeviceSerialNumber == "" {
		log.Printf("samsung-bridge content-transfer-progress: missing contentId/deviceSerialNumber")
		return http.StatusBadRequest, "text/plain", nil, []byte("contentId and deviceSerialNumber required")
	}
	log.Printf("samsung-bridge content-transfer-progress content=%s device_serial=%s status=%s total=%d%%",
		progress.ContentID, progress.DeviceSerialNumber, progress.Status, progress.TotalProgress)

	h.reportProgress(progress)

	// Echo the request body back verbatim — matches the real phone app's embedded server,
	// which re-serializes and returns the exact object it received.
	return http.StatusOK, "application/json", nil, body
}

// reportProgress relays transfer state to the main hub's device registry, the same way
// InkJoy frames' own MQTT state feedback drives their status in the Devices UI.
//
// content.json's manifest "id" (see buildContentJSON in samsung_mdc.go) is the fileID
// recorded via setSamsungPushFileID at push time — but that push happens in *this* bridge
// process while content.json is served by the main hub process (a separate memory space), so
// its lookup misses and it falls back to using the frame's own ID as the manifest id. The
// frame then echoes that back here as contentId, so for the bridge-driven send path (which
// scheduled sends and CmdSendImage both use) contentId is effectively the frame ID.
func (h *samsungBridgeHTTPHandler) reportProgress(progress samsungContentTransferProgress) {
	dev := h.hub.samsungDeviceByFrameID(progress.ContentID)
	if dev == nil || dev.IP == "" {
		return
	}
	action := "content_transfer_" + strings.ToLower(progress.Status)
	h.hub.devices.TouchSamsung(dev.IP, action)
	if h.client != nil {
		_ = h.client.PublishDevices(bridgeDevicesFromRegistry(h.hub.devices, DeviceTypeSamsung))
	}
}
