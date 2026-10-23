package inkjoybridge

import (
	"encoding/json"
	"strings"
	"unicode"
)

// TopicDir classifies the direction of an MQTT topic.
type TopicDir int

const (
	DirUnknown      TopicDir = iota
	DirFrameToCloud          // /device/report/{MAC}
	DirCloudToFrame          // /inkjoyap/{MAC}
)

// IsFrameClientID reports whether s looks like a frame client ID (12 hex chars).
func IsFrameClientID(s string) bool {
	if len(s) != 12 {
		return false
	}
	for _, c := range s {
		if !unicode.Is(unicode.ASCII_Hex_Digit, c) {
			return false
		}
	}
	return true
}

// ExtractTopicMAC pulls the MAC address from a known topic pattern.
func ExtractTopicMAC(topic string) (string, bool) {
	for _, prefix := range []string{"/device/report/", "/inkjoyap/"} {
		if strings.HasPrefix(topic, prefix) {
			mac := topic[len(prefix):]
			if IsFrameClientID(mac) {
				return mac, true
			}
		}
	}
	return "", false
}

// TopicDirection classifies the direction of an MQTT topic.
func TopicDirection(topic string) TopicDir {
	if strings.HasPrefix(topic, "/device/report/") {
		return DirFrameToCloud
	}
	if strings.HasPrefix(topic, "/inkjoyap/") {
		return DirCloudToFrame
	}
	return DirUnknown
}

// HeartInfo holds telemetry extracted from a heart message.
type HeartInfo struct {
	Battery     int
	RSSI        int
	Firmware    string
	Orientation int
}

