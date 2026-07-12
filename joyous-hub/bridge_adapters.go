package main

import (
	"encoding/json"
	"time"

	"joyous-hub/bridgehub"
	"joyous-hub/inkjoybridge"
	"joyous-hub/protocol"
)

// inkjoyDeviceAdapter forwards frame telemetry from the InkJoy bridge to the registry.
type inkjoyDeviceAdapter struct {
	reg *DeviceRegistry
}

func (a *inkjoyDeviceAdapter) MarkConnected(mac string) { a.reg.MarkConnected(mac) }

func (a *inkjoyDeviceAdapter) MarkDisconnected(mac string) { a.reg.MarkDisconnected(mac) }

func (a *inkjoyDeviceAdapter) UpdateLogin(mac string, info inkjoybridge.LoginInfo) {
	a.reg.UpdateLogin(mac, info)
}

func (a *inkjoyDeviceAdapter) UpdateHeart(mac string, info inkjoybridge.HeartInfo) {
	a.reg.UpdateHeart(mac, info)
}

func (a *inkjoyDeviceAdapter) SetHubIP(mac, ip string) { a.reg.SetHubIP(mac, ip) }

func (a *inkjoyDeviceAdapter) OnPlayAck(ackMsgid string, result int) {}

func (a *inkjoyDeviceAdapter) OnDeviceLogin(mac string) {}

func (a *inkjoyDeviceAdapter) OnDeviceHeart(mac string) {}

func (a *inkjoyDeviceAdapter) MarkShutdown(mac string, info inkjoybridge.SleepInfo) {
	a.reg.MarkShutdown(mac, info)
}

// captureAdapter adapts MessageCapture to inkjoybridge.CaptureSink.
type captureAdapter struct{ c *MessageCapture }

func (a *captureAdapter) RecordUpstream(mac, action string, payload []byte) error {
	if a.c == nil {
		return nil
	}
	return a.c.RecordUpstream(mac, action, payload)
}

func (a *captureAdapter) RecordDownstream(mac, action string, payload []byte) error {
	if a.c == nil {
		return nil
	}
	return a.c.RecordDownstream(mac, action, payload)
}

func (a *captureAdapter) RecordIntercepted(mac, action string, payload []byte) error {
	if a.c == nil {
		return nil
	}
	return a.c.RecordIntercepted(mac, action, payload)
}

// otaAdapter adapts OTACapture to inkjoybridge.OTASink.
type otaAdapter struct{ o *OTACapture }

func (a *otaAdapter) Handle(mac, action string, payload []byte) {
	if a.o != nil {
		a.o.Handle(mac, action, payload)
	}
}

// mqttLogAdapter adapts MQTTLogBuffer to inkjoybridge.MQTTLogger.
type mqttLogAdapter struct{ l *MQTTLogBuffer }

func (a *mqttLogAdapter) AddLocal(side, topic string, payload []byte, note string) {
	if a.l != nil {
		a.l.AddLocal(side, topic, payload, note)
	}
}

func (a *mqttLogAdapter) AddUpstream(side, topic string, payload []byte, note string) {
	if a.l != nil {
		a.l.AddUpstream(side, topic, payload, note)
	}
}

// playRelayAdapter wires play relay helpers into the InkJoy bridge.
type playRelayAdapter struct {
	hub *Hub
}

func (a *playRelayAdapter) RewritePlay(mac string, payload []byte) ([]byte, error) {
	if a.hub == nil {
		return payload, nil
	}
	return a.hub.rewriteExternalPlay(mac, payload)
}

func (a *playRelayAdapter) OnExternalPlay(mac string, payload []byte) {
	if a.hub != nil {
		a.hub.handleExternalPlay(mac, payload)
	}
}

// SyncBridgeDevices replaces devices owned by a bridge with a fresh snapshot.
// Hub-observed Samsung contacts (HTTP pulls on :18080) are preserved when newer
// than the bridge snapshot — otherwise devices.sync every 10s marks frames offline.
func (r *DeviceRegistry) SyncBridgeDevices(bridgeID string, devices []protocol.BridgeDevice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	type contact struct {
		lastSeen        time.Time
		lastAction      string
		deepSleepActive bool
	}
	preserved := make(map[string]contact)
	for id, d := range r.m {
		if d.bridgeID == bridgeID {
			preserved[id] = contact{
				lastSeen:        d.LastSeen,
				lastAction:      d.LastAction,
				deepSleepActive: d.DeepSleepActive,
			}
			delete(r.m, id)
		}
	}
	for _, bd := range devices {
		r.applyBridgeDeviceLocked(bridgeID, bd)
		p, ok := preserved[bd.ID]
		if !ok {
			continue
		}
		d := r.m[bd.ID]
		if d == nil {
			continue
		}
		if p.lastSeen.After(d.LastSeen) {
			d.LastSeen = p.lastSeen
			if p.lastAction != "" {
				d.LastAction = p.lastAction
			}
			// Hub cleared deep sleep after frame contact; don't let a stale bridge
			// snapshot revive the sticky UI flag.
			if !p.deepSleepActive {
				d.DeepSleepActive = false
			}
		}
		if d.Type == DeviceTypeSamsung {
			ApplySamsungConnected(d)
		}
	}
}

