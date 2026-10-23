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

func (m *memSessionStore) CloudSessionIDs(mac string) (string, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.ids[mac]
	return v[0], v[1], ok
}

func (m *memSessionStore) SetCloudSessionIDs(mac, d, u string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ids[mac] = [2]string{d, u}
}

type ackCapture struct {
	mu   sync.Mutex
	acks []struct{ AckMsgid, DeviceID, UID string }
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

// A cloud-relayed frame must only ever be told the cloud's identity.
func TestCloudRelayedLoginAckUsesCloudIDs(t *testing.T) {
	saved := loginAckGrace
	loginAckGrace = 50 * time.Millisecond
	t.Cleanup(func() { loginAckGrace = saved })
	const mac, realD, realU = "D0CF13EF4080", "16a2976f-real", "abf9ab97-real"
	store := &memSessionStore{ids: map[string][2]string{}}
	bs, capt := newAckTestBridges(t, store)

	// IDs unknown: the bridge holds its ack; the cloud answers in time.
	bs.ackLogin(mac, "m1", false)
	bs.noteCloudLoginAck(mac, cloudLoginAck("m1", realD, realU))
	time.Sleep(3 * loginAckGrace)
	if a := capt.loginAcks(); len(a) != 0 {
		t.Fatalf("bridge sent its own login_ack despite the cloud's: %+v", a)
	}
	if d, u, ok := store.CloudSessionIDs(mac); !ok || d != realD || u != realU {
		t.Fatalf("store = %q %q %v", d, u, ok)
	}

	// IDs known (also after a bridge restart, via the store): ack at once with them.
	bs2, capt2 := newAckTestBridges(t, store)
	bs2.ackLogin(mac, "m2", false)
	if a := waitAcks(t, capt2, 1); a[0].AckMsgid != "m2" || a[0].DeviceID != realD || a[0].UID != realU {
		t.Fatalf("local ack %+v, want cloud ids", a[0])
	}
}

// A frame the cloud never answers (28.5") still gets a login_ack, synthetic.
func TestSilentCloudFallsBackToSyntheticAck(t *testing.T) {
	saved := loginAckGrace
	loginAckGrace = 30 * time.Millisecond
	t.Cleanup(func() { loginAckGrace = saved })
	bs, capt := newAckTestBridges(t, &memSessionStore{ids: map[string][2]string{}})
	bs.ackLogin("3844BE396C90", "m1", false)
	d, u := localOnlySessionIDs("3844BE396C90")
	if a := waitAcks(t, capt, 1); a[0].AckMsgid != "m1" || a[0].DeviceID != d || a[0].UID != u {
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
