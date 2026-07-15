package main

import (
	"encoding/json"
	"log"
	"maps"
	"net/http"
	"sync"
	"time"
)

// sessionIDHeader carries the browser tab's session id on send-triggering POSTs (fetch
// supports custom headers). The SSE connection itself (browser EventSource, which cannot set
// custom headers) instead passes it as a "session" query parameter — requestSessionID checks
// both so either style of caller resolves the same way.
const sessionIDHeader = "X-Session-Id"

// requestSessionID extracts the calling browser session's id, or BroadcastSessionID if none
// was supplied (e.g. a request from something other than the SPA itself).
func requestSessionID(r *http.Request) string {
	if id := r.Header.Get(sessionIDHeader); id != "" {
		return id
	}
	return r.URL.Query().Get("session")
}

// BroadcastSessionID is the reserved sentinel meaning "every connected session" — used for
// events that aren't owned by whoever triggered them: device/bridge state changes, general
// errors, and system-triggered sends (e.g. a scheduled send) that no browser tab initiated.
const BroadcastSessionID = ""

// eventSubscriberBufferSize is deliberately small. There is no event replay or backlog in this
// bus — a session that isn't connected, or isn't keeping up, simply misses events rather than
// blocking a publisher (which usually runs on an MQTT-handling goroutine) or forcing history
// bookkeeping. Every event published here is a fresh snapshot of current state, not a delta,
// so a session that reconnects is caught up by the next snapshot rather than needing replay.
const eventSubscriberBufferSize = 8

// busEvent is the wire shape written as SSE "data:" payloads.
type busEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// EventBus fans out hub state changes to connected SSE sessions, unicast or broadcast.
type EventBus struct {
	mu   sync.Mutex
	subs map[string]chan []byte // sessionID -> subscriber channel
}

func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[string]chan []byte)}
}

// Subscribe registers sessionID (must be non-empty and not BroadcastSessionID) and returns its
// channel of already-marshaled event payloads, plus an unsubscribe func the caller must run
// exactly once (typically deferred) when the connection ends.
func (b *EventBus) Subscribe(sessionID string) (<-chan []byte, func()) {
	ch := make(chan []byte, eventSubscriberBufferSize)
	b.mu.Lock()
	b.subs[sessionID] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		// Only remove if this is still the channel we registered — a reconnect can replace
		// the map entry for the same sessionID before the old connection's cleanup runs.
		if b.subs[sessionID] == ch {
			delete(b.subs, sessionID)
		}
		b.mu.Unlock()
		close(ch)
	}
}

