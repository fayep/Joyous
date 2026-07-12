package bridgehub

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"joyous-hub/protocol"
)

func TestLongRunningBridgeCmd(t *testing.T) {
	if !longRunningBridgeCmd(protocol.CmdSendImage) {
		t.Fatal("send.image should be long-running")
	}
	if longRunningBridgeCmd(protocol.CmdRefresh) {
		t.Fatal("display.refresh should stay on MQTT thread")
	}
}

// TestPresenceHeartbeatDoesNotReLogOnline covers a bug where Client.heartbeat's 15s
// keepalive re-publish of Hello (see publishHello) caused the coordinator to log
// "bridge online" on every heartbeat tick forever, not just on the real online
// transition, spamming the hub log for every connected bridge indefinitely.
func TestPresenceHeartbeatDoesNotReLogOnline(t *testing.T) {
	c := NewCoordinator(nil, nil)

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	sendHello := func() {
		payload, err := protocol.NewEnvelope(protocol.TypeHello, "testbridge", protocol.HelloPayload{Kind: "inkjoy"})
		if err != nil {
			t.Fatalf("NewEnvelope: %v", err)
		}
		c.HandleMessage(protocol.BridgeTopic("testbridge", "presence"), payload)
	}

	sendHello() // initial connect: must log
	sendHello() // heartbeat within TTL: must NOT log again

	onlineCount := strings.Count(buf.String(), "online")
	if onlineCount != 1 {
		t.Fatalf("got %d \"online\" log lines after connect+heartbeat, want 1:\n%s", onlineCount, buf.String())
	}

	// Simulate the bridge having gone quiet past the presence TTL, then reconnecting —
	// this transition must log again.
	c.mu.Lock()
	rec := c.bridges["testbridge"]
	rec.lastSeen = time.Now().Add(-bridgePresenceTTL - time.Second)
	c.bridges["testbridge"] = rec
	c.mu.Unlock()

	sendHello()

	onlineCount = strings.Count(buf.String(), "online")
	if onlineCount != 2 {
		t.Fatalf("got %d \"online\" log lines after re-connect past TTL, want 2:\n%s", onlineCount, buf.String())
	}
}

// fakeDeviceStore is a minimal DeviceStore for tests that need handleBridgeTopic's
// "devices"/"device" cases to actually reach c.store (and thus trigger onDevicesChanged).
type fakeDeviceStore struct{}

func (fakeDeviceStore) SyncBridgeDevices(string, []protocol.BridgeDevice) {}
func (fakeDeviceStore) ApplyBridgeDevice(string, protocol.BridgeDevice)   {}
func (fakeDeviceStore) RemoveBridgeDevice(string, string)                {}

// TestBridgesChangedHandlerFiresOnlyOnRealTransition covers the same "not every 15s
// heartbeat" rule as TestPresenceHeartbeatDoesNotReLogOnline, this time for the new
// SetBridgesChangedHandler hook that lets the hub push a live "bridges" event instead of
// polling for it.
func TestBridgesChangedHandlerFiresOnlyOnRealTransition(t *testing.T) {
	c := NewCoordinator(nil, nil)
	var fired int
	c.SetBridgesChangedHandler(func() { fired++ })

	sendHello := func() {
		payload, err := protocol.NewEnvelope(protocol.TypeHello, "testbridge", protocol.HelloPayload{Kind: "inkjoy"})
		if err != nil {
			t.Fatalf("NewEnvelope: %v", err)
		}
		c.HandleMessage(protocol.BridgeTopic("testbridge", "presence"), payload)
	}

	sendHello() // initial connect: real transition
	sendHello() // heartbeat within TTL: not a transition

	if fired != 1 {
		t.Fatalf("got %d calls, want 1 (only the real online transition)", fired)
	}
}

func TestDevicesChangedHandlerFiresOnSyncAndDelta(t *testing.T) {
	c := NewCoordinator(nil, fakeDeviceStore{})
	var fired int
	c.SetDevicesChangedHandler(func() { fired++ })

	syncPayload, err := protocol.NewEnvelope(protocol.TypeDevices, "testbridge", protocol.DevicesPayload{})
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	c.HandleMessage(protocol.BridgeTopic("testbridge", "devices"), syncPayload)

	deltaPayload, err := protocol.NewEnvelope(protocol.TypeDevice, "testbridge", protocol.DevicePayload{})
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	c.HandleMessage(protocol.BridgeTopic("testbridge", "device"), deltaPayload)

	if fired != 2 {
		t.Fatalf("got %d calls, want 2 (one per devices sync, one per device delta)", fired)
	}
}

