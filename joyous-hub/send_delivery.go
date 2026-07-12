package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const sendDeliveryTTL = 10 * time.Minute

type sendStatus string

const (
	sendStatusPending     sendStatus = "pending"
	sendStatusDownloading sendStatus = "downloading"
	sendStatusDelivered   sendStatus = "delivered"
	sendStatusFailed      sendStatus = "failed"
)

type sendDelivery struct {
	ID             string
	DeviceID       string
	ImageID        string
	Status         sendStatus
	Created        time.Time
	DeliveredAt    time.Time
	done           chan struct{}
	inkjoyMsgID    string
	samsungFrameID string
	samsungETag    string
}

// SendDeliveryTracker tracks hub→frame sends until the frame pulls content.
type SendDeliveryTracker struct {
	mu             sync.Mutex
	byID           map[string]*sendDelivery
	inkjoyByMsgID  map[string]string
	samsungByFrame map[string]string
}

func NewSendDeliveryTracker() *SendDeliveryTracker {
	return &SendDeliveryTracker{
		byID:           make(map[string]*sendDelivery),
		inkjoyByMsgID:  make(map[string]string),
		samsungByFrame: make(map[string]string),
	}
}

func newSendID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (t *SendDeliveryTracker) register(d *sendDelivery) *sendDelivery {
	t.mu.Lock()
	t.byID[d.ID] = d
	t.mu.Unlock()
	time.AfterFunc(sendDeliveryTTL, func() { t.remove(d.ID) })
	return d
}

func (t *SendDeliveryTracker) Register(deviceID, imageID string) *sendDelivery {
	d := &sendDelivery{
		ID:       newSendID(),
		DeviceID: deviceID,
		ImageID:  imageID,
		Status:   sendStatusPending,
		Created:  time.Now(),
		done:     make(chan struct{}),
	}
	return t.register(d)
}

func (t *SendDeliveryTracker) BindSamsung(sendID, frameID, etag string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.byID[sendID]
	if !ok || frameID == "" {
		return
	}
	d.samsungFrameID = frameID
	d.samsungETag = etag
	t.samsungByFrame[frameID] = sendID
}

func (t *SendDeliveryTracker) BindInkJoy(sendID, msgid string) {
	if sendID == "" || msgid == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if d, ok := t.byID[sendID]; ok {
		if d.inkjoyMsgID != "" && d.inkjoyMsgID != msgid {
			delete(t.inkjoyByMsgID, d.inkjoyMsgID)
		}
		d.inkjoyMsgID = msgid
	}
	// Bridge can bind a msgid for a sendID the hub hasn't (or no longer)
	// Register()'d — see TestBindInkJoyWithoutRegister. Track it anyway so
	// SendIDForInkJoyMsgid still resolves; CompleteInkJoy's own byID lookup
	// is what actually gates whether a completion has anywhere to land.
	t.inkjoyByMsgID[msgid] = sendID
}

// UnbindInkJoy removes the current msgid mapping for a pending send (before re-publish).
func (t *SendDeliveryTracker) UnbindInkJoy(sendID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.byID[sendID]
	if !ok {
		return
	}
	if d.inkjoyMsgID != "" {
		delete(t.inkjoyByMsgID, d.inkjoyMsgID)
		d.inkjoyMsgID = ""
	}
}

// SendIDForInkJoyMsgid returns the hub send id bound to a play msgid.
func (t *SendDeliveryTracker) SendIDForInkJoyMsgid(msgid string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.inkjoyByMsgID[msgid]
}

func (t *SendDeliveryTracker) Get(sendID string) *sendDelivery {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.byID[sendID]
	if !ok {
		return nil
	}
	cp := *d
	return &cp
}

func (t *SendDeliveryTracker) finish(d *sendDelivery, status sendStatus) bool {
	if d == nil {
		return false
	}
	switch status {
	case sendStatusDelivered, sendStatusFailed:
		if d.Status != sendStatusPending && d.Status != sendStatusDownloading {
			return false
		}
	default:
		if d.Status != sendStatusPending {
			return false
		}
	}
	d.Status = status
	if status == sendStatusDelivered {
		d.DeliveredAt = time.Now()
	}
	close(d.done)
	return true
}

func (t *SendDeliveryTracker) Fail(sendID string) {
	t.CompleteSend(sendID, false)
}

// CompleteSend marks a hub send as delivered or failed by send id.
func (t *SendDeliveryTracker) CompleteSend(sendID string, ok bool) {
	t.mu.Lock()
	d, found := t.byID[sendID]
	if !found {
		t.mu.Unlock()
		return
	}
	status := sendStatusFailed
	if ok {
		status = sendStatusDelivered
	}
	if d.inkjoyMsgID != "" {
		delete(t.inkjoyByMsgID, d.inkjoyMsgID)
	}
	if d.samsungFrameID != "" {
		delete(t.samsungByFrame, d.samsungFrameID)
	}
	t.finish(d, status)
	t.mu.Unlock()
}

