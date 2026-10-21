package main

import (
	"testing"

	"joyous-hub/protocol"
)

func TestInkJoySendRetryOnLogin(t *testing.T) {
	h := buildTestHub(t)
	retry := NewInkJoySendRetry(h)
	h.inkjoyRetry = retry
	var resent int
	retry.SetResender(func(entry *inkjoyRetryEntry) error {
		resent++
		return nil
	})

	send := h.sendDelivery.Register("AABBCCDDEEFF", "img1")
	retry.TrackFromBridge(send.ID, "AABBCCDDEEFF", "img1", "", "http://hub", nil)

	retry.OnDeviceLogin("AABBCCDDEEFF")
	if resent < 1 {
		t.Fatalf("expected retry publish on login, got %d", resent)
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

func TestInkJoySendRetryInterruptUnbindsMsgid(t *testing.T) {
	h := buildTestHub(t)
	h.inkjoyRetry = NewInkJoySendRetry(h)

	send := h.sendDelivery.Register("AABBCCDDEEFF", "img1")
	h.sendDelivery.BindInkJoy(send.ID, "msg-1")
	h.inkjoyRetry.Track(send.ID, "AABBCCDDEEFF", "img1", "")

	h.inkjoyRetry.OnPlayAck("msg-1", inkjoyAckInterrupted)

	if got := h.sendDelivery.InkJoyMsgid(send.ID); got != "" {
		t.Fatalf("interrupt should unbind msgid, got %q", got)
	}
	if h.inkjoyRetry.Attempts(send.ID) == 0 {
		t.Fatal("expected retry still pending after interrupt")
	}
}

func TestInkJoySendRetryAckTimeoutUnbindsMsgid(t *testing.T) {
	h := buildTestHub(t)
	retry := NewInkJoySendRetry(h)
	h.inkjoyRetry = retry
	var resent bool
	retry.SetResender(func(entry *inkjoyRetryEntry) error {
		resent = true
		return nil
	})

	send := h.sendDelivery.Register("AABBCCDDEEFF", "img1")
	h.sendDelivery.BindInkJoy(send.ID, "msg-1")
	retry.TrackFromBridge(send.ID, "AABBCCDDEEFF", "img1", "", "http://hub", nil)

	retry.onAckTimeout(send.ID)

	if got := h.sendDelivery.InkJoyMsgid(send.ID); got != "" {
		t.Fatalf("timeout should unbind msgid for a fresh play, got %q", got)
	}
	if !resent {
		t.Fatal("expected resend on ack timeout")
	}
}

func TestInkJoyDeviceAdapterLoginTriggersRetry(t *testing.T) {
	h := buildTestHub(t)
	retry := NewInkJoySendRetry(h)
	h.inkjoyRetry = retry
	var resent bool
	retry.SetResender(func(entry *inkjoyRetryEntry) error {
		resent = true
		return nil
	})
	adapter := &inkjoyDeviceAdapter{reg: h.devices, retry: retry}

	send := h.sendDelivery.Register("AABBCCDDEEFF", "img1")
	retry.TrackFromBridge(send.ID, "AABBCCDDEEFF", "img1", "", "http://hub", nil)

	adapter.OnDeviceLogin("AABBCCDDEEFF")
	if !resent {
		t.Fatal("login should requeue pending play")
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
	retry.TrackFromBridge("bridge-send", "AABBCCDDEEFF", "img1", "", "http://hub", nil)

	retry.OnPlayAck("msg-1", inkjoyProgressFirst)
	retry.OnPlayAck("msg-1", inkjoyAckComplete)

	if len(got) != 2 {
		t.Fatalf("notifications=%d want 2", len(got))
	}
	if got[0].Phase != "downloading" || got[1].Phase != "delivered" {
		t.Fatalf("phases: %+v", got)
	}
}
