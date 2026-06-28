package main

import (
	"log"
	"sync"
	"time"
)

const (
	inkjoySendAckTimeout   = 90 * time.Second
	inkjoySendRetryDelay   = 45 * time.Second
	inkjoySendMaxAttempts  = 12
	inkjoySendMaxAge       = 30 * time.Minute
)

// InkJoySendRetry re-publishes play commands when a frame is offline or never acks.
type InkJoySendRetry struct {
	hub *Hub
	mu  sync.Mutex
	// sendID → entry
	pending map[string]*inkjoyRetryEntry
	// deviceID → sendID (latest pending send per frame)
	byDevice map[string]string
}

type inkjoyRetryEntry struct {
	sendID       string
	deviceID     string
	imageID      string
	overlayToken string
	attempts     int
	created      time.Time
	lastAttempt  time.Time
	ackTimer     *time.Timer
}

func NewInkJoySendRetry(hub *Hub) *InkJoySendRetry {
	return &InkJoySendRetry{
		hub:      hub,
		pending:  make(map[string]*inkjoyRetryEntry),
		byDevice: make(map[string]string),
	}
}

// Track watches an InkJoy send until play_ack completes or retries are exhausted.
func (r *InkJoySendRetry) Track(sendID, deviceID, imageID, overlayToken string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if oldSendID, ok := r.byDevice[deviceID]; ok && oldSendID != sendID {
		r.removeLocked(oldSendID)
	}
	entry := &inkjoyRetryEntry{
		sendID:       sendID,
		deviceID:     deviceID,
		imageID:      imageID,
		overlayToken: overlayToken,
		attempts:     1,
		created:      time.Now(),
		lastAttempt:  time.Now(),
	}
	r.pending[sendID] = entry
	r.byDevice[deviceID] = sendID
	r.scheduleAckTimeoutLocked(entry)
}

func (r *InkJoySendRetry) Clear(sendID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeLocked(sendID)
}

func (r *InkJoySendRetry) Attempts(sendID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.pending[sendID]; ok {
		return e.attempts
	}
	return 0
}

func (r *InkJoySendRetry) OnDeviceLogin(deviceID string) {
	r.triggerRetry(deviceID, true)
}

func (r *InkJoySendRetry) OnDeviceHeart(deviceID string) {
	r.triggerRetry(deviceID, false)
}

func (r *InkJoySendRetry) OnPlayAck(msgid string, result int) {
	if r.hub == nil || r.hub.sendDelivery == nil {
		return
	}
	sendID := r.hub.sendDelivery.SendIDForInkJoyMsgid(msgid)
	if sendID == "" {
		return
	}
	switch result {
	case inkjoyAckComplete:
		r.Clear(sendID)
		r.hub.sendDelivery.CompleteInkJoy(msgid, true)
	case inkjoyAckInterrupted:
		r.onFailure(sendID, msgid)
	default:
		if inkjoyIsProgressResult(result) {
			r.resetAckTimeout(sendID)
		}
	}
}

