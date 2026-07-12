package main

import (
	"testing"
	"time"

	"joyous-hub/protocol"
)

func TestSyncBridgeDevicesPreservesFresherHubContact(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	d := reg.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	reg.mu.Lock()
	reg.m[d.ID].bridgeID = protocol.KindSamsung
	reg.mu.Unlock()

	// Simulate hub clearing deep sleep after an HTTP pull from the frame.
	reg.mu.Lock()
	reg.m[d.ID].DeepSleepActive = false
	reg.mu.Unlock()
	reg.TouchSamsung("192.168.1.108", "content.json")

	stale := time.Now().Add(-time.Hour)
	reg.SyncBridgeDevices(protocol.KindSamsung, []protocol.BridgeDevice{{
		ID:              d.ID,
		Type:            string(DeviceTypeSamsung),
		IP:              "192.168.1.108",
		Connected:       false,
		LastSeen:        stale,
		LastAction:      "mdc_deep_sleep",
		DeepSleepActive: true,
	}})

	got, ok := reg.Get(d.ID)
	if !ok {
		t.Fatal("device missing after sync")
	}
	if !got.LastSeen.After(stale) {
		t.Fatalf("hub LastSeen wiped: got %v stale %v", got.LastSeen, stale)
	}
	if got.LastAction != "content.json" {
		t.Fatalf("LastAction=%q want content.json", got.LastAction)
	}
	if got.DeepSleepActive {
		t.Fatal("hub-cleared deep sleep should not be restored by stale bridge snapshot")
	}
	ApplySamsungConnected(got)
	if !got.Connected {
		t.Fatal("frame should stay active after devices.sync")
	}
}

func TestSyncBridgeDevicesUsesFresherBridgeContact(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	d := reg.UpsertSamsung(SSDPDevice{IP: "192.168.1.108", Server: "Samsung MDC"})
	reg.mu.Lock()
	reg.m[d.ID].bridgeID = protocol.KindSamsung
	reg.m[d.ID].LastSeen = time.Now().Add(-time.Hour)
	reg.m[d.ID].LastAction = "content.json"
	reg.mu.Unlock()

	fresh := time.Now()
	reg.SyncBridgeDevices(protocol.KindSamsung, []protocol.BridgeDevice{{
		ID:         d.ID,
		Type:       string(DeviceTypeSamsung),
		IP:         "192.168.1.108",
		LastSeen:   fresh,
		LastAction: "mdc_push",
	}})

	got, ok := reg.Get(d.ID)
	if !ok {
		t.Fatal("missing")
	}
	if got.LastAction != "mdc_push" {
		t.Fatalf("LastAction=%q want mdc_push", got.LastAction)
	}
	ApplySamsungConnected(got)
	if !got.Connected {
		t.Fatal("expected connected from bridge push")
	}
}