// ApplyBridgeDevice upserts a single device reported by a bridge.
func (r *DeviceRegistry) ApplyBridgeDevice(bridgeID string, device protocol.BridgeDevice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applyBridgeDeviceLocked(bridgeID, device)
}

// RemoveBridgeDevice drops a device from the registry.
func (r *DeviceRegistry) RemoveBridgeDevice(bridgeID, deviceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.m[deviceID]; ok && d.bridgeID == bridgeID {
		delete(r.m, deviceID)
	}
}

func (r *DeviceRegistry) applyBridgeDeviceLocked(bridgeID string, bd protocol.BridgeDevice) {
	d := r.m[bd.ID]
	if d == nil {
		d = &Device{ID: bd.ID}
		r.m[bd.ID] = d
	}
	d.bridgeID = bridgeID
	d.Type = DeviceType(bd.Type)
	d.Name = bd.Name
	d.MAC = bd.MAC
	d.IP = bd.IP
	d.USN = bd.USN
	d.Firmware = bd.Firmware
	d.Battery = bd.Battery
	d.PowerSource = bd.PowerSource
	d.RSSI = bd.RSSI
	d.Connected = bd.Connected
	if !bd.LastSeen.IsZero() {
		d.LastSeen = bd.LastSeen
	}
	d.LastAction = bd.LastAction
	d.SleepBeginTime = bd.SleepBeginTime
	d.SleepEndTime = bd.SleepEndTime
	d.Portrait = bd.Portrait
	d.HubIP = bd.HubIP
	d.MDCPin = bd.MDCPin
	d.DisplayCropFormat = bd.DisplayCropFormat
	d.DisplayWidth = bd.DisplayWidth
	d.DisplayHeight = bd.DisplayHeight
	d.DeepSleepActive = bd.DeepSleepActive
	if d.Type == DeviceTypeSamsung {
		ApplySamsungConnected(d)
	}
}

func deviceToBridge(d Device) protocol.BridgeDevice {
	return protocol.BridgeDevice{
		ID:                d.ID,
		Type:              string(d.Type),
		Name:              d.Name,
		MAC:               d.MAC,
		IP:                d.IP,
		USN:               d.USN,
		Firmware:          d.Firmware,
		Battery:           d.Battery,
		PowerSource:       d.PowerSource,
		RSSI:              d.RSSI,
		Connected:         d.Connected,
		LastSeen:          d.LastSeen,
		LastAction:        d.LastAction,
		SleepBeginTime:    d.SleepBeginTime,
		SleepEndTime:      d.SleepEndTime,
		Portrait:          d.Portrait,
		HubIP:             d.HubIP,
		MDCPin:            d.MDCPin,
		DisplayCropFormat: d.DisplayCropFormat,
		DisplayWidth:      d.DisplayWidth,
		DisplayHeight:     d.DisplayHeight,
		DeepSleepActive:   d.DeepSleepActive,
	}
}

func bridgeDevicesFromRegistry(reg *DeviceRegistry, kind DeviceType) []protocol.BridgeDevice {
	devs := reg.List()
	out := make([]protocol.BridgeDevice, 0)
	for _, d := range devs {
		if d.Type != kind {
			continue
		}
		if d.Type == DeviceTypeSamsung {
			ApplySamsungConnected(&d)
		}
		out = append(out, deviceToBridge(d))
	}
	return out
}

func marshalBridgeUI(devs []protocol.BridgeDevice) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"devices": devs, "at": time.Now().UTC()})
	return b
}

// bridgeCoordinatorPublisher sends InkJoy frame commands via the inkjoy bridge.
type bridgeCoordinatorPublisher struct {
	coord *bridgehub.Coordinator
}

func (p *bridgeCoordinatorPublisher) Publish(topic string, payload []byte) error {
	mac, ok := inkjoybridge.ExtractTopicMAC(topic)
	if !ok {
		return nil
	}
	// Forward as opaque MQTT publish command to the inkjoy bridge.
	body, _ := json.Marshal(map[string]string{
		"topic":   topic,
		"payload": string(payload),
	})
	return p.coord.PublishCommand(protocol.KindInkJoy, protocol.CmdPayload{
		Cmd:      "mqtt.publish",
		DeviceID: mac,
		Body:     body,
	})
}
