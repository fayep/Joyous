package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// UpstreamConfig holds connection parameters for the real MQTT broker.
type UpstreamConfig struct {
	Host     string
	Port     int
	Username string
	Password string
}

// ParseMQTTConfig parses a broker→frame mqtt_config MQTT payload.
// Returns an error if required fields (host, port, usr, pwd) are absent.
func ParseMQTTConfig(payload []byte) (UpstreamConfig, error) {
	var msg struct {
		Data struct {
			Host string `json:"host"`
			Port int    `json:"port"`
			Usr  string `json:"usr"`
			Pwd  string `json:"pwd"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return UpstreamConfig{}, err
	}
	d := msg.Data
	if d.Host == "" || d.Port == 0 || d.Usr == "" || d.Pwd == "" {
		return UpstreamConfig{}, errors.New("mqtt_config: missing required fields (host, port, usr, pwd)")
	}
	return UpstreamConfig{Host: d.Host, Port: d.Port, Username: d.Usr, Password: d.Pwd}, nil
}

// BuildMQTTConfigPayload builds the JSON payload for sending mqtt_config to a frame.
func BuildMQTTConfigPayload(mac string, cfg UpstreamConfig) []byte {
	msg := map[string]any{
		"action": "mqtt_config",
		"msgid":  fmt.Sprintf("%d", time.Now().UnixMilli()),
		"stamac": mac,
		"data": map[string]any{
			"host": cfg.Host,
			"port": cfg.Port,
			"usr":  cfg.Username,
			"pwd":  cfg.Password,
		},
	}
	b, _ := json.Marshal(msg)
	return b
}

// AllowList is a set of MQTT action names used for frame→broker or broker→frame filtering.
type AllowList struct {
	set map[string]bool
}

// Allows reports whether the given action should be forwarded to the upstream broker.
func (a AllowList) Allows(action string) bool {
	return a.set[action]
}

// DefaultUpstreamAllowCSV is the default frame→broker (upstream) allow list.
// "sleep" (not "shutdown" — an earlier guess that a 2026-07 capture proved
// wrong; see research/firmware-notes.md) is the real frame→broker power-off
// action name.
func DefaultUpstreamAllowCSV() string {
	return "login,heart,play_ack,fpga_ota_ack,sleep,image_refresh_ack,ota_ack,wifi_sleep_ack,mqtt_config_ack"
}

// DefaultDownstreamAllowCSV is the default broker→frame passthrough list.
func DefaultDownstreamAllowCSV() string {
	return "login_ack,heart_ack,play,device_config,shutdown_ack,image_refresh_ack,wifi_sleep"
}

// DefaultInterceptCSV is the default broker→frame intercept list (hub handles locally).
func DefaultInterceptCSV() string {
	return "mqtt_config,wifi_sleep,ota,fpga"
}

// DefaultUpstreamAllow returns the default frame→broker allow list.
func DefaultUpstreamAllow() AllowList {
	return ParseAllowList(DefaultUpstreamAllowCSV())
}

// DefaultDownstreamAllow returns the default broker→frame allow list.
func DefaultDownstreamAllow() AllowList {
	return ParseAllowList(DefaultDownstreamAllowCSV())
}

// DefaultIntercept returns the default broker→frame intercept list.
func DefaultIntercept() AllowList {
	return ParseAllowList(DefaultInterceptCSV())
}

// ParseAllowList parses a comma-separated list of action names.
func ParseAllowList(s string) AllowList {
	set := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			set[part] = true
		}
	}
	return AllowList{set: set}
}

// ParseUpstreamAllow parses a comma-separated frame→broker allow list.
func ParseUpstreamAllow(s string) AllowList {
	return ParseAllowList(s)
}
