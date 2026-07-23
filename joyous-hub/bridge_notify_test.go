package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"joyous-hub/bridgehub"
	"joyous-hub/protocol"
)

func TestNotifyBridgeDeviceContactIncludesClientIP(t *testing.T) {
	h := buildTestHub(t)
	d := h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})

	coord := bridgehub.NewCoordinator(nil, nil)
	coord.SetCommandPublishObserver(func(bridgeID string, cmd protocol.CmdPayload) {
		if bridgeID != string(DeviceTypeSamsung) {
			t.Errorf("bridgeID=%q", bridgeID)
		}
		if cmd.Cmd != protocol.CmdDeviceTouch || cmd.DeviceID != d.ID {
			t.Errorf("cmd=%+v", cmd)
		}
		var body protocol.DeviceTouchBody
		if err := json.Unmarshal(cmd.Body, &body); err != nil {
			t.Fatal(err)
		}
		if body.Action != "png" || body.ClientIP != "192.168.50.221" {
			t.Fatalf("body=%+v", body)
		}
	})
	payload, err := protocol.NewEnvelope(protocol.TypeHello, "samsung", protocol.HelloPayload{Kind: "samsung"})
	if err != nil {
		t.Fatal(err)
	}
	coord.HandleMessage(protocol.BridgeTopic("samsung", "presence"), payload)
	h.bridgeCoord = coord

	req := samsungFrameHTTPRequest("GET", "/samsung/"+SamsungFrameID(d)+"/image", "192.168.50.221")
	h.notifyBridgeDeviceContact(d.ID, "png", requestClientIP(req))
}

func TestNoteSamsungCachePullNotifiesWhenBridgeOnline(t *testing.T) {
	h := buildTestHub(t)
	frameID := "192-168-1-108"
	d := h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	if err := h.samsung.writePNGLocked(frameID, testPNG()); err != nil {
		t.Fatal(err)
	}

	var got protocol.DeviceTouchBody
	var gotDevice string
	coord := bridgehub.NewCoordinator(nil, nil)
	coord.SetCommandPublishObserver(func(_ string, cmd protocol.CmdPayload) {
		gotDevice = cmd.DeviceID
		_ = json.Unmarshal(cmd.Body, &got)
	})
	payload, err := protocol.NewEnvelope(protocol.TypeHello, "samsung", protocol.HelloPayload{Kind: "samsung"})
	if err != nil {
		t.Fatal(err)
	}
	coord.HandleMessage(protocol.BridgeTopic("samsung", "presence"), payload)
	h.bridgeCoord = coord

	rec := httptest.NewRecorder()
	h.handleSamsungPNG(rec, samsungFrameHTTPRequest("GET", "/samsung/"+frameID+".png", "10.0.0.9"), frameID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if gotDevice != d.ID || got.Action != "png" || got.ClientIP != "10.0.0.9" {
		t.Fatalf("device=%q body=%+v want device=%s action=png ip=10.0.0.9", gotDevice, got, d.ID)
	}
}
