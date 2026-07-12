package bridgehub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"slices"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	mochi "github.com/mochi-mqtt/server/v2"

	"joyous-hub/protocol"
)

const bridgePresenceTTL = 45 * time.Second
const defaultUIHTTPTimeout = 20 * time.Second

// DeviceStore receives device updates from bridges.
type DeviceStore interface {
	SyncBridgeDevices(bridgeID string, devices []protocol.BridgeDevice)
	ApplyBridgeDevice(bridgeID string, device protocol.BridgeDevice)
	RemoveBridgeDevice(bridgeID, deviceID string)
}

type bridgeRecord struct {
	hello    protocol.HelloPayload
	lastSeen time.Time
	// reportedOffline tracks whether checkStaleBridges has already fired onBridgesChanged for
	// this bridge going silent, so a sweep tick doesn't re-fire every cycle while it stays
	// offline. Reset implicitly: a fresh Hello replaces the whole bridgeRecord (see
	// handleBridgeTopic's "presence" case), defaulting this back to false.
	reportedOffline bool
}

// BridgeStatus is the hub-facing view of a connected bridge.
type BridgeStatus struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Online       bool     `json:"online"`
	Capabilities []string `json:"capabilities,omitempty"`
	HasConfigUI  bool     `json:"has_config_ui"`
	ListenHTTP   string   `json:"listen_http,omitempty"`
	ListenMQTT   string   `json:"listen_mqtt,omitempty"`
}

// Coordinator listens on the hub broker for bridge traffic and routes commands.
type Coordinator struct {
	broker   *mochi.Server
	store    DeviceStore
	mu       sync.RWMutex
	bridges  map[string]bridgeRecord
	uiState  map[string]protocol.UIStatePayload
	pending          map[string]chan protocol.UIHTTPResponsePayload
	onCmd            func(bridgeID string, cmd protocol.CmdPayload)
	onSendComplete   func(body protocol.SendCompletePayload)
	onBridgesChanged func()
	onDevicesChanged func()
}

// NewCoordinator attaches bridge protocol hooks to the hub MQTT broker.
func NewCoordinator(broker *mochi.Server, store DeviceStore) *Coordinator {
	c := &Coordinator{
		broker:   broker,
		store:    store,
		bridges:  make(map[string]bridgeRecord),
		uiState:  make(map[string]protocol.UIStatePayload),
		pending:  make(map[string]chan protocol.UIHTTPResponsePayload),
	}
	return c
}

// SetCommandHandler receives hub→bridge commands when bridges aren't subscribed inline.
func (c *Coordinator) SetCommandHandler(fn func(bridgeID string, cmd protocol.CmdPayload)) {
	c.onCmd = fn
}

// SetSendCompleteHandler receives bridge send delivery reports.
func (c *Coordinator) SetSendCompleteHandler(fn func(body protocol.SendCompletePayload)) {
	c.onSendComplete = fn
}

// SetBridgesChangedHandler is called after a bridge's presence/capabilities change (a Hello
// that's a genuine online transition — see the wasOnline check in handleBridgeTopic, not every
// 15s heartbeat). Lets the hub push a fresh ListBridgeStatus() snapshot to connected sessions
// instead of them polling for it.
func (c *Coordinator) SetBridgesChangedHandler(fn func()) {
	c.onBridgesChanged = fn
}

// SetDevicesChangedHandler is called after a bridge reports a device sync or delta (the
// "devices"/"device" topics — the single funnel every bridge's device state flows through).
// Lets the hub push a fresh device list to connected sessions instead of them polling for it.
func (c *Coordinator) SetDevicesChangedHandler(fn func()) {
	c.onDevicesChanged = fn
}

// StartStalenessSweep periodically checks for bridges that have gone silent past
// bridgePresenceTTL and fires onBridgesChanged for that transition. A bridge going *online* is
// announced immediately by its own Hello (see handleBridgeTopic), but nothing else observes it
// going *offline* — "online" is otherwise only computed lazily, whenever something happens to
// call ListBridgeStatus/BridgeOnline — so without this sweep, a silently-disconnected bridge
// would never get reported to already-connected sessions at all.
func (c *Coordinator) StartStalenessSweep(ctx context.Context) {
	ticker := time.NewTicker(bridgePresenceTTL / 3)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.checkStaleBridges()
			}
		}
	}()
}

