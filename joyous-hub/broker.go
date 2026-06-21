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
	Battery  int
	RSSI     int
	Firmware string
}

// ParseHeartPayload extracts telemetry from a heart MQTT payload.
func ParseHeartPayload(payload []byte) (HeartInfo, error) {
	var msg struct {
		Data struct {
			Battery  int    `json:"battery"`
			RSSI     int    `json:"rssi"`
			Firmware string `json:"firmware"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return HeartInfo{}, err
	}
	d := msg.Data
	return HeartInfo{Battery: d.Battery, RSSI: d.RSSI, Firmware: d.Firmware}, nil
}

// LoginInfo holds data extracted from a login message.
type LoginInfo struct {
	ClientID string
	Firmware string
}

// ParseLoginPayload extracts login info from a login MQTT payload.
func ParseLoginPayload(payload []byte) (LoginInfo, error) {
	var msg struct {
		Data struct {
			ClientID string `json:"clientid"`
			Firmware string `json:"firmware"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return LoginInfo{}, err
	}
	return LoginInfo{ClientID: msg.Data.ClientID, Firmware: msg.Data.Firmware}, nil
}

// ShouldIntercept reports whether an incoming cloud→frame action should be
// handled locally by the hub rather than forwarded to the frame.
func ShouldIntercept(action string) bool {
	return action == "mqtt_config"
}
