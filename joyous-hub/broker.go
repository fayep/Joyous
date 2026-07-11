package main

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
// Returns ("", false) if the topic is not recognised or the MAC is invalid.
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
	Orientation int // raw accelerometer value from DA215S
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

// ShouldIntercept reports whether cloud→frame action is handled locally by the hub.
func ShouldIntercept(action string, intercept AllowList) bool {
	return intercept.Allows(action)
}
