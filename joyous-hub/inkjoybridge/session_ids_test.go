package inkjoybridge

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/packets"
)

type memSessionStore struct {
	DeviceEvents
	mu  sync.Mutex
	ids map[string][2]string
}

func (m *memSessionStore) SessionIDs(mac string) (string, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.ids[mac]
	return v[0], v[1], ok
}

func (m *memSessionStore) SetSessionIDs(mac, d, u string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ids[mac] = [2]string{d, u}
}

type ackCapture struct {
	mu     sync.Mutex
	acks   []struct{ AckMsgid, DeviceID, UID string }
	hearts int
}

func (c *ackCapture) loginAcks() []struct{ AckMsgid, DeviceID, UID string } {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append(c.acks[:0:0], c.acks...)
}

// newAckTestBridges returns a bridgeSet whose local acks are captured.
func newAckTestBridges(t *testing.T, store *memSessionStore) (*bridgeSet, *ackCapture) {
	t.Helper()
	broker := mochi.New(&mochi.Options{InlineClient: true})
	capt := &ackCapture{}
	if err := broker.Subscribe("/inkjoyap/#", 1, func(_ *mochi.Client, _ packets.Subscription, pk packets.Packet) {
		var msg struct {
			Action string `json:"action"`
			Data   struct {
				AckMsgid string `json:"ack_msgid"`
				DeviceID string `json:"device_id"`
				UID      string `json:"uid"`
			} `json:"data"`
		}
		if json.Unmarshal(pk.Payload, &msg) == nil && msg.Action == "heart_ack" {
			capt.mu.Lock()
			capt.hearts++
			capt.mu.Unlock()
		}
		if json.Unmarshal(pk.Payload, &msg) == nil && msg.Action == "login_ack" {
			capt.mu.Lock()
			capt.acks = append(capt.acks, struct{ AckMsgid, DeviceID, UID string }{msg.Data.AckMsgid, msg.Data.DeviceID, msg.Data.UID})
			capt.mu.Unlock()
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { broker.Close() })
	bs := &bridgeSet{broker: broker}
	if store != nil {
		bs.devices = store
	}
	return bs, capt
}

func waitAcks(t *testing.T, c *ackCapture, n int) []struct{ AckMsgid, DeviceID, UID string } {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a := c.loginAcks(); len(a) >= n {
			return a
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("got %d login_acks, want %d", len(c.loginAcks()), n)
	return nil
}

func cloudLoginAck(ackMsgid, deviceID, uid string) []byte {
	return BuildBrokerAckPayload("D0CF13EF4080", "login_ack", ackMsgid, map[string]any{"device_id": deviceID, "uid": uid})
}

func shortAckTimers(t *testing.T) {
	g, w := loginAckGrace, upstreamSendWait
	loginAckGrace, upstreamSendWait = 30*time.Millisecond, 80*time.Millisecond
	t.Cleanup(func() { loginAckGrace, upstreamSendWait = g, w })
}

func pubkeyReport(mac, deviceID, uid string) []byte {
	return BuildBrokerAckPayload(mac, "pubkey_report", "", map[string]any{"device_id": deviceID, "uid": uid})
}

// A frame the cloud answers gets no login or heartbeat replies from the bridge,
// even when the bridge already knows its IDs.
func TestCloudAnsweredFrameGetsNoBridgeReplies(t *testing.T) {
	shortAckTimers(t)
	const mac, realD, realU = "D0CF13EF4080", "16a2976f-real", "abf9ab97-real"
	store := &memSessionStore{ids: map[string][2]string{mac: {realD, realU}}}
	bs, capt := newAckTestBridges(t, store)

	bs.ackLogin(mac, "m1", false)
	bs.loginSentUpstream(mac, "m1")
	bs.noteCloudLoginAck(mac, cloudLoginAck("m1", realD, realU))
	bs.ackHeart(mac, "h1", false)
	time.Sleep(4 * upstreamSendWait)
	capt.mu.Lock()
	defer capt.mu.Unlock()
	if len(capt.acks) != 0 || capt.hearts != 0 {
		t.Fatalf("bridge replied alongside the cloud: %d login_acks, %d heart_acks", len(capt.acks), capt.hearts)
	}
	if d, u, ok := store.SessionIDs(mac); !ok || d != realD || u != realU {
		t.Fatalf("store = %q %q %v", d, u, ok)
	}
}

// Right after a bridge restart the cloud link is still connecting when the
// frame logs in: nothing may answer before the login has gone upstream.
func TestLoginHeldUntilSentUpstream(t *testing.T) {
	shortAckTimers(t)
	const mac = "D0CF13EF4080"
	bs, capt := newAckTestBridges(t, &memSessionStore{ids: map[string][2]string{}})
	bs.ackLogin(mac, "m1", false)
	time.Sleep(upstreamSendWait / 2)
	bs.loginSentUpstream(mac, "m1")
	bs.noteCloudLoginAck(mac, cloudLoginAck("m1", "real-d", "real-u"))
	time.Sleep(4 * upstreamSendWait)
	if a := capt.loginAcks(); len(a) != 0 {
		t.Fatalf("bridge answered a login the cloud answered: %+v", a)
	}
}

// A frame the cloud ignores (the 28.5") is answered by the bridge with the IDs
// it already holds — never new ones — and its heartbeats are answered too.
func TestSilentCloudAnsweredWithFramesOwnIDs(t *testing.T) {
	shortAckTimers(t)
	const mac, d, u = "3844BE396C90", "cloud-registered-d", "cloud-registered-u"
	bs, capt := newAckTestBridges(t, &memSessionStore{ids: map[string][2]string{}})
	bs.noteFramePubkeyReport(mac, pubkeyReport(mac, d, u))
	bs.ackLogin(mac, "m1", false)
	bs.loginSentUpstream(mac, "m1")
	if a := waitAcks(t, capt, 1); a[0].AckMsgid != "m1" || a[0].DeviceID != d || a[0].UID != u {
		t.Fatalf("fallback ack %+v, want the frame's own ids", a[0])
	}
	bs.ackHeart(mac, "h1", false)
	deadline := time.Now().Add(time.Second)
	for {
		capt.mu.Lock()
		n := capt.hearts
		capt.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cloud-silent frame's heartbeat not answered")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A frame that has never had IDs, with no cloud link at all, still gets an
// answer (synthetic) once upstreamSendWait passes.
func TestNoUpstreamFallsBackToSynthetic(t *testing.T) {
	shortAckTimers(t)
	bs, capt := newAckTestBridges(t, &memSessionStore{ids: map[string][2]string{}})
	bs.ackLogin("3844BE396C90", "m1", false)
	d, u := localOnlySessionIDs("3844BE396C90")
	if a := waitAcks(t, capt, 1); a[0].DeviceID != d || a[0].UID != u {
		t.Fatalf("fallback ack %+v", a[0])
	}
}

// Local-only frames are answered at once with the synthetic IDs.
func TestLocalOnlyLoginAckImmediate(t *testing.T) {
	bs, capt := newAckTestBridges(t, nil)
	bs.ackLogin("94A990D08BE0", "m1", true)
	d, _ := localOnlySessionIDs("94A990D08BE0")
	if a := waitAcks(t, capt, 1); a[0].DeviceID != d {
		t.Fatalf("local-only ack %+v", a[0])
	}
}

// Frame messages sent while the cloud link is down are queued, not dropped.
func TestForwardQueuesUntilConnected(t *testing.T) {
	b := &upstreamBridge{mac: "D0CF13EF4080", set: &bridgeSet{}}
	for i := range upstreamQueueMax + 5 {
		b.forward([]byte{byte(i)})
	}
	if len(b.queue) != upstreamQueueMax || b.queue[0][0] != 0 {
		t.Fatalf("queued %d, want the first %d", len(b.queue), upstreamQueueMax)
	}
}
