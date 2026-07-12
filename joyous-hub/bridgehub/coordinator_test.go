package bridgehub

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"joyous-hub/protocol"
)

func TestLongRunningBridgeCmd(t *testing.T) {
	if !longRunningBridgeCmd(protocol.CmdSendImage) {
		t.Fatal("send.image should be long-running")
	}
	if longRunningBridgeCmd(protocol.CmdRefresh) {
		t.Fatal("display.refresh should stay on MQTT thread")
	}
}

// TestPresenceHeartbeatDoesNotReLogOnline covers a bug where Client.heartbeat's 15s
// keepalive re-publish of Hello (see publishHello) caused the coordinator to log
// "bridge online" on every heartbeat tick forever, not just on the real online
// transition, spamming the hub log for every connected bridge indefinitely.
func TestPresenceHeartbeatDoesNotReLogOnline(t *testing.T) {
	c := NewCoordinator(nil, nil)

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	sendHello := func() {
		payload, err := protocol.NewEnvelope(protocol.TypeHello, "testbridge", protocol.HelloPayload{Kind: "inkjoy"})
		if err != nil {
			t.Fatalf("NewEnvelope: %v", err)
		}
		c.HandleMessage(protocol.BridgeTopic("testbridge", "presence"), payload)
	}

	sendHello() // initial connect: must log
	sendHello() // heartbeat within TTL: must NOT log again

	onlineCount := strings.Count(buf.String(), "online")
	if onlineCount != 1 {
		t.Fatalf("got %d \"online\" log lines after connect+heartbeat, want 1:\n%s", onlineCount, buf.String())
	}

	// Simulate the bridge having gone quiet past the presence TTL, then reconnecting —
	// this transition must log again.
	c.mu.Lock()
	rec := c.bridges["testbridge"]
	rec.lastSeen = time.Now().Add(-bridgePresenceTTL - time.Second)
	c.bridges["testbridge"] = rec
	c.mu.Unlock()

	sendHello()

	onlineCount = strings.Count(buf.String(), "online")
	if onlineCount != 2 {
		t.Fatalf("got %d \"online\" log lines after re-connect past TTL, want 2:\n%s", onlineCount, buf.String())
	}
}
