package inkjoybridge

import "testing"

// TestParseLoginPanel verifies the login "panel" SKU field is captured (added
// in firmware v1.3.x) and that H is still parsed from ver alongside it. The
// 28.5" frame reports H:3 / panel "28.5" with the new dotted-firmware ver.
func TestParseLoginPanel(t *testing.T) {
	cases := []struct {
		name      string
		payload   string
		wantModel int
		wantPanel string
	}{
		{
			name:      "28.5 H:3 v1.3.8",
			payload:   `{"action":"login","data":{"ver":"M H:3 F:1.3.8 B:72","panel":"28.5"}}`,
			wantModel: HardwareModelInkJoy28,
			wantPanel: "28.5",
		},
		{
			name:      "10in H:2",
			payload:   `{"action":"login","data":{"ver":"M H:2 F:1.1.0","panel":"10"}}`,
			wantModel: HardwareModelInkJoy10,
			wantPanel: "10",
		},
		{
			name:      "older firmware without panel field",
			payload:   `{"action":"login","data":{"ver":"M H:2 F:0.5.6"}}`,
			wantModel: HardwareModelInkJoy10,
			wantPanel: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info, err := ParseLoginPayload([]byte(c.payload))
			if err != nil {
				t.Fatal(err)
			}
			if info.HardwareModel != c.wantModel {
				t.Errorf("HardwareModel = %d, want %d", info.HardwareModel, c.wantModel)
			}
			if info.Panel != c.wantPanel {
				t.Errorf("Panel = %q, want %q", info.Panel, c.wantPanel)
			}
		})
	}
}

// TestPanelGeometry checks the SKU→geometry table against the frame firmware's
// own panel table (ij_epd v1.3.8). BufferBytes is the exact full-panel size the
// frame expects; W*H*bytesPerPixel must reconcile with it.
func TestPanelGeometry(t *testing.T) {
	cases := []struct {
		panel     string
		w, h, buf int
	}{
		{"10", 1200, 1600, 3840000},   // 2 B/px
		{"25.3", 3200, 1800, 5760000}, // 1 B/px
		{"28.5", 2160, 3060, 6609600}, // 1 B/px
		{"31.5", 2560, 1440, 2304000},
		{"13.3", 1200, 1600, 960004}, // ~4bpp + 4-byte header
	}
	for _, c := range cases {
		g, ok := LookupPanelGeometry(c.panel)
		if !ok {
			t.Errorf("LookupPanelGeometry(%q) not found", c.panel)
			continue
		}
		if g.Width != c.w || g.Height != c.h || g.BufferBytes != c.buf {
			t.Errorf("panel %q = %dx%d buf=%d, want %dx%d buf=%d",
				c.panel, g.Width, g.Height, g.BufferBytes, c.w, c.h, c.buf)
		}
	}
	if _, ok := LookupPanelGeometry("nonsense"); ok {
		t.Error("LookupPanelGeometry(nonsense) should be unknown")
	}
}
