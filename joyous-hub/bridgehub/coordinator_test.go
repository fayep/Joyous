package bridgehub

import (
	"testing"

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
