package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"joyous-hub/inkjoybridge"
)

func patchDevice(h *Hub, id, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.handleDevicePatch(rec, httptest.NewRequest("PATCH", "/api/devices/"+id, strings.NewReader(body)), id)
	return rec
}

// Only the real InkJoy panels have a refresh transition, each from its own
// set; the clones draw PNGs and take none.
func TestDeviceTransitionFollowsPanel(t *testing.T) {
	h := buildTestHub(t)
	const ten, big, clone = "AABBCCDDEE10", "AABBCCDDEE28", "AABBCCDDEE13"
	for _, tc := range []struct {
		id    string
		login inkjoybridge.LoginInfo
		kind  string
	}{
		{ten, inkjoybridge.LoginInfo{HardwareModel: inkjoybridge.HardwareModelInkJoy10, Panel: "10"}, TransitionKindInkJoy},
		{big, inkjoybridge.LoginInfo{HardwareModel: inkjoybridge.HardwareModelInkJoy28, Panel: "28.5"}, TransitionKindInkJoy28},
		{clone, inkjoybridge.LoginInfo{HardwareModel: inkjoybridge.HardwareModelSeeed}, ""},
	} {
		h.devices.MarkConnected(tc.id)
		h.devices.UpdateLogin(tc.id, tc.login)
		d, _ := h.devices.Get(tc.id)
		if got := d.TransitionKind(); got != tc.kind {
			t.Fatalf("%s: kind %q, want %q", tc.id, got, tc.kind)
		}
	}

	for _, tc := range []struct {
		id, body string
		code     int
		want     string
	}{
		{ten, `{"transition":"wipe_clock.png"}`, 200, "wipe_clock.png"},
		{ten, `{"transition":"captured:art"}`, 400, "wipe_clock.png"},
		{ten, `{"transition":""}`, 200, ""},
		{big, `{"transition":"off"}`, 200, Wipe28Off},
		{big, `{"transition":"wipe_clock.png"}`, 400, Wipe28Off},
		{clone, `{"transition":"luminance"}`, 400, ""},
	} {
		if rec := patchDevice(h, tc.id, tc.body); rec.Code != tc.code {
			t.Fatalf("%s %s: HTTP %d %s", tc.id, tc.body, rec.Code, rec.Body)
		}
		if d, _ := h.devices.Get(tc.id); d.Transition != tc.want {
			t.Fatalf("%s %s: stored %q, want %q", tc.id, tc.body, d.Transition, tc.want)
		}
	}
}

// The hub hands the device's transition to the bridge with each send.
func TestSendBodyCarriesTransition(t *testing.T) {
	h := buildTestHub(t)
	id, err := h.images.Store(bytesReader(makeTestPNG()), "test.png")
	if err != nil {
		t.Fatal(err)
	}
	dev := &Device{ID: "AABBCCDDEE10", Type: DeviceTypeInkJoy, Transition: WipeRandom}
	body, err := buildSendImageBody(h, dev, id, "", "send", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if body.Transition != WipeRandom {
		t.Fatalf("transition %q", body.Transition)
	}
}
