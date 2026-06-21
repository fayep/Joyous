package main

import (
	"testing"
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
