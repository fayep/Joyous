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
	TypeHello          = "bridge.hello"
	TypeDevices        = "devices.sync"
	TypeDevice         = "device.update"
	TypeDeviceRemove   = "device.remove"
	TypeEvent          = "bridge.event"
	TypeUIState        = "ui.state"
	TypeUIAction       = "ui.action"
	TypeUIHTTPRequest  = "ui.http.request"
	TypeUIHTTPResponse = "ui.http.response"
	TypeCmd            = "bridge.cmd"
	TypeSendComplete   = "send.complete"
	TypeMQTTLogs       = "mqtt.logs"
)

// HelloPayload announces a bridge at startup.
type HelloPayload struct {
	Kind         string   `json:"kind"` // inkjoy, samsung
	Capabilities []string `json:"capabilities,omitempty"`
	ListenHTTP   string   `json:"listen_http,omitempty"`
	ListenMQTT   string   `json:"listen_mqtt,omitempty"`
	// HTTPPaths lists exact root-level HTTP paths (e.g. "/content-transfer-progress") this
	// bridge wants the hub to forward to it over the ui HTTP tunnel. For vendor protocol
	// quirks where a physical device calls back to what it believes is a server root —
	// not namespaced under the bridge's own /{kind}/ proxy prefix — rather than hardcoding
	// each one into the hub, the bridge declares the paths it owns and the hub's catch-all
	// route forwards matching requests to it.
	HTTPPaths []string `json:"http_paths,omitempty"`
}

// BridgeDevice is the vendor-neutral device view bridges publish to the hub.
type BridgeDevice struct {
	ID                string    `json:"id"`
	Type              string    `json:"type"`
	Name              string    `json:"name,omitempty"`
	MAC               string    `json:"mac,omitempty"`
	IP                string    `json:"ip,omitempty"`
	LastIP            string    `json:"last_ip,omitempty"` // Samsung: durable WoL target when ip cleared
	USN               string    `json:"usn,omitempty"`
	Firmware          string    `json:"firmware,omitempty"`
	Battery           int       `json:"battery,omitempty"`
	PowerSource       string    `json:"power_source,omitempty"`
	RSSI              int       `json:"rssi,omitempty"`
	Connected         bool      `json:"connected"`
	LastSeen          time.Time `json:"last_seen"`
	LastAction        string    `json:"last_action,omitempty"`
	SleepBeginTime    string    `json:"sleep_begin_time,omitempty"`
	SleepEndTime      string    `json:"sleep_end_time,omitempty"`
	Portrait          bool      `json:"portrait,omitempty"`
	HubIP             string    `json:"hub_ip,omitempty"`
	MDCPin            string    `json:"mdc_pin,omitempty"`
	DisplayCropFormat string    `json:"display_crop_format,omitempty"`
	DisplayWidth      int       `json:"display_width,omitempty"`
	DisplayHeight     int       `json:"display_height,omitempty"`
	DeepSleepActive   bool      `json:"deep_sleep_active,omitempty"`
	HardwareModel     int       `json:"hardware_model,omitempty"` // InkJoy login H (e.g. 3 = 28.5")
	Panel             string    `json:"panel,omitempty"`          // InkJoy login panel SKU (e.g. "28.5")
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
	Cmd      string          `json:"cmd"`
	DeviceID string          `json:"device_id,omitempty"`
	Body     json.RawMessage `json:"body,omitempty"`
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
	CmdDeviceTouch   = "device.touch" // hub → bridge: cache pull / hub contact (see DeviceTouchBody)
	CmdSendOverlay   = "send.overlay" // hub → bridge: relay pre-computed overlay text (see OverlaySendBody)
)

