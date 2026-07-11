package bridgehub

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	mochi "github.com/mochi-mqtt/server/v2"

	"joyous-hub/protocol"
)

// DeviceStore receives device updates from bridges.
type DeviceStore interface {
	SyncBridgeDevices(bridgeID string, devices []protocol.BridgeDevice)
	ApplyBridgeDevice(bridgeID string, device protocol.BridgeDevice)
	RemoveBridgeDevice(bridgeID, deviceID string)
}

// Coordinator listens on the hub broker for bridge traffic and routes commands.
type Coordinator struct {
	broker   *mochi.Server
	store    DeviceStore
	mu       sync.RWMutex
	bridges  map[string]protocol.HelloPayload
	uiState  map[string]protocol.UIStatePayload
	mqttLogs map[string]protocol.MQTTLogsPayload
	onCmd    func(bridgeID string, cmd protocol.CmdPayload)
}

// NewCoordinator attaches bridge protocol hooks to the hub MQTT broker.
func NewCoordinator(broker *mochi.Server, store DeviceStore) *Coordinator {
	c := &Coordinator{
		broker:   broker,
		store:    store,
		bridges:  make(map[string]protocol.HelloPayload),
		uiState:  make(map[string]protocol.UIStatePayload),
		mqttLogs: make(map[string]protocol.MQTTLogsPayload),
	}
	return c
}

// SetCommandHandler receives hub→bridge commands when bridges aren't subscribed inline.
func (c *Coordinator) SetCommandHandler(fn func(bridgeID string, cmd protocol.CmdPayload)) {
	c.onCmd = fn
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
			c.bridges[bridgeID] = hello
			c.mu.Unlock()
			log.Printf("bridgehub: bridge %q online kind=%s", bridgeID, hello.Kind)
		}
	case "devices":
		if env.Type == protocol.TypeDevices && c.store != nil {
			body, _ := protocol.DecodePayload[protocol.DevicesPayload](env)
			c.store.SyncBridgeDevices(bridgeID, body.Devices)
		}
	case "device":
		if env.Type == protocol.TypeDevice && c.store != nil {
			body, _ := protocol.DecodePayload[protocol.DevicePayload](env)
			c.store.ApplyBridgeDevice(bridgeID, body.Device)
		}
	case "ui":
		if env.Type == protocol.TypeUIState {
			body, _ := protocol.DecodePayload[protocol.UIStatePayload](env)
			c.mu.Lock()
			c.uiState[bridgeID] = body
			c.mu.Unlock()
		}
	case "event":
		if env.Type == protocol.TypeEvent {
			body, _ := protocol.DecodePayload[protocol.EventPayload](env)
			log.Printf("bridgehub: event bridge=%s name=%s", bridgeID, body.Name)
		}
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

// BridgeOnline reports whether a bridge has announced presence recently.
func (c *Coordinator) BridgeOnline(bridgeID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.bridges[bridgeID]
	return ok
}

// UIState returns cached bridge tab state.
func (c *Coordinator) UIState(bridgeID string) (protocol.UIStatePayload, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.uiState[bridgeID]
	return s, ok
}

// ListBridges returns connected bridge hello payloads.
func (c *Coordinator) ListBridges() map[string]protocol.HelloPayload {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]protocol.HelloPayload, len(c.bridges))
	for k, v := range c.bridges {
		out[k] = v
	}
	return out
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
	bridgeID string
	kind     string
	client   mqtt.Client
	onCmd    func(protocol.CmdPayload)
	onUI     func(protocol.UIActionPayload)
}

// ClientConfig holds bridge-side hub connection options.
type ClientConfig struct {
	HubMQTT   string // tcp://host:port
	BridgeID  string
	Kind      string
	OnCommand func(protocol.CmdPayload)
	OnUI      func(protocol.UIActionPayload)
}

// Connect dials the hub and announces presence.
func Connect(cfg ClientConfig) (*Client, error) {
	c := &Client{
		bridgeID: cfg.BridgeID,
		kind:     cfg.Kind,
		onCmd:    cfg.OnCommand,
		onUI:     cfg.OnUI,
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
		})
	c.client = mqtt.NewClient(opts)
	tok := c.client.Connect()
	tok.Wait()
	if err := tok.Error(); err != nil {
		return nil, err
	}
	return c, nil
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
	if c.onCmd != nil {
		c.onCmd(cmd)
	}
}

func (c *Client) onUIMessage(_ mqtt.Client, msg mqtt.Message) {
	env, err := protocol.DecodeEnvelope(msg.Payload())
	if err != nil || env.Type != protocol.TypeUIAction {
		return
	}
	action, err := protocol.DecodePayload[protocol.UIActionPayload](env)
	if err != nil {
		return
	}
	if c.onUI != nil {
		c.onUI(action)
	}
}

func (c *Client) publishHello(mc mqtt.Client) {
	payload, _ := protocol.NewEnvelope(protocol.TypeHello, c.bridgeID, protocol.HelloPayload{
		Kind: c.kind,
	})
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

// PublishMQTTLogs sends MQTT debug logs for the InkJoy tab.
func (c *Client) PublishMQTTLogs(logs protocol.MQTTLogsPayload) error {
	payload, err := protocol.NewEnvelope(protocol.TypeMQTTLogs, c.bridgeID, logs)
	if err != nil {
		return err
	}
	tok := c.client.Publish(protocol.BridgeTopic(c.bridgeID, "event"), 0, false, payload)
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
	// Generic JSON round-trip so bridge adapters don't import main.
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
