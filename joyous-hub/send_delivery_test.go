package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSendDeliveryWait(t *testing.T) {
	tr := NewSendDeliveryTracker()
	send := tr.Register("dev", "img")
	tr.BindInkJoy(send.ID, "m1")
	done := make(chan sendStatus, 1)
	go func() {
		done <- tr.Wait(send.ID, 2*time.Second)
	}()
	time.Sleep(20 * time.Millisecond)
	tr.CompleteInkJoy("m1", true)
	if status := <-done; status != sendStatusDelivered {
		t.Fatalf("wait: got %q want delivered", status)
	}
}

func TestSendDeliveryInkJoyComplete(t *testing.T) {
	tr := NewSendDeliveryTracker()
	send := tr.Register("AABBCCDDEEFF", "img1")
	tr.BindInkJoy(send.ID, "msg-1")
	tr.CompleteInkJoy("msg-1", true)
	d := tr.Get(send.ID)
	if d == nil || d.Status != sendStatusDelivered {
		t.Fatalf("status: %+v", d)
	}
}

func TestSendDeliverySamsungComplete(t *testing.T) {
	tr := NewSendDeliveryTracker()
	send := tr.Register("samsung:mac", "img1")
	tr.BindSamsung(send.ID, "B0F2F657D5CD", "etag-abc")

	tr.CompleteSamsung("B0F2F657D5CD", "etag-abc")
	d := tr.Get(send.ID)
	if d == nil || d.Status != sendStatusDelivered {
		t.Fatalf("status: %+v", d)
	}
	tr.CompleteSamsung("B0F2F657D5CD", "etag-abc")
	d2 := tr.Get(send.ID)
	if d2 == nil || d2.Status != sendStatusDelivered {
		t.Fatal("idempotent complete should stay delivered")
	}
}

func TestHandleSendStatusWait(t *testing.T) {
	h := buildTestHub(t)
	send := h.sendDelivery.Register("dev", "img")
	h.sendDelivery.BindInkJoy(send.ID, "m1")

	go func() {
		time.Sleep(50 * time.Millisecond)
		h.sendDelivery.CompleteInkJoy("m1", true)
	}()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/send/"+send.ID+"?wait=5", nil)
	h.handleSendStatus(rec, req, send.ID)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if out["status"] != "delivered" {
		t.Fatalf("got %v", out)
	}
}
