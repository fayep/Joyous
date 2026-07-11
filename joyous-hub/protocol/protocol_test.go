package protocol

import (
	"testing"
)

func TestTopics(t *testing.T) {
	if got := BridgeTopic("inkjoy", "presence"); got != "joyous/bridge/inkjoy/presence" {
		t.Fatalf("BridgeTopic: %q", got)
	}
	if got := HubTopic("samsung", "cmd"); got != "joyous/hub/samsung/cmd" {
		t.Fatalf("HubTopic: %q", got)
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	b, err := NewEnvelope(TypeHello, "inkjoy", HelloPayload{Kind: KindInkJoy})
	if err != nil {
		t.Fatal(err)
	}
	env, err := DecodeEnvelope(b)
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != TypeHello {
		t.Fatalf("type: %q", env.Type)
	}
	hello, err := DecodePayload[HelloPayload](env)
	if err != nil || hello.Kind != KindInkJoy {
		t.Fatalf("payload: %+v err=%v", hello, err)
	}
}
