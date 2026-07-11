package main

import (
	"strings"

	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/packets"

	"joyous-hub/bridgehub"
)

// bridgeHook forwards joyous/bridge/* publishes to the coordinator.
type bridgeHook struct {
	mochi.HookBase
	coord *bridgehub.Coordinator
}

func (h *bridgeHook) ID() string { return "bridge-hook" }

func (h *bridgeHook) Provides(b byte) bool { return b == mochi.OnPublished }

func (h *bridgeHook) OnPublished(_ *mochi.Client, pk packets.Packet) {
	if h.coord == nil {
		return
	}
	topic := pk.TopicName
	if strings.HasPrefix(topic, "joyous/bridge/") {
		h.coord.HandleMessage(topic, pk.Payload)
	}
}
