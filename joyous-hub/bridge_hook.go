package main

import (
	"strings"

	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/packets"

	"joyous-hub/bridgehub"
)

// bridgeHook forwards joyous/bridge/* publishes to the coordinator and logs hub↔bridge MQTT.
type bridgeHook struct {
	mochi.HookBase
	coord *bridgehub.Coordinator
	log   *MQTTLogBuffer
}

func (h *bridgeHook) ID() string { return "bridge-hook" }

func (h *bridgeHook) Provides(b byte) bool { return b == mochi.OnPublished }

func (h *bridgeHook) OnPublished(_ *mochi.Client, pk packets.Packet) {
	topic := pk.TopicName
	payload := pk.Payload
	if strings.HasPrefix(topic, "joyous/bridge/") {
		if h.log != nil {
			h.log.AddJoyousBridgeToHub(topic, payload)
		}
		if h.coord != nil {
			h.coord.HandleMessage(topic, payload)
		}
		return
	}
	if strings.HasPrefix(topic, "joyous/hub/") && h.log != nil {
		h.log.AddJoyousHubToBridge(topic, payload)
	}
}
