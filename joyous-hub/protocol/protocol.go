// Package protocol defines the Joyous hub↔bridge MQTT vocabulary.
// Bridges (InkJoy, Samsung, …) connect to the hub broker and use these
// topics instead of embedding vendor MQTT/MDC inside joyous-hub.
package protocol

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

const Version = "1"

// Bridge kinds identify vendor bridge programs.
const (
	KindInkJoy  = "inkjoy"
	KindSamsung = "samsung"
	KindNixplay = "nixplay"
)

// BridgeKinds lists path prefixes owned by vendor bridges (hub proxies /{kind}/…).
func BridgeKinds() []string {
	return []string{KindInkJoy, KindSamsung, KindNixplay}
}

// IsBridgeKind reports whether id is a known bridge path prefix.
func IsBridgeKind(id string) bool {
	return slices.Contains(BridgeKinds(), id)
}

// Topic layout on the hub broker:
//
//	joyous/bridge/{bridge_id}/presence   bridge → hub (retained)
//	joyous/bridge/{bridge_id}/devices    bridge → hub (retained snapshot)
//	joyous/bridge/{bridge_id}/device     bridge → hub (single device delta)
//	joyous/bridge/{bridge_id}/event      bridge → hub (ephemeral events)
//	joyous/bridge/{bridge_id}/ui         bridge → hub (retained tab UI state)
//	joyous/hub/{bridge_id}/cmd           hub → bridge (commands)
//	joyous/hub/{bridge_id}/ui            hub → bridge (UI user actions)

const (
	topicBridge = "joyous/bridge"
	topicHub    = "joyous/hub"
)

func BridgeTopic(bridgeID, suffix string) string {
	return fmt.Sprintf("%s/%s/%s", topicBridge, bridgeID, suffix)
}

func HubTopic(bridgeID, suffix string) string {
	return fmt.Sprintf("%s/%s/%s", topicHub, bridgeID, suffix)
}