// Publish sends an event to sessionID, or to every connected session if sessionID is
// BroadcastSessionID. Delivery is non-blocking and best-effort: publishing to a sessionID with
// no connected subscriber, or to a subscriber whose buffer is full, is a silent no-op.
func (b *EventBus) Publish(sessionID, eventType string, data any) {
	payload, err := json.Marshal(busEvent{Type: eventType, Data: data})
	if err != nil {
		log.Printf("eventbus: marshal %s: %v", eventType, err)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if sessionID == BroadcastSessionID {
		for id, ch := range b.subs {
			trySendEvent(id, ch, payload)
		}
		return
	}
	if ch, ok := b.subs[sessionID]; ok {
		trySendEvent(sessionID, ch, payload)
	}
}

func trySendEvent(sessionID string, ch chan []byte, payload []byte) {
	select {
	case ch <- payload:
	default:
		log.Printf("eventbus: dropping event for session=%s (not keeping up)", sessionID)
	}
}

// publishSendEvent looks up sendID's current status and publishes it as a "send" event — to
// just its owning session, or to everyone if it's a system-triggered (broadcast) send. This is
// the one place a send's live status reaches the event bus, called after every state change in
// main.go's SetSendCompleteHandler (the shared chokepoint both InkJoy and Samsung report
// through) and right after a send is registered, so the handshake-snapshot case and the
// live-update case use identical logic.
func (h *Hub) publishSendEvent(sendID string) {
	if h.events == nil || h.sendDelivery == nil {
		return
	}
	d := h.sendDelivery.Get(sendID)
	if d == nil {
		return
	}
	h.events.Publish(d.SessionID, "send", h.enrichSendPayload(sendStatusPayload(d), d.DeviceID))
}

// enrichSendPayload adds device_type to a send status payload when resolvable, so a session
// that receives a send event it didn't itself request (a broadcast-owned system send, or its
// own handshake snapshot) can still show the right hint (e.g. Samsung's power-button wake)
// without depending on its own devices list being populated.
func (h *Hub) enrichSendPayload(payload map[string]any, deviceID string) map[string]any {
	if dev, ok := h.devices.Get(deviceID); ok {
		payload["device_type"] = string(dev.Type)
	}
	return payload
}

// publishError broadcasts a general failure that isn't tied to a specific in-flight send a
// client is already watching — e.g. a scheduled send that couldn't even start because its
// album is empty. (A failure *during* a tracked send instead surfaces through publishSendEvent
// as that send's own "error" field.) Always broadcast: whoever's connected should hear about
// it, since there's no single owning session for something the system triggered on its own.
func (h *Hub) publishError(message string, context map[string]any) {
	if h.events == nil {
		return
	}
	data := map[string]any{"message": message}
	maps.Copy(data, context)
	h.events.Publish(BroadcastSessionID, "error", data)
}

// imagesEventPayload is the "images" event body. Updated carries the full current ImageMeta
// for each created/edited image (upload, tag/name/chroma patch, crop change) — enough for a
// client to re-check the image against whatever album filter it's currently viewing and
// patch/insert/remove it accordingly, without a separate fetch. Removed is deleted image ids.
// ReorderedAlbums flags album ids whose member order changed (position isn't a field on
// ImageMeta itself — it's implicit in an album's stored order — so there's nothing to diff per
// image; a client viewing one of these albums should just re-fetch that album's image list).
type imagesEventPayload struct {
	Updated         []ImageMeta `json:"updated,omitempty"`
	Removed         []string    `json:"removed,omitempty"`
	ReorderedAlbums []string    `json:"reordered_albums,omitempty"`
}

// publishImagesUpdated broadcasts the current metadata for one or more created/edited images —
// every connected session gets it (not unicast: any open tab's Album view might be showing the
// image), and filters it against whatever it's currently displaying. See imagesEventPayload.
func (h *Hub) publishImagesUpdated(metas ...ImageMeta) {
	if h.events == nil || len(metas) == 0 {
		return
	}
	h.events.Publish(BroadcastSessionID, "images", imagesEventPayload{Updated: metas})
}

// publishImagesRemoved broadcasts that one or more images were deleted.
func (h *Hub) publishImagesRemoved(ids ...string) {
	if h.events == nil || len(ids) == 0 {
		return
	}
	h.events.Publish(BroadcastSessionID, "images", imagesEventPayload{Removed: ids})
}

// publishAlbumReordered broadcasts that albumID's member order changed (see
// imagesEventPayload.ReorderedAlbums).
func (h *Hub) publishAlbumReordered(albumID string) {
	if h.events == nil || albumID == "" {
		return
	}
	h.events.Publish(BroadcastSessionID, "images", imagesEventPayload{ReorderedAlbums: []string{albumID}})
}

const eventsKeepaliveInterval = 25 * time.Second

// handleEvents serves GET /api/events?session=<id> — a single SSE connection replacing what
// used to be several independently-polled endpoints (devices, bridges, active sends, UI
// revision). See requestSessionID for how the session id is supplied.
func (h *Hub) handleEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := requestSessionID(r)
	if sessionID == "" {
		http.Error(w, "session id required (?session=...)", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if h.events == nil {
		http.Error(w, "events unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // in case this ever sits behind a buffering proxy
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsubscribe := h.events.Subscribe(sessionID)
	defer unsubscribe()

	if !h.writeSSE(w, flusher, []byte(mustMarshalEvent("devices", h.devicesSnapshotForUI()))) {
		return
	}
	if h.bridgeCoord != nil {
		if !h.writeSSE(w, flusher, []byte(mustMarshalEvent("bridges", h.bridgeCoord.ListBridgeStatus()))) {
			return
		}
	}
	if !h.writeSSE(w, flusher, []byte(mustMarshalEvent("revision", map[string]string{"revision": uiRevision}))) {
		return
	}
	if h.sendDelivery != nil {
		for _, status := range h.sendDelivery.ActiveSendsFor(sessionID) {
			if deviceID, ok := status["device_id"].(string); ok {
				status = h.enrichSendPayload(status, deviceID)
			}
			if !h.writeSSE(w, flusher, []byte(mustMarshalEvent("send", status))) {
				return
			}
		}
	}

	keepalive := time.NewTicker(eventsKeepaliveInterval)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case payload, ok := <-ch:
			if !ok {
				return
			}
			if !h.writeSSE(w, flusher, payload) {
				return
			}
		case <-keepalive.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *Hub) writeSSE(w http.ResponseWriter, flusher http.Flusher, payload []byte) bool {
	if _, err := w.Write([]byte("data: ")); err != nil {
		return false
	}
	if _, err := w.Write(payload); err != nil {
		return false
	}
	if _, err := w.Write([]byte("\n\n")); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// mustMarshalEvent builds the same {type, data} wire shape as EventBus.Publish, for the
// handshake snapshot written directly to a fresh connection (not going through the bus, since
// it's addressed to exactly one not-yet-fully-subscribed connection).
func mustMarshalEvent(eventType string, data any) string {
	payload, err := json.Marshal(busEvent{Type: eventType, Data: data})
	if err != nil {
		log.Printf("eventbus: marshal snapshot %s: %v", eventType, err)
		return `{"type":"` + eventType + `","data":null}`
	}
	return string(payload)
}
