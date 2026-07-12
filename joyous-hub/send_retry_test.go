package main

import (
	"bytes"
	"image"
	"image/png"
	"testing"
	"time"

	"joyous-hub/protocol"
)

type retryPublisher struct {
	publishes int
}

func (p *retryPublisher) Publish(topic string, payload []byte) error {
	p.publishes++
	return nil
}

func TestInkJoySendRetryOnLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping retry integration in short mode")
	}
	h := buildTestHub(t)
	pub := &retryPublisher{}
	h.publisher = pub
	h.inkjoyRetry = NewInkJoySendRetry(h)
	h.devices.MarkConnected("AABBCCDDEEFF")

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	imgID, err := h.images.Store(bytes.NewReader(buf.Bytes()), "test.png")
	if err != nil {
		t.Fatal(err)
	}

	send := h.sendDelivery.Register("AABBCCDDEEFF", imgID)
	h.inkjoyRetry.Track(send.ID, "AABBCCDDEEFF", imgID, "")

	h.inkjoyRetry.OnDeviceLogin("AABBCCDDEEFF")
	time.Sleep(50 * time.Millisecond)

	if pub.publishes < 1 {
		t.Fatalf("expected retry publish on login, got %d", pub.publishes)
	}
}

func TestInkJoySendRetryCompleteClears(t *testing.T) {
	h := buildTestHub(t)
	h.inkjoyRetry = NewInkJoySendRetry(h)

	send := h.sendDelivery.Register("AABBCCDDEEFF", "img1")
	h.sendDelivery.BindInkJoy(send.ID, "msg-1")
	h.inkjoyRetry.Track(send.ID, "AABBCCDDEEFF", "img1", "")

	h.inkjoyRetry.OnPlayAck("msg-1", inkjoyAckComplete)

	if h.inkjoyRetry.Attempts(send.ID) != 0 {
		t.Fatalf("expected retry cleared, attempts=%d", h.inkjoyRetry.Attempts(send.ID))
	}
	d := h.sendDelivery.Get(send.ID)
	if d == nil || d.Status != sendStatusDelivered {
		t.Fatalf("status: %+v", d)
	}
}

func TestInkJoySendRetryProgressResetsTimeout(t *testing.T) {
	h := buildTestHub(t)
	h.inkjoyRetry = NewInkJoySendRetry(h)

	send := h.sendDelivery.Register("AABBCCDDEEFF", "img1")
	h.sendDelivery.BindInkJoy(send.ID, "msg-1")
	h.inkjoyRetry.Track(send.ID, "AABBCCDDEEFF", "img1", "")

	h.inkjoyRetry.OnPlayAck("msg-1", inkjoyProgressFirst)
	if h.inkjoyRetry.Attempts(send.ID) != 1 {
		t.Fatalf("progress should not increment attempts, got %d", h.inkjoyRetry.Attempts(send.ID))
	}
}

func TestInkJoySendRetryBridgeBindNotifiesHub(t *testing.T) {
	h := buildTestHub(t)
	retry := NewInkJoySendRetry(h)
	var got []protocol.SendCompletePayload
	retry.SetSendCompleteNotifier(func(body protocol.SendCompletePayload) {
		got = append(got, body)
	})
	h.sendDelivery.BindInkJoy("bridge-send", "msg-1")
	retry.TrackFromBridge("bridge-send", "AABBCCDDEEFF", "img1", "", "http://hub")

	retry.OnPlayAck("msg-1", inkjoyProgressFirst)
	retry.OnPlayAck("msg-1", inkjoyAckComplete)

	if len(got) != 2 {
		t.Fatalf("notifications=%d want 2", len(got))
	}
	if got[0].Phase != "downloading" || got[1].Phase != "delivered" {
		t.Fatalf("phases: %+v", got)
	}
}
