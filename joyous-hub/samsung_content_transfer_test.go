//go:build samsungbridge

package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestServeContentTransferProgressEchoesValidBody(t *testing.T) {
	h := &samsungBridgeHTTPHandler{hub: buildTestHub(t)}
	body := []byte(`{"deviceSerialNumber":"SERIAL123","contentId":"B0F2F657D5CD","contentName":"Aviemore","status":"Successful","errorMessage":null,"currentImageStatus":{"imageId":"d9742cb0e71de3bd","imageName":"Aviemore","status":"Successful","progress":100,"errorMessage":null},"totalProgress":100}`)

	status, contentType, _, respBody := h.serveContentTransferProgress(http.MethodPost, body)

	if status != http.StatusOK {
		t.Fatalf("status=%d want 200, body=%s", status, respBody)
	}
	if contentType != "application/json" {
		t.Fatalf("content-type=%q want application/json", contentType)
	}
	if string(respBody) != string(body) {
		t.Fatalf("expected the request body echoed back verbatim (matches the real phone app's server), got %s", respBody)
	}
}

func TestServeContentTransferProgressRejectsWrongMethod(t *testing.T) {
	h := &samsungBridgeHTTPHandler{hub: buildTestHub(t)}
	status, _, _, _ := h.serveContentTransferProgress(http.MethodGet, nil)
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want 405", status)
	}
}

func TestServeContentTransferProgressRejectsInvalidJSON(t *testing.T) {
	h := &samsungBridgeHTTPHandler{hub: buildTestHub(t)}
	status, _, _, _ := h.serveContentTransferProgress(http.MethodPost, []byte("not json"))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", status)
	}
}

// TestServeContentTransferProgressStillAcksOnMissingFields covers a real device sending a
// shape our reverse-engineered field mapping doesn't match (contentId/deviceSerialNumber come
// back empty after parsing): the ack must still succeed with a 200 rather than blocking the
// frame's transfer over a mapping we might not have exactly right — only state-relay to the
// hub is skipped in that case (see TestReportProgressIgnoresUnknownFrame for that half).
func TestServeContentTransferProgressStillAcksOnMissingFields(t *testing.T) {
	h := &samsungBridgeHTTPHandler{hub: buildTestHub(t)}
	for _, body := range []string{
		`{"deviceSerialNumber":"","contentId":"B0F2F657D5CD","status":"Successful","currentImageStatus":{"imageId":"x","imageName":"x","status":"Successful"},"totalProgress":0}`,
		`{"deviceSerialNumber":"SERIAL123","contentId":"","status":"Successful","currentImageStatus":{"imageId":"x","imageName":"x","status":"Successful"},"totalProgress":0}`,
		`{"someUnexpectedShape":true}`,
	} {
		status, _, _, respBody := h.serveContentTransferProgress(http.MethodPost, []byte(body))
		if status != http.StatusOK {
			t.Fatalf("body=%s: status=%d want 200 (must still ack)", body, status)
		}
		if string(respBody) != body {
			t.Fatalf("body=%s: expected echoed back verbatim, got %s", body, respBody)
		}
	}
}

func TestReportProgressUpdatesKnownDeviceLastAction(t *testing.T) {
	hub := buildTestHub(t)
	hub.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.50"})
	dev := hub.devices.FindSamsungByIP("192.168.1.50")
	if dev == nil {
		t.Fatal("expected samsung device to be registered")
	}
	frameID := SamsungFrameID(dev)

	h := &samsungBridgeHTTPHandler{hub: hub}
	progress := samsungContentTransferProgress{
		DeviceSerialNumber: "SERIAL123",
		ContentID:          frameID,
		Status:             "Successful",
		CurrentImageStatus: samsungImageDownloadStatus{ImageID: "x", ImageName: "x", Status: "Successful"},
	}
	body, err := json.Marshal(progress)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	h.serveContentTransferProgress(http.MethodPost, body)

	updated := hub.devices.FindSamsungByIP("192.168.1.50")
	if updated == nil || updated.LastAction != "content_transfer_successful" {
		t.Fatalf("got %+v, want LastAction=content_transfer_successful", updated)
	}
}

func TestReportProgressIgnoresUnknownFrame(t *testing.T) {
	hub := buildTestHub(t)
	h := &samsungBridgeHTTPHandler{hub: hub}
	progress := samsungContentTransferProgress{
		DeviceSerialNumber: "SERIAL123",
		ContentID:          "NOTAREALFRAME",
		Status:             "Successful",
		CurrentImageStatus: samsungImageDownloadStatus{ImageID: "x", ImageName: "x", Status: "Successful"},
	}
	body, err := json.Marshal(progress)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Must not panic when the frame can't be resolved — the ack still succeeds regardless.
	status, _, _, _ := h.serveContentTransferProgress(http.MethodPost, body)
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200 even when the frame can't be resolved", status)
	}
}