func (c *Coordinator) checkStaleBridges() {
	c.mu.Lock()
	wentStale := false
	for id, rec := range c.bridges {
		if bridgeOnlineLocked(rec) || rec.reportedOffline {
			continue
		}
		rec.reportedOffline = true
		c.bridges[id] = rec
		wentStale = true
	}
	c.mu.Unlock()
	if wentStale && c.onBridgesChanged != nil {
		c.onBridgesChanged()
	}
}

// AttachClient subscribes a Paho client to bridge topics (for tests or external broker).
func (c *Coordinator) AttachClient(client mqtt.Client) {
	client.Subscribe("joyous/bridge/+/+", 1, c.onBridgeMessage)
}

func (c *Coordinator) onBridgeMessage(_ mqtt.Client, msg mqtt.Message) {
	c.handleBridgeTopic(msg.Topic(), msg.Payload())
}

// HandleMessage processes a bridge→hub publish (inline broker hook).
func (c *Coordinator) HandleMessage(topic string, payload []byte) {
	c.handleBridgeTopic(topic, payload)
}

func (c *Coordinator) handleBridgeTopic(topic string, payload []byte) {
	parts := splitTopic(topic)
	if len(parts) < 4 || parts[0] != "joyous" || parts[1] != "bridge" {
		return
	}
	bridgeID := parts[2]
	suffix := parts[3]

	env, err := protocol.DecodeEnvelope(payload)
	if err != nil {
		log.Printf("bridgehub: bad envelope on %s: %v", topic, err)
		return
	}

	switch suffix {
	case "presence":
		if env.Type == protocol.TypeHello {
			hello, _ := protocol.DecodePayload[protocol.HelloPayload](env)
			c.mu.Lock()
			prev, known := c.bridges[bridgeID]
			wasOnline := known && time.Since(prev.lastSeen) <= bridgePresenceTTL
			c.bridges[bridgeID] = bridgeRecord{hello: hello, lastSeen: time.Now()}
			c.mu.Unlock()
			// publishHello also runs as a 15s keepalive heartbeat (see Client.heartbeat),
			// not just on connect — only log/broadcast when the bridge actually transitions
			// from unknown/offline to online, or every heartbeat tick spams the log (and,
			// now, every connected session) forever for no actual change.
			if !wasOnline {
				log.Printf("bridgehub: bridge %q online kind=%s caps=%v", bridgeID, hello.Kind, hello.Capabilities)
				if c.onBridgesChanged != nil {
					c.onBridgesChanged()
				}
			}
		}
	case "devices":
		if env.Type == protocol.TypeDevices && c.store != nil {
			body, _ := protocol.DecodePayload[protocol.DevicesPayload](env)
			c.store.SyncBridgeDevices(bridgeID, body.Devices)
			if c.onDevicesChanged != nil {
				c.onDevicesChanged()
			}
		}
	case "device":
		if env.Type == protocol.TypeDevice && c.store != nil {
			body, _ := protocol.DecodePayload[protocol.DevicePayload](env)
			c.store.ApplyBridgeDevice(bridgeID, body.Device)
			if c.onDevicesChanged != nil {
				c.onDevicesChanged()
			}
		}
	case "ui":
		switch env.Type {
		case protocol.TypeUIState:
			body, _ := protocol.DecodePayload[protocol.UIStatePayload](env)
			c.mu.Lock()
			c.uiState[bridgeID] = body
			c.mu.Unlock()
		case protocol.TypeUIHTTPResponse:
			body, _ := protocol.DecodePayload[protocol.UIHTTPResponsePayload](env)
			c.deliverUIHTTPResponse(body)
		}
	case "event":
		switch env.Type {
		case protocol.TypeEvent:
			body, _ := protocol.DecodePayload[protocol.EventPayload](env)
			log.Printf("bridgehub: event bridge=%s name=%s", bridgeID, body.Name)
		case protocol.TypeSendComplete:
			body, _ := protocol.DecodePayload[protocol.SendCompletePayload](env)
			if c.onSendComplete != nil && body.SendID != "" {
				c.onSendComplete(body)
			}
		}
	}
}

