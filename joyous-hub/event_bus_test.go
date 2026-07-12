package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEventBusUnicastOnlyReachesOwningSession(t *testing.T) {
	b := NewEventBus()
	chA, cancelA := b.Subscribe("a")
	defer cancelA()
	chB, cancelB := b.Subscribe("b")
	defer cancelB()

	b.Publish("a", "send", map[string]string{"status": "downloading"})

	select {
	case payload := <-chA:
		var ev busEvent
		json.Unmarshal(payload, &ev)
		if ev.Type != "send" {
			t.Fatalf("got type %q, want send", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("session a did not receive its own unicast event")
	}

	select {
	case <-chB:
		t.Fatal("session b should not receive a's unicast event")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEventBusBroadcastReachesEverySession(t *testing.T) {
	b := NewEventBus()
	chA, cancelA := b.Subscribe("a")
	defer cancelA()
	chB, cancelB := b.Subscribe("b")
	defer cancelB()

	b.Publish(BroadcastSessionID, "devices", []string{"dev1"})

	for name, ch := range map[string]<-chan []byte{"a": chA, "b": chB} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("session %s did not receive the broadcast event", name)
		}
	}
}

func TestEventBusPublishToUnknownSessionIsNoop(t *testing.T) {
	b := NewEventBus()
	// No subscriber for "ghost" — must not panic or block.
	b.Publish("ghost", "devices", []string{})
}

func TestEventBusFullBufferDropsRatherThanBlocks(t *testing.T) {
	b := NewEventBus()
	ch, cancel := b.Subscribe("a")
	defer cancel()

	// Fill the buffer beyond capacity; none of these publishes may block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < eventSubscriberBufferSize*3; i++ {
			b.Publish("a", "devices", i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked instead of dropping once the buffer filled")
	}
	// Buffer should be full (capacity), not overflowing.
	if len(ch) != eventSubscriberBufferSize {
		t.Fatalf("got buffered len %d, want %d", len(ch), eventSubscriberBufferSize)
	}
}

func TestEventBusUnsubscribeStopsDelivery(t *testing.T) {
	b := NewEventBus()
	ch, cancel := b.Subscribe("a")
	cancel()

	b.Publish("a", "devices", []string{})

	// Channel must be closed, not just empty.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel closed after unsubscribe, got a value")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed after unsubscribe")
	}
}

func TestEventBusReconnectDoesNotClobberNewerSubscriber(t *testing.T) {
	b := NewEventBus()
	_, cancelOld := b.Subscribe("a")
	// Simulate a reconnect: a new subscription for the same session before the old one's
	// cleanup has run.
	chNew, cancelNew := b.Subscribe("a")
	defer cancelNew()

	cancelOld() // must not remove the newer subscriber's entry

	b.Publish("a", "devices", []string{"still-here"})
	select {
	case <-chNew:
	case <-time.After(time.Second):
		t.Fatal("newer subscriber lost its subscription after the older one's cleanup ran")
	}
}
