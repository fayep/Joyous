package main

import (
	"testing"
	"time"
)

func TestWaitInkJoyPlayCompleteSettles(t *testing.T) {
	h := &Hub{sendDelivery: NewSendDeliveryTracker()}
	send := h.sendDelivery.Register("dev", "warm")
	h.sendDelivery.BindInkJoy(send.ID, "m1")

	start := time.Now()
	done := make(chan bool, 1)
	go func() {
		done <- waitInkJoyPlayComplete(h, send.ID, 2*time.Second, 80*time.Millisecond)
	}()
	time.Sleep(10 * time.Millisecond)
	h.sendDelivery.CompleteInkJoy("m1", true)
	if ok := <-done; !ok {
		t.Fatal("expected complete")
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Fatalf("settle too short: %v", elapsed)
	}
}

func TestWaitInkJoyPlayCompleteAckTimeout(t *testing.T) {
	h := &Hub{sendDelivery: NewSendDeliveryTracker()}
	send := h.sendDelivery.Register("dev", "warm")
	h.sendDelivery.BindInkJoy(send.ID, "m1")
	if waitInkJoyPlayComplete(h, send.ID, 20*time.Millisecond, 0) {
		t.Fatal("expected timeout")
	}
}
