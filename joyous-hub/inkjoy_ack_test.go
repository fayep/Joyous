package main

import (
	"encoding/json"
	"testing"
)

func TestInkjoyAckConstants(t *testing.T) {
	if inkjoyAckInterrupted != 104 {
		t.Errorf("inkjoyAckInterrupted=%d want 104 (64+32+8)", inkjoyAckInterrupted)
	}
	if inkjoyAckAccepted != 106 {
		t.Errorf("inkjoyAckAccepted=%d want 106 (64+32+8+2)", inkjoyAckAccepted)
	}
	if inkjoyAckComplete != 113 {
		t.Errorf("inkjoyAckComplete=%d want 113 (64+32+16+1)", inkjoyAckComplete)
	}
	if inkjoyAckAccepted+inkjoyResDone-inkjoyResAccepted != inkjoyAckComplete {
		t.Errorf("accepted→complete transition: got %d want 113",
			inkjoyAckAccepted+inkjoyResDone-inkjoyResAccepted)
	}
	if inkjoyAckAccepted-inkjoyResBitAccepted != inkjoyAckInterrupted {
		t.Errorf("interrupted should be accepted−2: got %d", inkjoyAckInterrupted)
	}
}

func TestInkjoyProgressPercent(t *testing.T) {
	cases := []struct {
		result int
		pct    int
	}{
		{182, 20},
		{184, 40},
		{186, 60},
		{188, 80},
	}
	for _, c := range cases {
		pct, ok := inkjoyProgressPercent(c.result)
		if !ok || pct != c.pct {
			t.Errorf("result=%d: got (%d,%v) want (%d,true)", c.result, pct, ok, c.pct)
		}
	}
	for _, bad := range []int{106, 113, 181, 189, 255} {
		if _, ok := inkjoyProgressPercent(bad); ok {
			t.Errorf("result=%d should not decode as progress", bad)
		}
	}
}

func TestInkjoyProgressResult(t *testing.T) {
	cases := []struct {
		pct    int
		result int
	}{
		{0, 106},
		{10, 106},
		{20, 182},
		{40, 184},
		{60, 186},
		{80, 188},
		{100, 113},
	}
	for _, c := range cases {
		if got := inkjoyProgressResult(c.pct); got != c.result {
			t.Errorf("percent=%d: got %d want %d", c.pct, got, c.result)
		}
	}
}

func TestInkjoyAckResultLabel(t *testing.T) {
	if got := inkjoyAckResultLabel(104); got != "interrupted" {
		t.Errorf("104: got %q", got)
	}
	if got := inkjoyAckResultLabel(186); got != "progress 60%" {
		t.Errorf("186: got %q", got)
	}
}

func TestBuildBlockedOTAAcks(t *testing.T) {
	if inkjoyOTAAckAction("ota") != "ota_ack" {
		t.Fatal("ota ack action")
	}
	if inkjoyOTAAckAction("fpga") != "fpga_ota_ack" {
		t.Fatal("fpga ack action")
	}
	acks := buildBlockedOTAAcks("AABBCCDDEEFF", "ota", "1782010113843")
	if len(acks) != 2 {
		t.Fatalf("len=%d want 2", len(acks))
	}
	var m0, m1 struct {
		Action string `json:"action"`
		Data   struct {
			AckMsgid string `json:"ack_msgid"`
			Result   int    `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(acks[0], &m0); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(acks[1], &m1); err != nil {
		t.Fatal(err)
	}
	if m0.Action != "ota_ack" || m0.Data.Result != 106 || m0.Data.AckMsgid != "1782010113843" {
		t.Errorf("first ack: %+v", m0)
	}
	if m1.Action != "ota_ack" || m1.Data.Result != 104 {
		t.Errorf("second ack: %+v", m1)
	}
}
