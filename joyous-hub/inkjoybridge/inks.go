package inkjoybridge

import (
	"strconv"
	"strings"
)

// InkJoy panel refreshes are driven by an FPGA whose waveforms are set in
// software, so the inks a frame can show are a property of its hardware model
// plus firmware, not of the glass. inkSets lists, per model, the firmware
// version from which each ink count applies; the latest row at or below the
// frame's firmware wins. Add a row when a firmware release enables more inks.
// Models not listed have the 6 shared inks.
var inkSets = map[int][]struct {
	minFirmware string // dotted version; "" = every firmware
	inks        int
}{
	HardwareModelInkJoy28: {{"", 8}},
}

// PanelInkCount is the number of inks a frame shows: 6 (black, white, yellow,
// red, blue, green), or 8 with goldenrod and orange added. firmware is the
// device's Firmware field, either the bare heart "version" ("1.3.7") or the
// login ver ("M H:3 F:1.3.8 B:72").
func PanelInkCount(hardwareModel int, firmware string) int {
	inks := 6
	fw := FirmwareVersion(firmware)
	for _, s := range inkSets[hardwareModel] {
		if s.minFirmware == "" || (fw != "" && compareDotted(fw, s.minFirmware) >= 0) {
			inks = s.inks
		}
	}
	return inks
}

// FirmwareVersion extracts the dotted firmware version from a login ver
// string's F: field, or returns s itself when it is already a bare version.
func FirmwareVersion(s string) string {
	if i := strings.Index(s, "F:"); i >= 0 {
		s = s[i+2:]
		if j := strings.IndexByte(s, ' '); j >= 0 {
			s = s[:j]
		}
	}
	return strings.TrimSpace(s)
}

// compareDotted compares dotted numeric versions ("1.3.10" > "1.3.9"); a
// missing or non-numeric component counts as 0.
func compareDotted(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(as), len(bs)) {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
