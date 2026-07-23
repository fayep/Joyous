package main

import (
	"testing"
	"time"

	"joyous-hub/protocol"
)

func TestApplySamsungDeviceTouchIgnoresHubClientIP(t *testing.T) {
	h := buildTestHub(t)
	h.hubIP = "192.168.51.7"
	d := h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.50.221", Server: "Samsung MDC"})

	applySamsungDeviceTouch(h, d.ID, protocol.DeviceTouchBody{
		Action:   "png",
		ClientIP: "192.168.51.7", // hub/bridge ProbeSamsungHubURL
	})

	got, ok := h.devices.Get(d.ID)
	if !ok {
		t.Fatal("device missing")
	}
	if got.IP != "192.168.50.221" {
		t.Fatalf("IP overwritten to hub address: got %q", got.IP)
	}
}

func TestApplySamsungDeviceTouchLearnsClientIP(t *testing.T) {
	h := buildTestHub(t)
	d := h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.50.108", Server: "Samsung MDC"})
	h.devices.mu.Lock()
	h.devices.m[d.ID].LastSeen = time.Now().Add(-time.Hour)
	h.devices.m[d.ID].IP = "" // deep-sleep cleared IP
	h.devices.mu.Unlock()

	applySamsungDeviceTouch(h, d.ID, protocol.DeviceTouchBody{
		Action:   "content.json",
		ClientIP: "192.168.50.221",
	})

	got, ok := h.devices.Get(d.ID)
	if !ok {
		t.Fatal("device missing")
	}
	if got.IP != "192.168.50.221" {
		t.Fatalf("IP=%q want 192.168.50.221", got.IP)
	}
	if got.LastAction != "content.json" {
		t.Fatalf("LastAction=%q want content.json", got.LastAction)
	}
	ApplySamsungConnected(got)
	if !got.Connected {
		t.Fatal("frame should be active after device.touch")
	}
}

func TestApplySamsungDeviceTouchPNGCancelsSleepSeq(t *testing.T) {
	h := buildTestHub(t)
	d := h.devices.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	seqBefore := bumpSleepAfterPushSeq(d.IP)
	applySamsungDeviceTouch(h, d.ID, protocol.DeviceTouchBody{
		Action:   "png",
		ClientIP: d.IP,
	})
	if currentSleepAfterPushSeq(d.IP) == seqBefore {
		t.Fatal("png pull should bump sleep-after-push seq")
	}
}
