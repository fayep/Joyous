package inkjoybridge

import (
	"log"
	"sync"
	"time"
)

// A cloud play that arrives while its frame is disconnected from the bridge
// would be published to nobody and lost (seen on the 28.5": it dropped off,
// the cloud's play came 12s later, and the frame never showed it). So the
// bridge holds the latest such play — already rewritten to the hub's cached
// copy — and delivers it once the frame logs back in. The play keeps the
// cloud's msgid, so the frame's play_ack/display_start/display_done still
// answer the cloud's play.

// heldPlayTTL is how long a held play stays deliverable.
var heldPlayTTL = 10 * time.Minute

// heldPlayDelay lets a reconnecting frame finish its login exchange before
// the held play is delivered.
var heldPlayDelay = 2 * time.Second

type heldPlay struct {
	topic   string
	payload []byte
	at      time.Time
}

type heldPlays struct {
	mu sync.Mutex
	m  map[string]heldPlay // mac → latest undelivered cloud play
}

// frameConnected reports whether the frame has a live session on the broker
// (frames connect with their MAC as client ID).
func (bs *bridgeSet) frameConnected(mac string) bool {
	if bs.broker == nil {
		return false
	}
	cl, ok := bs.broker.Clients.Get(mac)
	return ok && !cl.Closed()
}

// deliverCloudPlay publishes a cloud play to the frame now, or holds it for
// the frame's next login if the frame isn't connected.
func (bs *bridgeSet) deliverCloudPlay(mac, topic string, payload []byte) {
	if bs.frameConnected(mac) {
		bs.broker.Publish(topic, payload, false, 1)
		return
	}
	bs.held.mu.Lock()
	if bs.held.m == nil {
		bs.held.m = map[string]heldPlay{}
	}
	bs.held.m[mac] = heldPlay{topic: topic, payload: payload, at: time.Now()}
	bs.held.mu.Unlock()
	log.Printf("[%s] cloud play %s held: frame not connected", mac, MQTTMsgid(payload))
}

// releaseHeldPlay delivers a held cloud play shortly after the frame logs in.
func (bs *bridgeSet) releaseHeldPlay(mac string) {
	bs.held.mu.Lock()
	p, ok := bs.held.m[mac]
	if ok {
		delete(bs.held.m, mac)
	}
	bs.held.mu.Unlock()
	if !ok {
		return
	}
	if time.Since(p.at) > heldPlayTTL {
		log.Printf("[%s] held cloud play %s expired", mac, MQTTMsgid(p.payload))
		return
	}
	time.AfterFunc(heldPlayDelay, func() {
		log.Printf("[%s] delivering held cloud play %s", mac, MQTTMsgid(p.payload))
		if bs.mqttLog != nil {
			bs.mqttLog.AddLocal("bridge→frame", p.topic, p.payload, "held cloud play")
		}
		bs.broker.Publish(p.topic, p.payload, false, 1)
	})
}
