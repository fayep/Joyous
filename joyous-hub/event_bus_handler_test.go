package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleEventsRequiresSessionID(t *testing.T) {
	h := buildTestHub(t)
	rec := httptest.NewRecorder()
	h.handleEvents(rec, httptest.NewRequest("GET", "/api/events", nil))
	if rec.Code != 400 {
		t.Fatalf("status=%d, want 400 without a session id", rec.Code)
	}
}

func TestHandleEventsSendsHandshakeSnapshot(t *testing.T) {
	h := buildTestHub(t)
	h.devices.MarkConnected("AABBCCDDEEFF")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/events?session=sess1", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	h.handleEvents(rec, req)

	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type=%q, want text/event-stream", ct)
	}
	// buildTestHub doesn't wire a bridgeCoord, so "bridges" is correctly absent here (see
	// handleEvents: it's only sent when h.bridgeCoord != nil) — matches production, where a
	// Hub without a coordinator simply has no bridges to report.
	for _, want := range []string{`"type":"devices"`, `"type":"revision"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("handshake snapshot missing %s, got body:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "AABBCCDDEEFF") {
		t.Fatalf("handshake devices snapshot missing the registered device, got body:\n%s", body)
	}
}

func TestHandleEventsIncludesOwnAndBroadcastActiveSends(t *testing.T) {
	h := buildTestHub(t)
	h.sendDelivery.RegisterWithSession("dev1", "img1", "sess1") // this session's own send
	h.sendDelivery.RegisterWithSession("dev2", "img2", "sess2") // someone else's — must not appear
	h.sendDelivery.Register("dev3", "img3")                     // broadcast (e.g. a scheduled send) — must appear

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/events?session=sess1", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	h.handleEvents(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `"device_id":"dev1"`) {
		t.Fatalf("expected own send dev1 in snapshot, got:\n%s", body)
	}
	if !strings.Contains(body, `"device_id":"dev3"`) {
		t.Fatalf("expected broadcast send dev3 in snapshot, got:\n%s", body)
	}
	if strings.Contains(body, `"device_id":"dev2"`) {
		t.Fatalf("did not expect another session's private send dev2 in snapshot, got:\n%s", body)
	}
}

func TestHandleEventsStreamsLiveEvents(t *testing.T) {
	h := buildTestHub(t)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/events?session=sess1", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.handleEvents(rec, req)
		close(done)
	}()

	// Give the handler time to finish its handshake writes and reach the streaming loop, then
	// publish a live event and confirm it lands before we cancel.
	time.Sleep(30 * time.Millisecond)
	h.events.Publish("sess1", "send", map[string]any{"send_id": "liveevent123", "status": "downloading"})
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleEvents did not return after context cancellation")
	}

	if !strings.Contains(rec.Body.String(), "liveevent123") {
		t.Fatalf("expected the live-published event in the stream, got:\n%s", rec.Body.String())
	}
}

func TestPublishSendEventUnicastsToOwningSession(t *testing.T) {
	h := buildTestHub(t)
	owner, cancelOwner := h.events.Subscribe("owner")
	defer cancelOwner()
	other, cancelOther := h.events.Subscribe("other")
	defer cancelOther()

	send := h.sendDelivery.RegisterWithSession("dev1", "img1", "owner")
	h.publishSendEvent(send.ID)

	select {
	case payload := <-owner:
		if !strings.Contains(string(payload), send.ID) {
			t.Fatalf("owner event missing send id, got %s", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("owning session did not receive its send event")
	}
	select {
	case <-other:
		t.Fatal("a different session should not receive another session's send event")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPublishSendEventBroadcastsForSystemTriggeredSend(t *testing.T) {
	h := buildTestHub(t)
	a, cancelA := h.events.Subscribe("a")
	defer cancelA()
	b, cancelB := h.events.Subscribe("b")
	defer cancelB()

	send := h.sendDelivery.Register("dev1", "img1") // no owning session (e.g. a scheduled send)
	h.publishSendEvent(send.ID)

	for name, ch := range map[string]<-chan []byte{"a": a, "b": b} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("session %s did not receive the broadcast send event", name)
		}
	}
}

func TestPublishSendEventIncludesDeviceType(t *testing.T) {
	h := buildTestHub(t)
	h.devices.MarkConnected("AABBCCDDEEFF") // registers as an inkjoy device
	ch, cancel := h.events.Subscribe("owner")
	defer cancel()

	send := h.sendDelivery.RegisterWithSession("AABBCCDDEEFF", "img1", "owner")
	h.publishSendEvent(send.ID)

	select {
	case payload := <-ch:
		if !strings.Contains(string(payload), `"device_type":"inkjoy"`) {
			t.Fatalf("expected device_type in send event, got %s", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive the send event")
	}
}

func TestPublishErrorBroadcastsToEverySession(t *testing.T) {
	h := buildTestHub(t)
	a, cancelA := h.events.Subscribe("a")
	defer cancelA()
	b, cancelB := h.events.Subscribe("b")
	defer cancelB()

	h.publishError("scheduled send: album empty", map[string]any{"device_id": "dev1", "album_id": "alb1"})

	for name, ch := range map[string]<-chan []byte{"a": a, "b": b} {
		select {
		case payload := <-ch:
			body := string(payload)
			if !strings.Contains(body, `"type":"error"`) || !strings.Contains(body, "album empty") || !strings.Contains(body, "dev1") {
				t.Fatalf("session %s got unexpected error event: %s", name, body)
			}
		case <-time.After(time.Second):
			t.Fatalf("session %s did not receive the broadcast error event", name)
		}
	}
}