// ParseHeartPayload extracts telemetry from a heart MQTT payload.
func ParseHeartPayload(payload []byte) (HeartInfo, error) {
	var msg struct {
		Data struct {
			Battery     int    `json:"battery"`
			RSSI        int    `json:"wifi_rssi"`
			Firmware    string `json:"version"`
			Orientation int    `json:"orientation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return HeartInfo{}, err
	}
	d := msg.Data
	return HeartInfo{Battery: d.Battery, RSSI: d.RSSI, Firmware: d.Firmware, Orientation: d.Orientation}, nil
}

// LoginInfo holds data extracted from a login message.
type LoginInfo struct {
	ClientID      string
	Firmware      string
	HardwareModel int // H from login ver "M H:%d F:..."; 0 if absent
	// Panel is the frame's self-reported panel SKU string from the login
	// "panel" field (e.g. "10", "28.5"), added in firmware v1.3.x. "" for
	// older firmware that doesn't report it. This is the authoritative source
	// for a frame's geometry — see PanelGeometries — since a given H can in
	// principle ship with different panels.
	Panel          string
	SleepBeginTime string
	SleepEndTime   string
	// P2 is the device's own factory/NVS-stored P2 dither-target calibration
	// (login's "p2": [[r,g,b]x6]), if this firmware reports one. nil for
	// firmware that doesn't send it (older builds, real InkJoy hardware).
	P2 *[6][3]int
	// ProtocolT is the "T" clone-firmware protocol/feature version parsed
	// from ver ("T:%d", see HardwareModelFromVer's H). 0 if absent (real
	// InkJoy hardware never sends it, nor do clone builds predating it).
	// See InkJoyPNGProtocolT: T < InkJoyPNGProtocolT means the device has
	// no PNG content-cache support and must only ever be sent .bin.
	ProtocolT int
}

// HardwareModelSeeed is the H value used by Seeed EE02 custom firmware.
// These frames speak InkJoy MQTT locally but must not be published to InkJoy cloud.
const HardwareModelSeeed = 9

// HardwareModelGooDisplay is the H value used by GooDisplay ESP32-133C02
// custom firmware — same local-only situation as Seeed, distinct model so
// the hub can tell the two clone hardware types apart (e.g. P2 pre-fill).
const HardwareModelGooDisplay = 10

// Known real-InkJoy hardware models (H in the login ver). Observed mappings:
// H:2 ships the 10" panel, H:3 the 28.5" panel (firmware v1.3.x). These are
// not local-only (they're genuine InkJoy frames). The authoritative geometry
// is the login "panel" field, not H — see PanelGeometries.
const (
	HardwareModelInkJoy10 = 2 // 10" panel ("10")
	HardwareModelInkJoy28 = 3 // 28.5" panel ("28.5")
)

// PanelGeometry is an InkJoy panel SKU's native framebuffer geometry, taken
// from the frame firmware's panel→geometry table (ij_epd v1.3.8 function at
// 0x4201eb06, reverse-engineered 2026-10-20). Width/Height are the firmware's
// native (portrait) pixel dimensions. BufferBytes is the exact full-panel
// framebuffer size the frame expects; note the per-pixel size varies by SKU
// (e.g. "10" is 2 B/px, "28.5"/"25.3" are 1 B/px, and "13.3"/"7.09" are ~4bpp
// plus a 4-byte header), so derive bit depth from BufferBytes/(W*H), not a
// fixed assumption.
type PanelGeometry struct {
	Width       int
	Height      int
	BufferBytes int
}

// PanelGeometries maps the login "panel" SKU string to its geometry. Keep in
// lockstep with the frame firmware's table (see PanelGeometry's doc for the
// traced source).
var PanelGeometries = map[string]PanelGeometry{
	"13.3": {Width: 1200, Height: 1600, BufferBytes: 960004},
	"7.09": {Width: 1200, Height: 1600, BufferBytes: 960004},
	"10":   {Width: 1200, Height: 1600, BufferBytes: 3840000},
	"25.3": {Width: 3200, Height: 1800, BufferBytes: 5760000},
	"28.5": {Width: 2160, Height: 3060, BufferBytes: 6609600},
	"31.5": {Width: 2560, Height: 1440, BufferBytes: 2304000},
}

// LookupPanelGeometry returns the native geometry for a login panel SKU
// string, and whether that SKU is known.
func LookupPanelGeometry(panel string) (PanelGeometry, bool) {
	g, ok := PanelGeometries[panel]
	return g, ok
}

// LocalOnlyHardware reports whether this model must stay on the local bridge
// (never open an upstream cloud session / forward frame→cloud traffic).
func LocalOnlyHardware(model int) bool {
	return model == HardwareModelSeeed || model == HardwareModelGooDisplay
}

// verIntField parses the integer following prefix (e.g. "H:", "T:") in an
// InkJoy login ver string ("M H:%d T:%d F:..."). Returns 0 if prefix is
// absent (older firmware, or real InkJoy hardware which sends neither H
// nor T).
func verIntField(ver, prefix string) int {
	i := strings.Index(ver, prefix)
	if i < 0 {
		return 0
	}
	rest := ver[i+len(prefix):]
	n := 0
	for _, c := range rest {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// HardwareModelFromVer parses H from an InkJoy login ver string ("M H:%d F:...").
func HardwareModelFromVer(ver string) int {
	return verIntField(ver, "H:")
}

// ProtocolTFromVer parses T (clone-firmware protocol/feature version) from
// an InkJoy login ver string. 0 if absent -- see LoginInfo.ProtocolT.
func ProtocolTFromVer(ver string) int {
	return verIntField(ver, "T:")
}

// InkJoyPNGProtocolT is the minimum ProtocolT that supports the PNG
// content-cache format (see content_cache.h / panel.h's renderPngBytes in
// Firmware/). Devices below this must only ever be sent .bin. Keep in
// lockstep with Firmware/include/config.h's FW_PROTOCOL_T -- they gate the
// same capability from opposite ends of the wire and must never drift.
const InkJoyPNGProtocolT = 9

// InkJoyNativeDimsProtocolT is the minimum ProtocolT whose firmware can
// decode/render a PNG at native (swapped) panel dimensions under
// epaper.setRotation(0), instead of always assuming the fixed
// frameW x frameH landscape wire shape (see panel_tftespi.cpp's
// renderPngBytes, nativeDims handling). Deliberately a separate, higher
// threshold than InkJoyPNGProtocolT: firmware already in the field at
// T==InkJoyPNGProtocolT supports PNG but only at the fixed shape, and must
// keep getting exactly that until it's upgraded. Keep in lockstep with
// Firmware/include/config.h's FW_PROTOCOL_T, same as InkJoyPNGProtocolT.
const InkJoyNativeDimsProtocolT = 10

// ParseLoginPayload extracts login info from a login MQTT payload.
func ParseLoginPayload(payload []byte) (LoginInfo, error) {
	var msg struct {
		Data struct {
			ClientID       string     `json:"clientid"`
			Ver            string     `json:"ver"`
			Panel          string     `json:"panel"`
			SleepBeginTime string     `json:"sleep_begin_time"`
			SleepEndTime   string     `json:"sleep_end_time"`
			P2             *[6][3]int `json:"p2"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return LoginInfo{}, err
	}
	return LoginInfo{
		ClientID:       msg.Data.ClientID,
		Firmware:       msg.Data.Ver,
		HardwareModel:  HardwareModelFromVer(msg.Data.Ver),
		Panel:          msg.Data.Panel,
		ProtocolT:      ProtocolTFromVer(msg.Data.Ver),
		P2:             msg.Data.P2,
		SleepBeginTime: msg.Data.SleepBeginTime,
		SleepEndTime:   msg.Data.SleepEndTime,
	}, nil
}

// SleepInfo holds data extracted from a frame's sleep (power-off) message.
// Reason's enum values aren't confirmed yet — observed so far: 2 (seen at
// 47% battery, so likely the scheduled wifi_sleep window rather than a
// low-battery trigger, but not verified against other reason values).
type SleepInfo struct {
	Battery int
	Reason  int
}

// ParseSleepPayload extracts telemetry from a frame's sleep MQTT payload.
func ParseSleepPayload(payload []byte) (SleepInfo, error) {
	var msg struct {
		Data struct {
			Battery int `json:"battery"`
			Reason  int `json:"reason"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return SleepInfo{}, err
	}
	return SleepInfo{Battery: msg.Data.Battery, Reason: msg.Data.Reason}, nil
}

// ShouldIntercept reports whether cloud→frame action is handled locally by the bridge.
func ShouldIntercept(action string, intercept AllowList) bool {
	return intercept.Allows(action)
}

// MQTTAction extracts the action field from an InkJoy MQTT JSON payload.
func MQTTAction(payload []byte) string {
	var env struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return ""
	}
	return env.Action
}

// MQTTMsgid extracts the msgid field from an InkJoy MQTT JSON payload.
// MQTTAckMsgid returns data.ack_msgid: the msgid of the message this one answers.
func MQTTAckMsgid(payload []byte) string {
	var env struct {
		Data struct {
			AckMsgid json.RawMessage `json:"ack_msgid"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &env) != nil || len(env.Data.AckMsgid) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(env.Data.AckMsgid, &s) == nil {
		return s
	}
	return strings.Trim(string(env.Data.AckMsgid), `"`)
}

func MQTTMsgid(payload []byte) string {
	var env struct {
		Msgid json.RawMessage `json:"msgid"`
	}
	if err := json.Unmarshal(payload, &env); err != nil || len(env.Msgid) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(env.Msgid, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(env.Msgid, &n); err == nil {
		return n.String()
	}
	return strings.Trim(string(env.Msgid), `"`)
}
