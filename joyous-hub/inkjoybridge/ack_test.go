package inkjoybridge

import (
	"encoding/json"
	"testing"
)

func TestBuildLoginAck(t *testing.T) {
	raw := BuildLoginAck("94A990D08BE0", "12345")
	var msg struct {
		Action string `json:"action"`
		Stamac string `json:"stamac"`
		Data   struct {
			AckMsgid string `json:"ack_msgid"`
			DeviceID string `json:"device_id"`
			UID      string `json:"uid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Action != "login_ack" || msg.Stamac != "94A990D08BE0" || msg.Data.AckMsgid != "12345" {
		t.Fatalf("unexpected login_ack: %+v", msg)
	}
	if msg.Data.DeviceID == "" || msg.Data.UID == "" {
		t.Fatal("expected synthetic device_id/uid")
	}
	// Stable across calls for the same MAC.
	raw2 := BuildLoginAck("94A990D08BE0", "999")
	var msg2 struct {
		Data struct {
			DeviceID string `json:"device_id"`
			UID      string `json:"uid"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw2, &msg2)
	if msg2.Data.DeviceID != msg.Data.DeviceID || msg2.Data.UID != msg.Data.UID {
		t.Fatal("session ids should be stable per MAC")
	}
}

func TestBuildHeartAck(t *testing.T) {
	raw := BuildHeartAck("94A990D08BE0", "67890")
	var msg struct {
		Action string `json:"action"`
		Data   struct {
			AckMsgid string `json:"ack_msgid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Action != "heart_ack" || msg.Data.AckMsgid != "67890" {
		t.Fatalf("unexpected heart_ack: %+v", msg)
	}
}
