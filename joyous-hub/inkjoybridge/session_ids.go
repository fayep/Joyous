package inkjoybridge

import (
	"encoding/json"
	"log"
	"sync"
	"time"
)

// The bridge answers a frame's login/heart itself only when nobody else will:
// local-only frames (no cloud), and cloud-relayed frames whose cloud didn't
// answer (the 28.5"). A frame the cloud does answer gets the cloud's replies
// alone. Before this the 10" got two login_acks per login carrying different
// identities, adopted whichever it acted on, and restarted (rst:0xc SW_CPU)
// ~3s after each mismatch.
//
// A frame persists the device_id/uid from the login_ack it acts on and reports
// them back (pubkey_report). When the bridge must answer, it uses the IDs the
// frame already holds, so a local reply never changes a frame's identity;
// synthetic IDs are only for a frame that has never had any.

// SessionIDStore optionally persists each frame's session IDs across bridge
// restarts. Config.Devices may implement it.
type SessionIDStore interface {
	SessionIDs(mac string) (deviceID, uid string, ok bool)
	SetSessionIDs(mac, deviceID, uid string)
}

// loginAckGrace is how long a cloud-relayed frame's login, once actually sent
// upstream, waits for the cloud's own login_ack before the bridge answers.
var loginAckGrace = 3 * time.Second

// upstreamSendWait bounds how long a login is held for the cloud link to come
// up (it is opened on a frame's first message) before the bridge answers anyway.
var upstreamSendWait = 15 * time.Second

type sessionIDs struct{ deviceID, uid string }

type loginAcks struct {
	mu      sync.Mutex
	known   map[string]sessionIDs // mac → IDs the frame holds
	pending map[string]*loginWait // login msgid → held login
	silent  map[string]bool       // mac → cloud didn't answer its last login
}

type loginWait struct {
	mac  string
	sent bool // published upstream; now waiting loginAckGrace for the cloud
	t    *time.Timer
}

func (bs *bridgeSet) sessionStore() SessionIDStore {
	s, _ := bs.devices.(SessionIDStore)
	return s
}

// frameSessionIDs returns the IDs this frame holds, if known.
func (bs *bridgeSet) frameSessionIDs(mac string) (sessionIDs, bool) {
	bs.acks.mu.Lock()
	ids, ok := bs.acks.known[mac]
	bs.acks.mu.Unlock()
	if ok {
		return ids, true
	}
	if st := bs.sessionStore(); st != nil {
		if d, u, ok := st.SessionIDs(mac); ok && d != "" && u != "" {
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

// recordSessionIDs notes the IDs a frame holds (from the cloud's login_ack it
// will act on, or the frame's own pubkey_report) and persists any change.
func (bs *bridgeSet) recordSessionIDs(mac string, ids sessionIDs, source string) {
	if ids.deviceID == "" || ids.uid == "" {
		return
	}
	bs.acks.mu.Lock()
	if bs.acks.known == nil {
		bs.acks.known = map[string]sessionIDs{}
	}
	changed := bs.acks.known[mac] != ids
	bs.acks.known[mac] = ids
	bs.acks.mu.Unlock()
	if changed {
		log.Printf("[%s] session ids (%s) device_id=%s uid=%s", mac, source, ids.deviceID, ids.uid)
		if st := bs.sessionStore(); st != nil {
			st.SetSessionIDs(mac, ids.deviceID, ids.uid)
		}
	}
}

// ackLogin handles the bridge's side of a frame login. Local-only frames are
// answered at once. A cloud-relayed frame's login is held: once it has gone
// upstream (loginSentUpstream) the cloud gets loginAckGrace to answer
// (noteCloudLoginAck), and only if it doesn't, or the link never comes up
// within upstreamSendWait, does the bridge answer, with the frame's own IDs.
func (bs *bridgeSet) ackLogin(mac, msgid string, localOnly bool) {
	if localOnly {
		bs.publishLocalAck(mac, BuildLoginAck(mac, msgid))
		return
	}
	bs.acks.mu.Lock()
	defer bs.acks.mu.Unlock()
	if bs.acks.pending == nil {
		bs.acks.pending = map[string]*loginWait{}
	}
	if w := bs.acks.pending[msgid]; w != nil {
		w.t.Stop()
	}
	w := &loginWait{mac: mac}
	w.t = time.AfterFunc(upstreamSendWait, func() { bs.loginAckTimeout(msgid, w) })
	bs.acks.pending[msgid] = w
}

// loginSentUpstream starts the cloud's answer window for a held login.
func (bs *bridgeSet) loginSentUpstream(mac, msgid string) {
	bs.acks.mu.Lock()
	defer bs.acks.mu.Unlock()
	w := bs.acks.pending[msgid]
	if w == nil || w.sent {
		return
	}
	w.t.Stop()
	w.sent = true
	w.t = time.AfterFunc(loginAckGrace, func() { bs.loginAckTimeout(msgid, w) })
}

// loginAckTimeout answers a held login the cloud didn't, and marks the frame
// cloud-silent so its heartbeats are answered locally too.
func (bs *bridgeSet) loginAckTimeout(msgid string, w *loginWait) {
	bs.acks.mu.Lock()
	if bs.acks.pending[msgid] != w {
		bs.acks.mu.Unlock()
		return
	}
	delete(bs.acks.pending, msgid)
	if bs.acks.silent == nil {
		bs.acks.silent = map[string]bool{}
	}
	bs.acks.silent[w.mac] = true
	bs.acks.mu.Unlock()
	ids, ok := bs.frameSessionIDs(w.mac)
	if !ok {
		ids.deviceID, ids.uid = localOnlySessionIDs(w.mac)
	}
	bs.publishLocalAck(w.mac, buildLoginAckWithIDs(w.mac, msgid, ids))
}

// ackHeart answers a heartbeat locally only for frames nobody else answers.
func (bs *bridgeSet) ackHeart(mac, msgid string, localOnly bool) {
	bs.acks.mu.Lock()
	silent := bs.acks.silent[mac]
	bs.acks.mu.Unlock()
	if localOnly || silent {
		bs.publishLocalAck(mac, BuildHeartAck(mac, msgid))
	}
}

// noteCloudLoginAck records a cloud login_ack: that login needs no local
// reply, the frame isn't cloud-silent, and its IDs are the ones the frame
// will hold.
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
	bs.acks.mu.Lock()
	if w := bs.acks.pending[msg.Data.AckMsgid]; w != nil {
		w.t.Stop()
		delete(bs.acks.pending, msg.Data.AckMsgid)
	}
	delete(bs.acks.silent, mac)
	bs.acks.mu.Unlock()
	bs.recordSessionIDs(mac, sessionIDs{msg.Data.DeviceID, msg.Data.UID}, "cloud login_ack")
}

// noteFramePubkeyReport records the IDs a frame reports holding.
func (bs *bridgeSet) noteFramePubkeyReport(mac string, payload []byte) {
	var msg struct {
		Data struct {
			DeviceID string `json:"device_id"`
			UID      string `json:"uid"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &msg) == nil {
		bs.recordSessionIDs(mac, sessionIDs{msg.Data.DeviceID, msg.Data.UID}, "frame pubkey_report")
	}
}

func buildLoginAckWithIDs(mac, ackMsgid string, ids sessionIDs) []byte {
	return BuildBrokerAckPayload(mac, "login_ack", ackMsgid, map[string]any{
		"device_id": ids.deviceID,
		"uid":       ids.uid,
	})
}
