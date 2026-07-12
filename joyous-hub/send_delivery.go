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
	// sendStatusRetrying is a display-only overlay (see handleSendStatus), not a state
	// sendDelivery.Status itself ever transitions to — it's derived from
	// pending/downloading + RetryAttempts>0 so Wait()/finish()'s pending/downloading
	// checks don't need to special-case it.
	sendStatusRetrying sendStatus = "retrying"
)

type sendDelivery struct {
	ID             string
	DeviceID       string
	ImageID        string
	Status         sendStatus
	Created        time.Time
	DeliveredAt    time.Time
	RetryAttempts  int    // bumped by IncrementRetry; any bridge can report progress this way
	LastError      string // most recent retry/failure detail (SendCompletePayload.Detail)
	SessionID      string // owning browser session (event_bus.go); BroadcastSessionID if none
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

// Register starts tracking a send with no owning session (BroadcastSessionID) — used by
// system-triggered sends (scheduled sends) and anywhere a caller doesn't have a browser session
// to attribute the send to. Its "send" events go to every connected session rather than just
// one, which is exactly the behavior wanted for a send nobody in particular asked for.
func (t *SendDeliveryTracker) Register(deviceID, imageID string) *sendDelivery {
	return t.RegisterWithSession(deviceID, imageID, BroadcastSessionID)
}

// RegisterWithSession is Register, attributing the send to sessionID so its "send" events are
// unicast to just that browser session instead of broadcast to everyone.
func (t *SendDeliveryTracker) RegisterWithSession(deviceID, imageID, sessionID string) *sendDelivery {
	d := &sendDelivery{
		ID:        newSendID(),
		DeviceID:  deviceID,
		ImageID:   imageID,
		Status:    sendStatusPending,
		Created:   time.Now(),
		SessionID: sessionID,
		done:      make(chan struct{}),
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
	t.CompleteSendDetailed(sendID, ok, "")
}

// CompleteSendDetailed is CompleteSend plus a failure reason (SendCompletePayload.Detail),
// recorded so GET /api/send/{sendId} can report why a send failed instead of just that it did.
func (t *SendDeliveryTracker) CompleteSendDetailed(sendID string, ok bool, detail string) {
	t.mu.Lock()
	d, found := t.byID[sendID]
	if !found {
		t.mu.Unlock()
		return
	}
	status := sendStatusFailed
	if ok {
		status = sendStatusDelivered
	} else if detail != "" {
		d.LastError = detail
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

// IncrementRetry records another delivery attempt for a still-in-flight send (e.g. a Samsung
// frame that's asleep and needs a physical wake, or an InkJoy play republish after a missed
// ack). GET /api/send/{sendId} reports status "retrying" once this is above zero, instead of
// each caller inventing its own client-side guess for how long to wait before hinting at that.
func (t *SendDeliveryTracker) IncrementRetry(sendID, detail string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.byID[sendID]
	if !ok || (d.Status != sendStatusPending && d.Status != sendStatusDownloading) {
		return
	}
	d.RetryAttempts++
	if detail != "" {
		d.LastError = detail
	}
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

// ActiveSendsFor returns full status payloads (see sendStatusPayload) for every in-flight send
// visible to sessionID — its own unicast sends plus any broadcast-owned (system-triggered, e.g.
// scheduled send) ones — so a browser tab that just (re)connected to the event stream is caught
// up on anything already in progress, not just events that happen to fire after it subscribes.
func (t *SendDeliveryTracker) ActiveSendsFor(sessionID string) []map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []map[string]any
	for _, d := range t.byID {
		if d.Status != sendStatusPending && d.Status != sendStatusDownloading {
			continue
		}
		if d.SessionID != sessionID && d.SessionID != BroadcastSessionID {
			continue
		}
		out = append(out, sendStatusPayload(d))
	}
	return out
}

// ActiveSendInfo is a minimal, JSON-safe view of an in-flight send.
type ActiveSendInfo struct {
	SendID   string `json:"send_id"`
	DeviceID string `json:"device_id"`
	ImageID  string `json:"image_id,omitempty"`
}

// ActiveSends returns every send that hasn't reached a terminal state yet (pending or
// downloading — "retrying" is a display-only overlay on these, see handleSendStatus). This is
// how a browser tab that didn't itself trigger a send — e.g. a scheduled send firing in the
// background — discovers there's something to watch and show progress for.
func (t *SendDeliveryTracker) ActiveSends() []ActiveSendInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]ActiveSendInfo, 0, len(t.byID))
	for _, d := range t.byID {
		if d.Status == sendStatusPending || d.Status == sendStatusDownloading {
			out = append(out, ActiveSendInfo{SendID: d.ID, DeviceID: d.DeviceID, ImageID: d.ImageID})
		}
	}
	return out
}

// handleActiveSends serves GET /api/sends/active.
func (h *Hub) handleActiveSends(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.sendDelivery == nil {
		json.NewEncoder(w).Encode([]map[string]any{})
		return
	}
	active := h.sendDelivery.ActiveSends()
	out := make([]map[string]any, 0, len(active))
	for _, a := range active {
		entry := map[string]any{"send_id": a.SendID, "device_id": a.DeviceID}
		if a.ImageID != "" {
			entry["image_id"] = a.ImageID
		}
		if dev, ok := h.devices.Get(a.DeviceID); ok {
			entry["device_type"] = string(dev.Type)
		}
		out = append(out, entry)
	}
	json.NewEncoder(w).Encode(out)
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sendStatusPayload(d))
}

// sendStatusPayload builds the JSON-safe status snapshot for a send — shared by
// handleSendStatus (GET /api/send/{sendId}) and the event bus, so a live "send" SSE event has
// exactly the same shape a client would get from polling.
func sendStatusPayload(d *sendDelivery) map[string]any {
	status := d.Status
	if d.RetryAttempts > 0 && (status == sendStatusPending || status == sendStatusDownloading) {
		status = sendStatusRetrying
	}
	out := map[string]any{
		"send_id":   d.ID,
		"status":    string(status),
		"device_id": d.DeviceID,
	}
	if d.ImageID != "" {
		out["image_id"] = d.ImageID
	}
	if !d.DeliveredAt.IsZero() {
		out["delivered_at"] = d.DeliveredAt
	}
	if d.RetryAttempts > 0 {
		out["retry_attempts"] = d.RetryAttempts
	}
	if status == sendStatusRetrying && d.LastError != "" {
		out["detail"] = d.LastError
	}
	if d.Status == sendStatusFailed && d.LastError != "" {
		out["error"] = d.LastError
	}
	return out
}
