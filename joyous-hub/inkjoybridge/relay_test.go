package inkjoybridge

import (
	"testing"

	"github.com/mochi-mqtt/server/v2/packets"
)

// Frame→cloud messages on the allow list are relayed, except every response
// to a play the bridge injected (play_ack, display_start, display_done);
// anything not on the list stays local.
func TestRelayAllowedButNotInjectedPlayResponses(t *testing.T) {
	const mac = "D0CF13EF4080"
	bs := &bridgeSet{upstreamHost: "127.0.0.1:1"} // link never connects, so relayed messages queue
	h := &frameHook{
		bridges:       bs,
		upstreamAllow: ParseAllowList(DefaultUpstreamAllowCSV() + ",fpga_asset_report"),
		injectedPlay:  func(id string) bool { return id == "injected" },
	}
	ub := bs.get(mac)

	msg := func(action, ackMsgid string) []byte {
		data := map[string]any{}
		if ackMsgid != "" {
			data["ack_msgid"] = ackMsgid
		}
		return BuildBrokerAckPayload(mac, action, "", data)
	}
	sent := []struct {
		payload []byte
		relay   bool
	}{
		{msg("play_ack", "injected"), false},
		{msg("display_start", "injected"), false},
		{msg("display_done", "injected"), false},
		{msg("play_ack", "cloud-play"), true},
		{msg("display_start", "cloud-play"), true},
		{msg("display_done", "cloud-play"), true},
		{msg("fpga_asset_report", ""), true},
		{msg("ota_ack", ""), true},
		{msg("some_new_action", ""), false},
	}
	want := 0
	for _, m := range sent {
		h.OnPublished(nil, packets.Packet{TopicName: "/device/report/" + mac, Payload: m.payload})
		if m.relay {
			want++
		}
	}
	ub.mu.Lock()
	defer ub.mu.Unlock()
	if len(ub.queue) != want {
		t.Fatalf("relayed %d messages, want %d", len(ub.queue), want)
	}
	for _, p := range ub.queue {
		if MQTTAckMsgid(p) == "injected" {
			t.Fatalf("relayed a response to an injected play: %s", p)
		}
	}
}

// Responses to anything the bridge itself sent a frame (image_refresh,
// overlay, wifi_sleep, ...) stay local too, not just plays.
func TestBridgeOriginatedResponsesStayLocal(t *testing.T) {
	const mac = "D0CF13EF4080"
	s := NewServer(Config{Upstream: "127.0.0.1:1"})
	refresh := BuildBrokerAckPayload(mac, "image_refresh", "", nil)
	if err := s.PublishToFrame(mac, refresh); err != nil {
		t.Fatal(err)
	}
	ub := s.bridges.get(mac)
	for _, p := range [][]byte{
		BuildBrokerAckPayload(mac, "image_refresh_ack", MQTTMsgid(refresh), map[string]any{"result": 113}),
		BuildBrokerAckPayload(mac, "image_refresh_ack", "cloud-refresh", map[string]any{"result": 113}),
	} {
		s.hook.OnPublished(nil, packets.Packet{TopicName: "/device/report/" + mac, Payload: p})
	}
	ub.mu.Lock()
	defer ub.mu.Unlock()
	if len(ub.queue) != 1 || MQTTAckMsgid(ub.queue[0]) != "cloud-refresh" {
		t.Fatalf("relayed %d (%q), want only the cloud's refresh ack", len(ub.queue), ub.queue)
	}
}