func (r *InkJoySendRetry) triggerRetry(deviceID string, immediate bool) {
	r.mu.Lock()
	sendID, ok := r.byDevice[deviceID]
	if !ok {
		r.mu.Unlock()
		return
	}
	entry, ok := r.pending[sendID]
	if !ok {
		r.mu.Unlock()
		return
	}
	if !immediate && time.Since(entry.lastAttempt) < inkjoySendRetryDelay {
		r.mu.Unlock()
		return
	}
	d := r.hub.sendDelivery.Get(sendID)
	if d == nil || d.Status != sendStatusPending {
		r.removeLocked(sendID)
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	r.doRetry(entry)
}

func (r *InkJoySendRetry) onFailure(sendID, msgid string) {
	r.mu.Lock()
	entry, ok := r.pending[sendID]
	if !ok {
		r.mu.Unlock()
		if r.hub.sendDelivery != nil {
			r.hub.sendDelivery.CompleteInkJoy(msgid, false)
		}
		return
	}
	if entry.ackTimer != nil {
		entry.ackTimer.Stop()
		entry.ackTimer = nil
	}
	exhausted := entry.attempts >= inkjoySendMaxAttempts || time.Since(entry.created) > inkjoySendMaxAge
	if exhausted {
		delete(r.pending, sendID)
		delete(r.byDevice, entry.deviceID)
		r.mu.Unlock()
		if r.hub.sendDelivery != nil {
			r.hub.sendDelivery.CompleteInkJoy(msgid, false)
		}
		return
	}
	deviceID := entry.deviceID
	imageID := entry.imageID
	attempts := entry.attempts
	r.mu.Unlock()
	log.Printf("inkjoy send interrupted device=%s image=%s attempt=%d — will retry", deviceID, imageID, attempts)
	time.AfterFunc(inkjoySendRetryDelay, func() { r.retryBySendID(sendID) })
}

func (r *InkJoySendRetry) retryBySendID(sendID string) {
	r.mu.Lock()
	entry, ok := r.pending[sendID]
	if !ok {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	r.doRetry(entry)
}

func (r *InkJoySendRetry) onAckTimeout(sendID string) {
	r.mu.Lock()
	entry, ok := r.pending[sendID]
	if !ok {
		r.mu.Unlock()
		return
	}
	if r.hub.sendDelivery == nil {
		r.mu.Unlock()
		return
	}
	d := r.hub.sendDelivery.Get(sendID)
	if d == nil || d.Status != sendStatusPending {
		r.removeLocked(sendID)
		r.mu.Unlock()
		return
	}
	if entry.attempts >= inkjoySendMaxAttempts || time.Since(entry.created) > inkjoySendMaxAge {
		r.removeLocked(sendID)
		r.mu.Unlock()
		r.hub.sendDelivery.Fail(sendID)
		log.Printf("inkjoy send gave up device=%s image=%s after %d attempts", entry.deviceID, entry.imageID, entry.attempts)
		return
	}
	r.mu.Unlock()
	log.Printf("inkjoy send no play_ack device=%s image=%s attempt=%d — retrying", entry.deviceID, entry.imageID, entry.attempts)
	r.doRetry(entry)
}

func (r *InkJoySendRetry) doRetry(entry *inkjoyRetryEntry) {
	if r.hub == nil {
		return
	}
	dev, ok := r.hub.devices.Get(entry.deviceID)
	if !ok {
		r.Clear(entry.sendID)
		return
	}
	if r.hub.sendDelivery != nil {
		d := r.hub.sendDelivery.Get(entry.sendID)
		if d == nil || d.Status != sendStatusPending {
			r.Clear(entry.sendID)
			return
		}
	}
	err := r.hub.sendInkJoyImage(dev, entry.imageID, entry.overlayToken, entry.sendID)
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.pending[entry.sendID]
	if !ok || cur != entry {
		return
	}
	entry.attempts++
	entry.lastAttempt = time.Now()
	if err != nil {
		log.Printf("inkjoy send retry publish failed device=%s: %v", entry.deviceID, err)
	}
	r.scheduleAckTimeoutLocked(entry)
}

func (r *InkJoySendRetry) resetAckTimeout(sendID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.pending[sendID]; ok {
		r.scheduleAckTimeoutLocked(entry)
	}
}

func (r *InkJoySendRetry) scheduleAckTimeoutLocked(entry *inkjoyRetryEntry) {
	if entry.ackTimer != nil {
		entry.ackTimer.Stop()
	}
	sendID := entry.sendID
	entry.ackTimer = time.AfterFunc(inkjoySendAckTimeout, func() {
		r.onAckTimeout(sendID)
	})
}

func (r *InkJoySendRetry) removeLocked(sendID string) {
	entry, ok := r.pending[sendID]
	if !ok {
		return
	}
	if entry.ackTimer != nil {
		entry.ackTimer.Stop()
	}
	delete(r.pending, sendID)
	if r.byDevice[entry.deviceID] == sendID {
		delete(r.byDevice, entry.deviceID)
	}
}
