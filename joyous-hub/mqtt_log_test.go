package main

import (
	"testing"

	"joyous-hub/protocol"
)

func TestMQTTLogBufferRing(t *testing.T) {
	b := NewMQTTLogBuffer(3)
	for i := 0; i < 5; i++ {
		b.AddLocal("frame→hub", "/device/report/AA", []byte(`{"action":"heart"}`), "")
	}
	local, _ := b.Snapshot()
	if len(local) != 3 {
		t.Fatalf("len=%d want 3", len(local))
	}
	if local[0].Action != "heart" || local[2].Action != "heart" {
		t.Fatalf("unexpected entries")
	}
}

func TestFormatMQTTLogBody(t *testing.T) {
	got := formatMQTTLogBody([]byte(`{"action":"play","data":{"port":443}}`))
	if got == "" || got[0] != '{' {
		t.Fatalf("got %q", got)
	}
}

func TestMQTTLogSnapshotSides(t *testing.T) {
	b := NewMQTTLogBuffer(20)
	b.AddLocal("hub→frame", "/inkjoyap/AA", []byte(`{"action":"play"}`), "")
	b.AddUpstream("cloud→hub", "/inkjoyap/AA", []byte(`{"action":"ota"}`), "blocked")
	local, upstream := b.Snapshot()
	if len(local) != 1 || len(upstream) != 1 {
		t.Fatalf("local=%d upstream=%d", len(local), len(upstream))
	}
	if upstream[0].Note != "blocked" {
		t.Fatalf("note=%q", upstream[0].Note)
	}
}

func TestMQTTLogBufferFrameTrafficIsPlainFIFO(t *testing.T) {
	// AddLocal/AddUpstream (frame↔hub traffic) must NOT preferentially evict
	// login/heart — that used to happen unconditionally server-side,
	// independent of the UI's "hide login/heart noise" toggle (which only
	// filters what's already in the buffer for display). That meant login/
	// heart got silently dropped from the buffer regardless of the toggle,
	// well before a user checked. Plain FIFO here; the toggle is the only
	// noise filter now.
	b := NewMQTTLogBuffer(3)
	b.AddLocal("bridge→frame", "/inkjoyap/AA", []byte(`{"action":"play"}`), "")
	for i := 0; i < 5; i++ {
		b.AddLocal("bridge→frame", "/inkjoyap/AA", []byte(`{"action":"heart"}`), "")
	}
	local, _ := b.Snapshot()
	if len(local) != 3 {
		t.Fatalf("len=%d want 3", len(local))
	}
	for _, e := range local {
		if e.Action != "heart" {
			t.Fatalf("expected only the 3 most recent (heart) entries, got: %+v", local)
		}
	}
}

func TestJoyousMQTTLogEvictsNoisyBeforeSendImage(t *testing.T) {
	b := NewMQTTLogBuffer(3)
	sendPayload, _ := protocol.NewEnvelope(protocol.TypeCmd, "inkjoy", protocol.CmdPayload{Cmd: protocol.CmdSendImage})
	syncPayload, _ := protocol.NewEnvelope(protocol.TypeDevices, "inkjoy", protocol.DevicesPayload{})
	b.AddJoyousHubToBridge("joyous/hub/inkjoy/cmd", sendPayload)
	for i := 0; i < 5; i++ {
		b.AddJoyousBridgeToHub("joyous/bridge/inkjoy/devices", syncPayload)
	}
	_, upstream := b.Snapshot()
	hasSend := false
	for _, e := range upstream {
		if e.Action == protocol.TypeCmd+" · "+protocol.CmdSendImage {
			hasSend = true
		}
	}
	if !hasSend {
		t.Fatalf("send.image evicted by devices.sync: %+v", upstream)
	}
}
