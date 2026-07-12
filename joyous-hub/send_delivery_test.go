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

func TestBindInkJoyWithoutRegister(t *testing.T) {
	tr := NewSendDeliveryTracker()
	tr.BindInkJoy("hub-send-id", "msg-bridge")
	if got := tr.SendIDForInkJoyMsgid("msg-bridge"); got != "hub-send-id" {
		t.Fatalf("msgid map: got %q want hub-send-id", got)
	}
}

func TestMarkInkJoyDownloading(t *testing.T) {
	tr := NewSendDeliveryTracker()
	send := tr.Register("dev", "img")
	tr.MarkInkJoyDownloading(send.ID)
	d := tr.Get(send.ID)
	if d == nil || d.Status != sendStatusDownloading {
		t.Fatalf("downloading: %+v", d)
	}
}

func TestSendDeliverySamsungDownloading(t *testing.T) {
	tr := NewSendDeliveryTracker()
	send := tr.Register("samsung:mac", "img1")
	tr.BindSamsung(send.ID, "B0F2F657D5CD", "etag-abc")

	tr.MarkSamsungDownloading("B0F2F657D5CD", "etag-abc")
	d := tr.Get(send.ID)
	if d == nil || d.Status != sendStatusDownloading {
		t.Fatalf("downloading: %+v", d)
	}

	tr.CompleteSamsung("B0F2F657D5CD", "etag-abc")
	d = tr.Get(send.ID)
	if d == nil || d.Status != sendStatusDelivered {
		t.Fatalf("delivered: %+v", d)
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

func TestIncrementRetryReportsAsRetryingStatus(t *testing.T) {
	h := buildTestHub(t)
	send := h.sendDelivery.Register("dev", "img")

	h.sendDelivery.IncrementRetry(send.ID, "frame asleep")
	h.sendDelivery.IncrementRetry(send.ID, "frame asleep")

	rec := httptest.NewRecorder()
	h.handleSendStatus(rec, httptest.NewRequest("GET", "/api/send/"+send.ID, nil), send.ID)
	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if out["status"] != "retrying" {
		t.Fatalf("got status=%v, want retrying: %v", out["status"], out)
	}
	if n, _ := out["retry_attempts"].(float64); n != 2 {
		t.Fatalf("got retry_attempts=%v, want 2: %v", out["retry_attempts"], out)
	}
}

func TestIncrementRetryIgnoredAfterCompletion(t *testing.T) {
	tr := NewSendDeliveryTracker()
	send := tr.Register("dev", "img")
	tr.CompleteSend(send.ID, true)

	tr.IncrementRetry(send.ID, "too late")

	d := tr.Get(send.ID)
	if d == nil || d.RetryAttempts != 0 {
		t.Fatalf("expected no retry recorded on an already-completed send: %+v", d)
	}
}

func TestRetryClearedByFinalCompletion(t *testing.T) {
	h := buildTestHub(t)
	send := h.sendDelivery.Register("dev", "img")
	h.sendDelivery.IncrementRetry(send.ID, "frame asleep")
	h.sendDelivery.CompleteSend(send.ID, true)

	rec := httptest.NewRecorder()
	h.handleSendStatus(rec, httptest.NewRequest("GET", "/api/send/"+send.ID, nil), send.ID)
	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if out["status"] != "delivered" {
		t.Fatalf("got status=%v, want delivered (retrying must not outlive completion): %v", out["status"], out)
	}
}

func TestCompleteSendDetailedSurfacesErrorOnFailure(t *testing.T) {
	h := buildTestHub(t)
	send := h.sendDelivery.Register("dev", "img")
	h.sendDelivery.CompleteSendDetailed(send.ID, false, "gave up after 12 attempts")

	rec := httptest.NewRecorder()
	h.handleSendStatus(rec, httptest.NewRequest("GET", "/api/send/"+send.ID, nil), send.ID)
	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if out["status"] != "failed" {
		t.Fatalf("got status=%v, want failed: %v", out["status"], out)
	}
	if out["error"] != "gave up after 12 attempts" {
		t.Fatalf("got error=%v, want the failure detail surfaced: %v", out["error"], out)
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
