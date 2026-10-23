package main

import (
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"joyous-hub/protocol"
)

// The bridge's periodic device sync must not wipe what the hub owns.
func TestBridgeSyncKeepsHubOwnedFields(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())
	snap := []protocol.BridgeDevice{{ID: "D0CF13EF4080", Type: string(DeviceTypeInkJoy), MAC: "D0CF13EF4080"}}
	reg.SyncBridgeDevices(protocol.KindInkJoy, snap)
	override := []PaletteRGB{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}, {1, 1, 1}, {2, 2, 2}, {3, 3, 3}}
	reg.SetColorOverride("D0CF13EF4080", override)
	reg.SetLastImage("D0CF13EF4080", "img1", "tok")

	reg.SyncBridgeDevices(protocol.KindInkJoy, snap)
	d, _ := reg.Get("D0CF13EF4080")
	if len(d.ColorOverride) != 6 || d.ColorOverride[2] != (PaletteRGB{7, 8, 9}) || d.LastImageID != "img1" || d.LastOverlayHash != "tok" {
		t.Fatalf("after sync: override %v, last image %q/%q", d.ColorOverride, d.LastImageID, d.LastOverlayHash)
	}
	// A frame dropped from the snapshot is still removed.
	reg.SyncBridgeDevices(protocol.KindInkJoy, nil)
	if _, ok := reg.Get("D0CF13EF4080"); ok {
		t.Fatal("device missing from the snapshot was kept")
	}
}

func touchAt(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// "Currently displayed" follows the newest thing actually played, wherever it
// came from: a Joyous image, a relayed cloud play, or an IJI3 (28.5") play.
func TestInkJoyCurrentDisplay(t *testing.T) {
	h := buildTestHub(t)
	const mac = "D0CF13EF4080"
	h.devices.MarkConnected(mac)
	id := storeSynthImage(t, h.images, 160, 120)
	current := func() Device {
		devs := h.devicesSnapshotForUI()
		for _, d := range devs {
			if d.ID == mac {
				return d
			}
		}
		t.Fatal("device missing")
		return Device{}
	}
	t0 := time.Now().Add(-time.Hour)

	// 1. Joyous send of a hub image.
	if err := h.inkjoy.Save(mac, id+"~tok-p", make([]byte, frameW*frameH*2)); err != nil {
		t.Fatal(err)
	}
	touchAt(t, h.inkjoy.binPath(mac, id+"~tok-p"), t0)
	if d := current(); d.LastImageID != id || d.LastOverlayHash != "tok" {
		t.Fatalf("after Joyous send: %q/%q preview %v", d.LastImageID, d.LastOverlayHash, d.DisplayPreviewAt)
	}

	// 2. Later cloud play, relayed: cached under the cloud imgid + preview JPEG.
	h.inkjoy.Save(mac, "955f2a6f-cloud", make([]byte, frameW*frameH*2))
	touchAt(t, h.inkjoy.binPath(mac, "955f2a6f-cloud"), t0.Add(time.Minute))
	h.displayPreview.Save(mac, []byte("jpeg"))
	touchAt(t, h.displayPreview.jpegPath(mac), t0.Add(time.Minute+time.Second))
	if d := current(); d.LastImageID != "" || d.DisplayPreviewAt.IsZero() {
		t.Fatalf("after cloud play: last image %q, preview %v", d.LastImageID, d.DisplayPreviewAt)
	}

	// 3. Later IJI3 play: previewed from the played file.
	b, _ := testIJI3(t)
	h.inkjoy.Save(mac, id+"~tok2-p", b)
	touchAt(t, h.inkjoy.binPath(mac, id+"~tok2-p"), t0.Add(2*time.Minute))
	if d := current(); d.LastImageID != "" || !d.DisplayPreviewAt.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("after IJI3 play: last image %q, preview %v", d.LastImageID, d.DisplayPreviewAt)
	}
	rec := httptest.NewRecorder()
	h.handleDeviceDisplayPreview(rec, httptest.NewRequest("GET", "/api/devices/"+mac+"/display-preview", nil), mac)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Body.Len() < 1000 {
		t.Fatalf("IJI3 preview: %d %q %d bytes", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
}
