package main

import "testing"

// TestInjectedPlaySuppression: all play_ack results for a hub play share one msgid.
func TestInjectedPlaySuppression(t *testing.T) {
	msgid := "1781902938791"
	if isInjectedPlay(msgid) {
		t.Fatal("unexpected hit before register")
	}
	registerInjectedPlay(msgid)
	for _, result := range []int{106, 200, 255} {
		if !isInjectedPlay(msgid) {
			t.Fatalf("play_ack result=%d should stay suppressed", result)
		}
	}
}