func (t *SendDeliveryTracker) CompleteInkJoy(msgid string, ok bool) {
	t.mu.Lock()
	sendID, found := t.inkjoyByMsgID[msgid]
	if !found {
		t.mu.Unlock()
		return
	}
	d, ok2 := t.byID[sendID]
	if !ok2 {
		delete(t.inkjoyByMsgID, msgid)
		t.mu.Unlock()
		return
	}
	status := sendStatusFailed
	if ok {
		status = sendStatusDelivered
	}
	if t.finish(d, status) {
		delete(t.inkjoyByMsgID, msgid)
	}
	t.mu.Unlock()
}

func (t *SendDeliveryTracker) MarkInkJoyDownloading(sendID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.byID[sendID]
	if !ok || d.Status != sendStatusPending {
		return
	}
	d.Status = sendStatusDownloading
}

func (t *SendDeliveryTracker) MarkSamsungDownloading(frameID, etag string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sendID, found := t.samsungByFrame[frameID]
	if !found {
		return
	}
	d, ok := t.byID[sendID]
	if !ok || d.Status != sendStatusPending {
		return
	}
	if d.samsungETag != "" && etag != "" && d.samsungETag != etag {
		return
	}
	d.Status = sendStatusDownloading
}

func (t *SendDeliveryTracker) CompleteSamsung(frameID, etag string) {
	t.mu.Lock()
	sendID, found := t.samsungByFrame[frameID]
	if !found {
		t.mu.Unlock()
		return
	}
	d, ok := t.byID[sendID]
	if !ok || d.samsungETag != etag {
		t.mu.Unlock()
		return
	}
	if d.Status != sendStatusPending && d.Status != sendStatusDownloading {
		t.mu.Unlock()
		return
	}
	if t.finish(d, sendStatusDelivered) {
		delete(t.samsungByFrame, frameID)
		if d.inkjoyMsgID != "" {
			delete(t.inkjoyByMsgID, d.inkjoyMsgID)
		}
	}
	t.mu.Unlock()
}

func (t *SendDeliveryTracker) Wait(sendID string, timeout time.Duration) sendStatus {
	t.mu.Lock()
	d, ok := t.byID[sendID]
	if !ok {
		t.mu.Unlock()
		return ""
	}
	done := d.done
	t.mu.Unlock()
	if timeout <= 0 {
		timeout = inkjoySendAckTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if d2, ok := t.byID[sendID]; ok {
		return d2.Status
	}
	return sendStatusFailed
}

func (t *SendDeliveryTracker) remove(sendID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.byID[sendID]
	if !ok {
		return
	}
	if d.inkjoyMsgID != "" {
		delete(t.inkjoyByMsgID, d.inkjoyMsgID)
	}
	if d.samsungFrameID != "" {
		delete(t.samsungByFrame, d.samsungFrameID)
	}
	delete(t.byID, sendID)
}

func (h *Hub) handleSendStatus(w http.ResponseWriter, r *http.Request, sendID string) {
	if sendID == "" {
		http.Error(w, "send id required", http.StatusBadRequest)
		return
	}
	if h.sendDelivery == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	wait := 0
	if s := r.URL.Query().Get("wait"); s != "" {
		wait, _ = strconv.Atoi(s)
	}
	if wait > 120 {
		wait = 120
	}
	d := h.sendDelivery.Get(sendID)
	if d == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if wait > 0 && (d.Status == sendStatusPending || d.Status == sendStatusDownloading) {
		timer := time.NewTimer(time.Duration(wait) * time.Second)
		defer timer.Stop()
		fresh := h.sendDelivery.Get(sendID)
		if fresh != nil {
			select {
			case <-fresh.done:
			case <-timer.C:
			case <-r.Context().Done():
			}
		}
		d = h.sendDelivery.Get(sendID)
		if d == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
	}
	out := map[string]any{
		"send_id":   d.ID,
		"status":    string(d.Status),
		"device_id": d.DeviceID,
	}
	if d.ImageID != "" {
		out["image_id"] = d.ImageID
	}
	if !d.DeliveredAt.IsZero() {
		out["delivered_at"] = d.DeliveredAt
	}
	if h.inkjoyRetry != nil {
		if n := h.inkjoyRetry.Attempts(sendID); n > 1 {
			out["retry_attempts"] = n - 1
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