func TestDevicesChangedHandlerNotCalledWithoutHandler(t *testing.T) {
	// Must not panic when no handler is registered (the common case — most Coordinators
	// never call SetDevicesChangedHandler).
	c := NewCoordinator(nil, fakeDeviceStore{})
	payload, err := protocol.NewEnvelope(protocol.TypeDevices, "testbridge", protocol.DevicesPayload{})
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	c.HandleMessage(protocol.BridgeTopic("testbridge", "devices"), payload)
}

// TestCheckStaleBridgesFiresOnceOnOfflineTransition covers a bridge going silent past the TTL:
// nothing else observes that transition (online status is otherwise only computed lazily), so
// without this sweep an already-connected session would never learn a bridge went offline.
func TestCheckStaleBridgesFiresOnceOnOfflineTransition(t *testing.T) {
	c := NewCoordinator(nil, nil)
	var fired int
	c.SetBridgesChangedHandler(func() { fired++ })

	c.mu.Lock()
	c.bridges["testbridge"] = bridgeRecord{
		hello:    protocol.HelloPayload{Kind: "inkjoy"},
		lastSeen: time.Now().Add(-bridgePresenceTTL - time.Second),
	}
	c.mu.Unlock()

	c.checkStaleBridges()
	c.checkStaleBridges() // a second sweep tick while still offline must not re-fire

	if fired != 1 {
		t.Fatalf("got %d calls, want exactly 1", fired)
	}
}

func TestCheckStaleBridgesIgnoresOnlineBridges(t *testing.T) {
	c := NewCoordinator(nil, nil)
	var fired int
	c.SetBridgesChangedHandler(func() { fired++ })

	c.mu.Lock()
	c.bridges["testbridge"] = bridgeRecord{hello: protocol.HelloPayload{Kind: "inkjoy"}, lastSeen: time.Now()}
	c.mu.Unlock()

	c.checkStaleBridges()

	if fired != 0 {
		t.Fatalf("got %d calls, want 0 for a still-online bridge", fired)
	}
}

// TestBridgeForPath covers the hub's catch-all HTTP route (see Hub.handleStatic in web.go)
// resolving which bridge owns an extra vendor-protocol path it declared via
// HelloPayload.HTTPPaths, instead of falling through to serving the SPA.
func TestBridgeForPath(t *testing.T) {
	c := NewCoordinator(nil, nil)

	if _, ok := c.BridgeForPath("/content-transfer-progress"); ok {
		t.Fatal("no bridge connected yet: expected no match")
	}

	sendHello := func(bridgeID string, paths []string) {
		payload, err := protocol.NewEnvelope(protocol.TypeHello, bridgeID, protocol.HelloPayload{Kind: "samsung", HTTPPaths: paths})
		if err != nil {
			t.Fatalf("NewEnvelope: %v", err)
		}
		c.HandleMessage(protocol.BridgeTopic(bridgeID, "presence"), payload)
	}
	sendHello("samsung", []string{"/content-transfer-progress", "/device-thumbnail"})

	id, ok := c.BridgeForPath("/content-transfer-progress")
	if !ok || id != "samsung" {
		t.Fatalf("got (%q, %v), want (\"samsung\", true)", id, ok)
	}
	if _, ok := c.BridgeForPath("/device-thumbnail"); !ok {
		t.Fatal("expected /device-thumbnail to match too")
	}
	if _, ok := c.BridgeForPath("/not-registered"); ok {
		t.Fatal("expected no match for an unregistered path")
	}

	// Once the bridge's presence goes stale past the TTL, its paths must stop matching —
	// mirrors BridgeOnline's TTL semantics so an offline bridge doesn't keep claiming a path
	// forever.
	c.mu.Lock()
	rec := c.bridges["samsung"]
	rec.lastSeen = time.Now().Add(-bridgePresenceTTL - time.Second)
	c.bridges["samsung"] = rec
	c.mu.Unlock()
	if _, ok := c.BridgeForPath("/content-transfer-progress"); ok {
		t.Fatal("expected no match once the owning bridge is stale/offline")
	}
}