func (c *Coordinator) deliverUIHTTPResponse(resp protocol.UIHTTPResponsePayload) {
	c.mu.Lock()
	ch := c.pending[resp.RequestID]
	if ch != nil {
		delete(c.pending, resp.RequestID)
	}
	c.mu.Unlock()
	if ch != nil {
		ch <- resp
	}
}

// PublishCommand sends a command to a bridge via the hub broker.
func (c *Coordinator) PublishCommand(bridgeID string, cmd protocol.CmdPayload) error {
	payload, err := protocol.NewEnvelope(protocol.TypeCmd, bridgeID, cmd)
	if err != nil {
		return err
	}
	topic := protocol.HubTopic(bridgeID, "cmd")
	if c.broker != nil {
		return c.broker.Publish(topic, payload, false, 1)
	}
	return nil
}

// PublishUIAction forwards a UI action to a bridge.
func (c *Coordinator) PublishUIAction(bridgeID string, action protocol.UIActionPayload) error {
	payload, err := protocol.NewEnvelope(protocol.TypeUIAction, bridgeID, action)
	if err != nil {
		return err
	}
	topic := protocol.HubTopic(bridgeID, "ui")
	if c.broker != nil {
		return c.broker.Publish(topic, payload, false, 1)
	}
	return nil
}

// ProxyBridgeHTTP tunnels an HTTP request to a bridge over MQTT and waits for the response.
// The hub does not serve /{bridge_id}/… itself; paths under each bridge prefix are owned by the bridge.
func (c *Coordinator) ProxyBridgeHTTP(ctx context.Context, bridgeID string, req protocol.UIHTTPRequestPayload) (protocol.UIHTTPResponsePayload, error) {
	if !c.BridgeOnline(bridgeID) {
		return protocol.UIHTTPResponsePayload{}, errors.New("bridge offline")
	}
	if req.RequestID == "" {
		req.RequestID = newRequestID()
	}
	ch := make(chan protocol.UIHTTPResponsePayload, 1)
	c.mu.Lock()
	c.pending[req.RequestID] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, req.RequestID)
		c.mu.Unlock()
	}()

	payload, err := protocol.NewEnvelope(protocol.TypeUIHTTPRequest, bridgeID, req)
	if err != nil {
		return protocol.UIHTTPResponsePayload{}, err
	}
	topic := protocol.HubTopic(bridgeID, "ui")
	if c.broker == nil {
		return protocol.UIHTTPResponsePayload{}, errors.New("broker unavailable")
	}
	if err := c.broker.Publish(topic, payload, false, 1); err != nil {
		return protocol.UIHTTPResponsePayload{}, err
	}

	timeout := defaultUIHTTPTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d > 0 && d < timeout {
			timeout = d
		}
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return protocol.UIHTTPResponsePayload{}, ctx.Err()
	case <-time.After(timeout):
		return protocol.UIHTTPResponsePayload{}, errors.New("bridge HTTP timeout")
	}
}

// ProxyUIHTTP is an alias for ProxyBridgeHTTP (bridge-owned HTTP under /{bridge_id}/…).
func (c *Coordinator) ProxyUIHTTP(ctx context.Context, bridgeID string, req protocol.UIHTTPRequestPayload) (protocol.UIHTTPResponsePayload, error) {
	return c.ProxyBridgeHTTP(ctx, bridgeID, req)
}

// bridgeOnlineLocked reports whether rec is within the presence TTL. Callers
// must hold c.mu (read or write).
func bridgeOnlineLocked(rec bridgeRecord) bool {
	return time.Since(rec.lastSeen) <= bridgePresenceTTL
}