// DeviceTouchBody is the body for CmdDeviceTouch. The hub reports that a device
// contacted hub-owned resources (e.g. pulled frame cache); the owning bridge
// applies vendor-specific LastSeen / IP / sleep handling.
type DeviceTouchBody struct {
	Action   string `json:"action"`
	ClientIP string `json:"client_ip,omitempty"` // observed peer IP on the hub HTTP request
}

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
	// OverlayWire carries the hub's already-computed overlay line geometry
	// (see OverlaySendBody's own comment) for devices that render overlay
	// text themselves -- currently only Seeed/GooDisplay clones (see
	// inkjoybridge.LocalOnlyHardware). Separate from Overlay above: that
	// field is raw config+weather for the bridge to composite into image
	// pixels (real InkJoy vendor frames, Samsung -- always baked in, never
	// gated), this one is relayed into the play message's own "overlay"
	// field verbatim for the device to draw itself, on top of the same
	// image, in one message. A play can carry both, neither, or just one.
	OverlayWire *OverlaySendBody `json:"overlay_wire,omitempty"`
	// NativeDims, when true, tells the bridge to encode this send at native
	// (swapped) panel dimensions instead of rotating portrait content into
	// the fixed frameW x frameH landscape wire shape -- our own clone
	// hardware's physical panel is natively portrait, so this skips a
	// rotate90CCW pixel transform (and the matching overlay coordinate
	// transform) entirely. Computed and consumed bridge-side only
	// (bridgeDeliverInkJoyImage decides it via inkjoybridge.LocalOnlyHardware
	// + InkJoyNativeDimsProtocolT; bridgeEncodeInkJoy reads it) -- never set
	// or branched on by hub-compiled code, same convention as OverlayWire.
	NativeDims bool   `json:"native_dims,omitempty"`
	// InkJoy28, when true, tells the bridge to encode this send as an IJI3
	// RAW8|LZ4 blob for the 28.5"/H:3 panel (2160x3060 single-plane 8bpp,
	// panel-native scan transform) instead of the 10" hi/lo .bin. Computed and
	// consumed bridge-side only (bridgeDeliverInkJoyImage sets it from
	// dev.HardwareModel; bridgeEncodeInkJoy reads it) -- never set or branched
	// on by hub-compiled code, same convention as NativeDims.
	InkJoy28   bool   `json:"inkjoy28,omitempty"`
	SendID     string `json:"send_id,omitempty"`
	HubBaseURL string `json:"hub_base_url"`
	// ColorOverride is the device's per-device ink/color palette override, if
	// any (hub-owned — see the hub's Device.ColorOverride), resolved by the hub
	// at send time and handed to the bridge for this one encode. The bridge
	// itself never stores or manages this; it only ever sees whatever the hub
	// includes here, so bridges stay bridge-specific-config-free.
	// One entry per panel ink (6, or 8 for the 28.5"); a bridge renderer ignores
	// an override sized for a different panel and keeps its default palette.
	ColorOverride []RGB `json:"color_override,omitempty"`
}

// RGB is a wire-safe 0-255 color triple (device-agnostic — used for any
// per-device display palette override the hub resolves before a send).
type RGB [3]int

// OverlaySendInfo carries overlay config and weather snapshot from the hub so
// the bridge can composite the same overlay the hub previewed.
type OverlaySendInfo struct {
	Config  json.RawMessage `json:"config"`
	Weather json.RawMessage `json:"weather"`
}

// OverlaySendBody is the body for CmdSendOverlay. Unlike OverlaySendInfo
// (raw config+weather, for the bridge to render), this carries the hub's
// own already-computed line geometry -- same layout math the hub uses to
// bake its own overlay pixels (see joyous-hub's overlayWireLines) -- so the
// bridge/device never redo margin/position arithmetic themselves. This is
// a standalone test/comparison message, not part of a real image send: it
// draws on top of whatever a device is already displaying so its own
// on-device rendering can be checked against the hub's baked-in version
// for size/position parity.
type OverlaySendBody struct {
	Lines   []OverlayWireLine `json:"lines"`
	Caption *OverlayWireLine  `json:"caption,omitempty"`
}

// OverlayWireLine is one already-positioned line of overlay text. X/Y are
// absolute pixel coordinates (top-left of the line, ascent added by the
// renderer) in the target device's own native frame dimensions -- not
// normalized/relative -- since this is device-specific test tooling, not
// something crops/album-relative like CropRect.
type OverlayWireLine struct {
	Text string `json:"text"`
	Font string `json:"font"` // "large" | "medium" | "caption"
	X    int    `json:"x"`
	Y    int    `json:"y"`
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

// EventPayload is an ephemeral bridge→hub event.
type EventPayload struct {
	Name string          `json:"name"`
	Body json.RawMessage `json:"body,omitempty"`
}

// Well-known EventPayload.Name values.
const (
	// EventNotify asks the hub UI to show a toast (see NotifyBody).
	EventNotify = "notify"
)

// NotifyBody is EventPayload.Body when Name is EventNotify.
type NotifyBody struct {
	ID         string `json:"id,omitempty"`    // toast id; repeats replace in place
	Message    string `json:"message"`         // user-visible text
	Level      string `json:"level,omitempty"` // "", "success", "error"
	DeviceID   string `json:"device_id,omitempty"`
	Closable   *bool  `json:"closable,omitempty"`     // default true
	AutoHideMs int    `json:"auto_hide_ms,omitempty"` // 0 = stay until dismiss/replace
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
