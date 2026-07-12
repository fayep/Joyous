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
	ClientID       string
	Firmware       string
	SleepBeginTime string
	SleepEndTime   string
}

// ParseLoginPayload extracts login info from a login MQTT payload.
func ParseLoginPayload(payload []byte) (LoginInfo, error) {
	var msg struct {
		Data struct {
			ClientID       string `json:"clientid"`
			Ver            string `json:"ver"`
			SleepBeginTime string `json:"sleep_begin_time"`
			SleepEndTime   string `json:"sleep_end_time"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return LoginInfo{}, err
	}
	return LoginInfo{
		ClientID:       msg.Data.ClientID,
		Firmware:       msg.Data.Ver,
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