// BridgeOnline reports whether a bridge has announced presence recently.
//
// This and HasCapability each take their own lock and check the TTL against
// time.Now() at the moment they're called — two calls made from different,
// causally-unrelated requests (e.g. a UI list request, then a later proxy
// request) can therefore disagree if lastSeen crosses the TTL boundary
// between them. That's an inherent property of polling a freshness window
// from independent call sites, not a fixable bug in either method itself;
// don't rely on one call's result still being true by the time of another.
func (c *Coordinator) BridgeOnline(bridgeID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rec, ok := c.bridges[bridgeID]
	return ok && bridgeOnlineLocked(rec)
}

// HasCapability reports whether an online bridge advertises a capability.
// See BridgeOnline's doc comment for the same-TTL caveat.
func (c *Coordinator) HasCapability(bridgeID, cap string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rec, ok := c.bridges[bridgeID]
	if !ok || !bridgeOnlineLocked(rec) {
		return false
	}
	return slices.Contains(rec.hello.Capabilities, cap)
}

// BridgeForPath returns the ID of the online bridge that has registered path as an extra
// HTTP path via HelloPayload.HTTPPaths, if any. Used by the hub's catch-all HTTP route to
// forward vendor protocol callbacks (paths not namespaced under a bridge's own /{kind}/
// proxy prefix) to the bridge that owns them, instead of serving the SPA.
func (c *Coordinator) BridgeForPath(path string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for id, rec := range c.bridges {
		if !bridgeOnlineLocked(rec) {
			continue
		}
		if slices.Contains(rec.hello.HTTPPaths, path) {
			return id, true
		}
	}
	return "", false
}

// UIState returns cached bridge tab state.
func (c *Coordinator) UIState(bridgeID string) (protocol.UIStatePayload, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.uiState[bridgeID]
	return s, ok
}

// ListBridges returns connected bridge hello payloads (legacy map).
func (c *Coordinator) ListBridges() map[string]protocol.HelloPayload {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]protocol.HelloPayload, len(c.bridges))
	for k, rec := range c.bridges {
		if time.Since(rec.lastSeen) <= bridgePresenceTTL {
			out[k] = rec.hello
		}
	}
	return out
}

// ListBridgeStatus returns hub-facing bridge status for the SPA.
func (c *Coordinator) ListBridgeStatus() []BridgeStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]BridgeStatus, 0, len(c.bridges))
	for id, rec := range c.bridges {
		online := time.Since(rec.lastSeen) <= bridgePresenceTTL
		if !online {
			continue
		}
		hasUI := false
		for _, cap := range rec.hello.Capabilities {
			if cap == protocol.CapConfigUI {
				hasUI = true
				break
			}
		}
		out = append(out, BridgeStatus{
			ID:           id,
			Kind:         rec.hello.Kind,
			Online:       true,
			Capabilities: rec.hello.Capabilities,
			HasConfigUI:  hasUI,
			ListenHTTP:   rec.hello.ListenHTTP,
			ListenMQTT:   rec.hello.ListenMQTT,
		})
	}
	return out
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func splitTopic(topic string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(topic); i++ {
		if topic[i] == '/' {
			if i > start {
				parts = append(parts, topic[start:i])
			}
			start = i + 1
		}
	}
	if start < len(topic) {
		parts = append(parts, topic[start:])
	}
	return parts
}

// Client connects a bridge process to the hub broker.
type Client struct {
	bridgeID      string
	kind          string
	client        mqtt.Client
	onCmd         func(protocol.CmdPayload)
	onUI          func(protocol.UIActionPayload)
	onReconnect   func(*Client)
	uiHTTP        UIHTTPHandler
	hello         protocol.HelloPayload
	heartbeatOnce sync.Once
}

// UIHTTPHandler serves bridge-owned configuration pages.
type UIHTTPHandler interface {
	ServeUIHTTP(method, path string, headers map[string]string, body []byte) (status int, contentType string, respHeaders map[string]string, respBody []byte)
}