// Envelope wraps every hub↔bridge JSON payload.
type Envelope struct {
	Type      string          `json:"type"`
	Version   string          `json:"v,omitempty"`
	BridgeID  string          `json:"bridge_id,omitempty"`
	Timestamp time.Time       `json:"ts"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// Bridge capability strings (HelloPayload.Capabilities).
const (
	CapConfigUI = "config.ui" // bridge serves a vendor configuration page over ui HTTP proxy
)

// Message type constants.
const (
	TypeHello           = "bridge.hello"
	TypeDevices         = "devices.sync"
	TypeDevice          = "device.update"
	TypeDeviceRemove    = "device.remove"
	TypeEvent           = "bridge.event"
	TypeUIState         = "ui.state"
	TypeUIAction        = "ui.action"
	TypeUIHTTPRequest   = "ui.http.request"
	TypeUIHTTPResponse  = "ui.http.response"
	TypeCmd             = "bridge.cmd"
	TypeSendComplete    = "send.complete"
	TypeMQTTLogs        = "mqtt.logs"
)

// HelloPayload announces a bridge at startup.
type HelloPayload struct {
	Kind         string   `json:"kind"` // inkjoy, samsung
	Capabilities []string `json:"capabilities,omitempty"`
	ListenHTTP   string   `json:"listen_http,omitempty"`
	ListenMQTT   string   `json:"listen_mqtt,omitempty"`
}

// BridgeDevice is the vendor-neutral device view bridges publish to the hub.
type BridgeDevice struct {
	ID              string    `json:"id"`
	Type            string    `json:"type"`
	Name            string    `json:"name,omitempty"`
	MAC             string    `json:"mac,omitempty"`
	IP              string    `json:"ip,omitempty"`
	USN             string    `json:"usn,omitempty"`
	Firmware        string    `json:"firmware,omitempty"`
	Battery         int       `json:"battery,omitempty"`
	PowerSource     string    `json:"power_source,omitempty"`
	RSSI            int       `json:"rssi,omitempty"`
	Connected       bool      `json:"connected"`
	LastSeen        time.Time `json:"last_seen"`
	LastAction      string    `json:"last_action,omitempty"`
	SleepBeginTime  string    `json:"sleep_begin_time,omitempty"`
	SleepEndTime    string    `json:"sleep_end_time,omitempty"`
	Portrait        bool      `json:"portrait,omitempty"`
	HubIP           string    `json:"hub_ip,omitempty"`
	MDCPin          string    `json:"mdc_pin,omitempty"`
	DisplayCropFormat string  `json:"display_crop_format,omitempty"`
	DisplayWidth    int       `json:"display_width,omitempty"`
	DisplayHeight   int       `json:"display_height,omitempty"`
	DeepSleepActive bool      `json:"deep_sleep_active,omitempty"`
}

// DevicesPayload is a full device list snapshot from a bridge.
type DevicesPayload struct {
	Devices []BridgeDevice `json:"devices"`
}

// DevicePayload is a single-device update.
type DevicePayload struct {
	Device BridgeDevice `json:"device"`
}

// DeviceRemovePayload removes a device from the hub view.
type DeviceRemovePayload struct {
	DeviceID string `json:"device_id"`
}

// CmdPayload is a hub→bridge command.
type CmdPayload struct {
	Cmd     string          `json:"cmd"`
	DeviceID string         `json:"device_id,omitempty"`
	Body    json.RawMessage `json:"body,omitempty"`
}

// Command names (CmdPayload.Cmd).
const (
	CmdDiscover      = "discover"
	CmdSendImage     = "send.image"
	CmdRefresh       = "display.refresh"
	CmdSleep         = "sleep.set"
	CmdRedirect      = "mqtt.redirect"
	CmdSamsungPush   = "samsung.push"
	CmdSamsungWake   = "samsung.wake"
	CmdSamsungSleep  = "samsung.sleep"
	CmdSamsungConfig = "samsung.config"
	CmdDeviceTouch   = "device.touch" // hub → bridge: LastSeen/action from hub-side contact
	CmdBLEScan       = "ble.scan"
	CmdBLEAdopt      = "ble.adopt"
)

// CropRect is a normalized (0–1) rectangle within the source image.
type CropRect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// SendImageBody is the body for CmdSendImage.
// The hub sends the album source reference and all saved crops; the bridge
// fetches the original from HubBaseURL, picks the crop/format appropriate for
// the destination device, and performs vendor-specific encode.
type SendImageBody struct {
	ImageID      string              `json:"image_id"`
	Crops        map[string]CropRect `json:"crops,omitempty"` // all album crops, keyed by aspect (e.g. "4:3", "16:9")
	OverlayToken string              `json:"overlay_token,omitempty"`
	Overlay      *OverlaySendInfo    `json:"overlay,omitempty"`
	SendID       string              `json:"send_id,omitempty"`
	HubBaseURL   string              `json:"hub_base_url"`
}

// OverlaySendInfo carries overlay config and weather snapshot from the hub so
// the bridge can composite the same overlay the hub previewed.
type OverlaySendInfo struct {
	Config  json.RawMessage `json:"config"`
	Weather json.RawMessage `json:"weather"`
}

// SendCompletePayload reports send delivery to the hub.
type SendCompletePayload struct {
	SendID   string `json:"send_id"`
	DeviceID string `json:"device_id"`
	Success  bool   `json:"success"`
	Detail   string `json:"detail,omitempty"`
	// Phase is optional: "bound" (Samsung etag ready), "downloading", empty/"delivered" on completion.
	Phase string `json:"phase,omitempty"`
	// FrameID and ETag are set with phase=bound so the hub can BindSamsung before frame pull.
	FrameID string `json:"frame_id,omitempty"`
	ETag    string `json:"etag,omitempty"`
}

// UIStatePayload is bridge-owned tab state (InkJoy / Samsung pages).
type UIStatePayload struct {
	Revision int             `json:"revision"`
	State    json.RawMessage `json:"state"`
}

// UIActionPayload is a user action from the hub UI forwarded to the bridge.
type UIActionPayload struct {
	Action string          `json:"action"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// UIHTTPRequestPayload is a hub→bridge HTTP request tunneled over MQTT.
type UIHTTPRequestPayload struct {
	RequestID string            `json:"request_id"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      []byte            `json:"body,omitempty"`
}

// UIHTTPResponsePayload is the bridge→hub reply for UIHTTPRequestPayload.
type UIHTTPResponsePayload struct {
	RequestID   string            `json:"request_id"`
	Status      int               `json:"status"`
	ContentType string            `json:"content_type,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        []byte            `json:"body,omitempty"`
}

// EventPayload is an ephemeral bridge event.
type EventPayload struct {
	Name string          `json:"name"`
	Body json.RawMessage `json:"body,omitempty"`
}

// MQTTLogsPayload carries MQTT debug log entries for the InkJoy tab.
type MQTTLogsPayload struct {
	Local    json.RawMessage `json:"local,omitempty"`
	Upstream json.RawMessage `json:"upstream,omitempty"`
}

func NewEnvelope(msgType, bridgeID string, payload any) ([]byte, error) {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	env := Envelope{
		Type:      msgType,
		Version:   Version,
		BridgeID:  bridgeID,
		Timestamp: time.Now().UTC(),
		Payload:   raw,
	}
	return json.Marshal(env)
}

func DecodeEnvelope(data []byte) (Envelope, error) {
	var env Envelope
	err := json.Unmarshal(data, &env)
	return env, err
}

func DecodePayload[T any](env Envelope) (T, error) {
	var out T
	if len(env.Payload) == 0 {
		return out, nil
	}
	err := json.Unmarshal(env.Payload, &out)
	return out, err
}
