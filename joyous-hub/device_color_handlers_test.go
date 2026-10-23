package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"joyous-hub/inkjoybridge"
	"joyous-hub/protocol"
)

func getDeviceColor(t *testing.T, h *Hub, id string) (names []string, def []PaletteRGB, override []PaletteRGB) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.handleDeviceColorGet(rec, httptest.NewRequest("GET", "/api/devices/"+id+"/color", nil), id)
	if rec.Code != 200 {
		t.Fatalf("GET %s: %d %s", id, rec.Code, rec.Body.String())
	}
	var body struct {
		Names         []string     `json:"names"`
		BridgeDefault []PaletteRGB `json:"bridge_default"`
		Override      []PaletteRGB `json:"override"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Names, body.BridgeDefault, body.Override
}

func putDeviceColor(h *Hub, id, palette string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.handleDeviceColorPut(rec, httptest.NewRequest("PUT", "/api/devices/"+id+"/color",
		strings.NewReader(`{"palette":`+palette+`}`)), id)
	return rec
}

func paletteJSON(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = "[10,20,30]"
	}
	return "[" + strings.Join(rows, ",") + "]"
}

// The override is sized to the panel: 8 inks on the 28.5", 6 elsewhere.
func TestDeviceColorOverrideFollowsPanelInkCount(t *testing.T) {
	h := buildTestHub(t)
	const ten, big = "AABBCCDDEE10", "AABBCCDDEE28"
	h.devices.MarkConnected(ten)
	h.devices.MarkConnected(big)
	h.devices.UpdateLogin(big, inkjoybridge.LoginInfo{HardwareModel: inkjoybridge.HardwareModelInkJoy28, Panel: "28.5"})

	for _, tc := range []struct {
		id         string
		inks       int
		defaultPal [][3]float64
	}{
		{ten, 6, PaletteInkJoyDisplay[:]},
		{big, 8, paletteInkJoy28Display[:]},
	} {
		names, def, override := getDeviceColor(t, h, tc.id)
		if len(names) != tc.inks || len(def) != tc.inks || override != nil {
			t.Fatalf("%s: names=%v default=%v override=%v, want %d inks and no override", tc.id, names, def, override, tc.inks)
		}
		for i, c := range def {
			if want := tc.defaultPal[i]; float64(c[0]) != want[0] || float64(c[1]) != want[1] || float64(c[2]) != want[2] {
				t.Fatalf("%s: default[%d]=%v, want %v", tc.id, i, c, want)
			}
		}
		for _, n := range []int{6, 8} {
			if n == tc.inks {
				continue
			}
			if rec := putDeviceColor(h, tc.id, paletteJSON(n)); rec.Code != 400 {
				t.Fatalf("%s: PUT %d inks = %d, want 400", tc.id, n, rec.Code)
			}
		}
		if rec := putDeviceColor(h, tc.id, paletteJSON(tc.inks)); rec.Code != 200 {
			t.Fatalf("%s: PUT %d inks = %d %s", tc.id, tc.inks, rec.Code, rec.Body.String())
		}
		if _, _, override = getDeviceColor(t, h, tc.id); len(override) != tc.inks {
			t.Fatalf("%s: stored override %v, want %d inks", tc.id, override, tc.inks)
		}
		if rec := putDeviceColor(h, tc.id, "null"); rec.Code != 200 {
			t.Fatalf("%s: reset = %d", tc.id, rec.Code)
		}
		if _, _, override = getDeviceColor(t, h, tc.id); override != nil {
			t.Fatalf("%s: override after reset %v", tc.id, override)
		}
	}
}

// Each renderer only takes an override sized for its own panel.
func TestPaletteOverrideResolution(t *testing.T) {
	six, eight := make([]PaletteRGB, 6), make([]PaletteRGB, 8)
	for i := range eight {
		eight[i] = PaletteRGB{i, 2 * i, 300} // 300 clamps to 255
	}
	copy(six, eight)

	if got := inkjoy28Palette(eight); len(got) != 8 || got[7] != [3]float64{7, 14, 255} {
		t.Fatalf("28.5\" with 8-ink override: %v", got)
	}
	for _, o := range [][]PaletteRGB{nil, six} {
		if got := inkjoy28Palette(o); &got[0] != &paletteInkJoy28Display[0] {
			t.Fatalf("28.5\" with %d-ink override should keep the measured default, got %v", len(o), got)
		}
	}
	if p, ok := paletteOverride6(six); !ok || p[5] != [3]float64{5, 10, 255} {
		t.Fatalf("6-ink override: %v %v", p, ok)
	}
	for _, o := range [][]PaletteRGB{nil, eight} {
		if _, ok := paletteOverride6(o); ok {
			t.Fatalf("%d-ink override applied to a 6-ink panel", len(o))
		}
	}
}

// devices.json written when the override was a fixed [6] array still loads.
func TestDeviceColorOverrideJSONCompat(t *testing.T) {
	var d Device
	if err := json.Unmarshal([]byte(`{"id":"x","color_override":[[1,2,3],[4,5,6],[7,8,9],[1,1,1],[2,2,2],[3,3,3]]}`), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.ColorOverride) != 6 || d.ColorOverride[2] != (PaletteRGB{7, 8, 9}) {
		t.Fatalf("override %v", d.ColorOverride)
	}
	if out, _ := json.Marshal(Device{ID: "y"}); strings.Contains(string(out), "color_override") {
		t.Fatalf("nil override should be omitted: %s", out)
	}
}

// The hub learns a bridge-owned frame's panel from the bridge's device sync,
// so its colour override is sized for the right panel.
func TestBridgeSyncCarriesPanelForInkCount(t *testing.T) {
	bridge := NewDeviceRegistry(t.TempDir())
	bridge.MarkConnected("AABBCCDDEE28")
	bridge.UpdateLogin("AABBCCDDEE28", inkjoybridge.LoginInfo{HardwareModel: inkjoybridge.HardwareModelInkJoy28, Panel: "28.5"})
	d, _ := bridge.Get("AABBCCDDEE28")

	hub := NewDeviceRegistry(t.TempDir())
	hub.SyncBridgeDevices(protocol.KindInkJoy, []protocol.BridgeDevice{deviceToBridge(*d)})
	got, ok := hub.Get("AABBCCDDEE28")
	if !ok || got.Panel != "28.5" || len(got.InkNames()) != 8 {
		t.Fatalf("hub copy: %+v, inks %v", got, got.InkNames())
	}
}