// ClientConfig holds bridge-side hub connection options.
type ClientConfig struct {
	HubMQTT   string // tcp://host:port
	BridgeID  string
	Kind      string
	Hello     protocol.HelloPayload
	OnCommand func(protocol.CmdPayload)
	OnUI      func(protocol.UIActionPayload)
	// OnReconnect, if set, is called every time the underlying MQTT connection is
	// (re)established — including the first connect — so the bridge can gratuitously
	// republish its device list. Without this, a bridge that only pushes devices on its own
	// periodic timer leaves the hub's device list empty/stale from a hub restart (which drops
	// the in-process broker's retained state) until that timer next fires — for a bridge with a
	// multi-minute refresh interval, that's a long, confusing gap. The live *Client is passed in
	// rather than relying on a closure over an outer variable, since this can fire before
	// Connect returns (and before that outer variable would be assigned).
	OnReconnect func(*Client)
	UIHTTP      UIHTTPHandler
}

// Connect dials the hub and announces presence.
func Connect(cfg ClientConfig) (*Client, error) {
	c := &Client{
		bridgeID:    cfg.BridgeID,
		kind:        cfg.Kind,
		onCmd:       cfg.OnCommand,
		onUI:        cfg.OnUI,
		onReconnect: cfg.OnReconnect,
		uiHTTP:      cfg.UIHTTP,
		hello:       cfg.Hello,
	}
	if c.hello.Kind == "" {
		c.hello.Kind = cfg.Kind
	}
	opts := mqtt.NewClientOptions().
		AddBroker(cfg.HubMQTT).
		SetClientID("bridge-" + cfg.BridgeID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetOnConnectHandler(func(mc mqtt.Client) {
			mc.Subscribe(protocol.HubTopic(cfg.BridgeID, "cmd"), 1, c.onCmdMessage)
			mc.Subscribe(protocol.HubTopic(cfg.BridgeID, "ui"), 1, c.onUIMessage)
			c.publishHello(mc)
			// paho reuses the same mqtt.Client across auto-reconnects, so
			// OnConnectHandler fires again on every reconnect — start the
			// heartbeat loop only once per Client, not once per connection,
			// or each reconnect leaks another permanently-running goroutine.
			c.heartbeatOnce.Do(func() { go c.heartbeat(mc) })
			if c.onReconnect != nil {
				// Run off the MQTT callback goroutine: onReconnect typically publishes
				// (network round trip via tok.Wait()), which must not block the client's
				// internal connection-handling goroutine.
				go c.onReconnect(c)
			}
		})
	c.client = mqtt.NewClient(opts)
	tok := c.client.Connect()
	tok.Wait()
	if err := tok.Error(); err != nil {
		return nil, err
	}
	return c, nil
}

// heartbeat runs once for the lifetime of the Client (see heartbeatOnce in
// Connect). mc is the same underlying mqtt.Client across auto-reconnects, so
// this single loop just skips publishing while disconnected rather than
// exiting — exiting here would mean no heartbeat resumes after a reconnect.
func (c *Client) heartbeat(mc mqtt.Client) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for range tick.C {
		if mc == nil || !mc.IsConnected() {
			continue
		}
		c.publishHello(mc)
	}
}

func (c *Client) onCmdMessage(_ mqtt.Client, msg mqtt.Message) {
	env, err := protocol.DecodeEnvelope(msg.Payload())
	if err != nil || env.Type != protocol.TypeCmd {
		return
	}
	cmd, err := protocol.DecodePayload[protocol.CmdPayload](env)
	if err != nil {
		return
	}
	if c.onCmd == nil {
		return
	}
	// Encoding and other heavy work must not run on the MQTT callback thread or
	// hub→bridge UI HTTP tunneled over the same client will time out.
	if longRunningBridgeCmd(cmd.Cmd) {
		go c.onCmd(cmd)
		return
	}
	c.onCmd(cmd)
}

func longRunningBridgeCmd(cmd string) bool {
	switch cmd {
	case protocol.CmdSendImage:
		return true
	default:
		return false
	}
}

func (c *Client) onUIMessage(_ mqtt.Client, msg mqtt.Message) {
	env, err := protocol.DecodeEnvelope(msg.Payload())
	if err != nil {
		return
	}
	switch env.Type {
	case protocol.TypeUIAction:
		action, err := protocol.DecodePayload[protocol.UIActionPayload](env)
		if err != nil {
			return
		}
		if c.onUI != nil {
			c.onUI(action)
		}
	case protocol.TypeUIHTTPRequest:
		req, err := protocol.DecodePayload[protocol.UIHTTPRequestPayload](env)
		if err != nil {
			return
		}
		go c.handleUIHTTPRequest(req)
	}
}

