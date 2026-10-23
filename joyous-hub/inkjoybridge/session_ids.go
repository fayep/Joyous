package inkjoybridge

import (
	"encoding/json"
	"log"
	"sync"
	"time"
)

// A frame persists the device_id/uid from whichever login_ack it acts on and
// reports them back (pubkey_report). Two acks carrying different identities —
// the bridge's synthetic local ack and the cloud's real one — make a
// cloud-relayed frame flip between them; the 10" was seen restarting right
// after each such pair. So the bridge's own login_ack must carry the IDs the
// cloud issued, and is only synthesized when the cloud has issued none.

// SessionIDStore optionally persists each frame's cloud-issued session IDs
// across bridge restarts. Config.Devices may implement it.
type SessionIDStore interface {
	CloudSessionIDs(mac string) (deviceID, uid string, ok bool)
	SetCloudSessionIDs(mac, deviceID, uid string)
}

// loginAckGrace is how long a cloud-relayed frame's login waits for the
// cloud's own login_ack before the bridge answers with a synthetic one (the
// cloud doesn't answer some frames at all, e.g. the 28.5").
var loginAckGrace = 3 * time.Second

type sessionIDs struct{ deviceID, uid string }

type loginAcks struct {
	mu      sync.Mutex
	known   map[string]sessionIDs  // mac → cloud-issued IDs
	pending map[string]*time.Timer // login msgid → synthetic-ack fallback
}

func (bs *bridgeSet) sessionStore() SessionIDStore {
	s, _ := bs.devices.(SessionIDStore)
	return s
}

// cloudSessionIDs returns the IDs the cloud last issued this frame, if any.
func (bs *bridgeSet) cloudSessionIDs(mac string) (sessionIDs, bool) {
	bs.acks.mu.Lock()
	ids, ok := bs.acks.known[mac]
	bs.acks.mu.Unlock()
	if ok {
		return ids, true
	}
	if st := bs.sessionStore(); st != nil {
		if d, u, ok := st.CloudSessionIDs(mac); ok && d != "" && u != "" {
			ids = sessionIDs{d, u}
			bs.acks.mu.Lock()
			if bs.acks.known == nil {
				bs.acks.known = map[string]sessionIDs{}
			}
			bs.acks.known[mac] = ids
			bs.acks.mu.Unlock()
			return ids, true
		}
	}
	return sessionIDs{}, false
}

// ackLogin answers a frame's login locally. Local-only frames get the
// synthetic IDs at once. A cloud-relayed frame gets its cloud-issued IDs at
// once if known; otherwise the bridge waits loginAckGrace for the cloud's
// ack (noteCloudLoginAck cancels the wait) and only then synthesizes one.
func (bs *bridgeSet) ackLogin(mac, msgid string, localOnly bool) {
	if localOnly {
		bs.publishLocalAck(mac, BuildLoginAck(mac, msgid))
		return
	}
	if ids, ok := bs.cloudSessionIDs(mac); ok {
		bs.publishLocalAck(mac, buildLoginAckWithIDs(mac, msgid, ids))
		return
	}
	bs.acks.mu.Lock()
	defer bs.acks.mu.Unlock()
	if bs.acks.pending == nil {
		bs.acks.pending = map[string]*time.Timer{}
	}
	if t := bs.acks.pending[msgid]; t != nil {
		t.Stop()
	}
	bs.acks.pending[msgid] = time.AfterFunc(loginAckGrace, func() {
		bs.acks.mu.Lock()
		_, still := bs.acks.pending[msgid]
		delete(bs.acks.pending, msgid)
		bs.acks.mu.Unlock()
		if still {
			bs.publishLocalAck(mac, BuildLoginAck(mac, msgid))
		}
	})
}

// noteCloudLoginAck records the IDs in a cloud login_ack and cancels any
// pending synthetic ack for the login it answers.
func (bs *bridgeSet) noteCloudLoginAck(mac string, payload []byte) {
	var msg struct {
		Data struct {
			AckMsgid string `json:"ack_msgid"`
			DeviceID string `json:"device_id"`
			UID      string `json:"uid"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &msg) != nil {
		return
	}
	ids := sessionIDs{msg.Data.DeviceID, msg.Data.UID}
	bs.acks.mu.Lock()
	if t := bs.acks.pending[msg.Data.AckMsgid]; t != nil {
		t.Stop()
		delete(bs.acks.pending, msg.Data.AckMsgid)
	}
	changed := false
	if ids.deviceID != "" && ids.uid != "" {
		if bs.acks.known == nil {
			bs.acks.known = map[string]sessionIDs{}
		}
		changed = bs.acks.known[mac] != ids
		bs.acks.known[mac] = ids
	}
	bs.acks.mu.Unlock()
	if changed {
		log.Printf("[%s] cloud session ids device_id=%s uid=%s", mac, ids.deviceID, ids.uid)
		if st := bs.sessionStore(); st != nil {
			st.SetCloudSessionIDs(mac, ids.deviceID, ids.uid)
		}
	}
}

func buildLoginAckWithIDs(mac, ackMsgid string, ids sessionIDs) []byte {
	return BuildBrokerAckPayload(mac, "login_ack", ackMsgid, map[string]any{
		"device_id": ids.deviceID,
		"uid":       ids.uid,
	})
}