func (c *Client) handleUIHTTPRequest(req protocol.UIHTTPRequestPayload) {
	if c.uiHTTP == nil {
		c.publishUIHTTPResponse(req.RequestID, 503, "text/plain", nil, []byte("bridge UI unavailable"))
		return
	}
	status, ct, hdrs, body := c.uiHTTP.ServeUIHTTP(req.Method, req.Path, req.Headers, req.Body)
	c.publishUIHTTPResponse(req.RequestID, status, ct, hdrs, body)
}

func (c *Client) publishUIHTTPResponse(requestID string, status int, contentType string, headers map[string]string, body []byte) {
	resp := protocol.UIHTTPResponsePayload{
		RequestID:   requestID,
		Status:      status,
		ContentType: contentType,
		Headers:     headers,
		Body:        body,
	}
	payload, err := protocol.NewEnvelope(protocol.TypeUIHTTPResponse, c.bridgeID, resp)
	if err != nil {
		return
	}
	topic := protocol.BridgeTopic(c.bridgeID, "ui")
	if c.client != nil && c.client.IsConnected() {
		c.client.Publish(topic, 1, false, payload)
	}
}

func (c *Client) publishHello(mc mqtt.Client) {
	hello := c.hello
	if hello.Kind == "" {
		hello.Kind = c.kind
	}
	payload, _ := protocol.NewEnvelope(protocol.TypeHello, c.bridgeID, hello)
	topic := protocol.BridgeTopic(c.bridgeID, "presence")
	mc.Publish(topic, 1, true, payload)
}

// PublishDevices sends a full device snapshot to the hub.
func (c *Client) PublishDevices(devices []protocol.BridgeDevice) error {
	payload, err := protocol.NewEnvelope(protocol.TypeDevices, c.bridgeID, protocol.DevicesPayload{Devices: devices})
	if err != nil {
		return err
	}
	tok := c.client.Publish(protocol.BridgeTopic(c.bridgeID, "devices"), 1, true, payload)
	tok.Wait()
	return tok.Error()
}

// PublishDevice sends a single device update.
func (c *Client) PublishDevice(device protocol.BridgeDevice) error {
	payload, err := protocol.NewEnvelope(protocol.TypeDevice, c.bridgeID, protocol.DevicePayload{Device: device})
	if err != nil {
		return err
	}
	tok := c.client.Publish(protocol.BridgeTopic(c.bridgeID, "device"), 1, false, payload)
	tok.Wait()
	return tok.Error()
}

// PublishUIState publishes bridge-owned tab state.
func (c *Client) PublishUIState(state protocol.UIStatePayload) error {
	payload, err := protocol.NewEnvelope(protocol.TypeUIState, c.bridgeID, state)
	if err != nil {
		return err
	}
	tok := c.client.Publish(protocol.BridgeTopic(c.bridgeID, "ui"), 1, true, payload)
	tok.Wait()
	return tok.Error()
}

// PublishSendComplete notifies the hub of send delivery.
func (c *Client) PublishSendComplete(body protocol.SendCompletePayload) error {
	payload, err := protocol.NewEnvelope(protocol.TypeSendComplete, c.bridgeID, body)
	if err != nil {
		return err
	}
	tok := c.client.Publish(protocol.BridgeTopic(c.bridgeID, "event"), 1, false, payload)
	tok.Wait()
	return tok.Error()
}

// Disconnect closes the hub connection.
func (c *Client) Disconnect() {
	if c.client != nil && c.client.IsConnected() {
		c.client.Disconnect(250)
	}
}

// DeviceFromRegistry converts a hub Device to protocol.BridgeDevice.
func DeviceFromRegistry(id, typ string, d any) protocol.BridgeDevice {
	b, _ := json.Marshal(d)
	var out protocol.BridgeDevice
	_ = json.Unmarshal(b, &out)
	if out.ID == "" {
		out.ID = id
	}
	if out.Type == "" {
		out.Type = typ
	}
	return out
}
