package inkjoybridge

import (
	"sync"
	"testing"
	"time"

	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/packets"
)

// A cloud play for a frame that isn't connected is held and delivered, with
// the cloud's msgid unchanged, after the frame next logs in.
func TestCloudPlayHeldUntilFrameLogsIn(t *testing.T) {
	saved := heldPlayDelay
	heldPlayDelay = 10 * time.Millisecond
	t.Cleanup(func() { heldPlayDelay = saved })

	broker := mochi.New(&mochi.Options{InlineClient: true})
	t.Cleanup(func() { broker.Close() })
	var mu sync.Mutex
	var got []string
	if err := broker.Subscribe("/inkjoyap/#", 1, func(_ *mochi.Client, _ packets.Subscription, pk packets.Packet) {
		mu.Lock()
		got = append(got, MQTTMsgid(pk.Payload))
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	const mac = "3844BE396C90"
	bs := &bridgeSet{broker: broker}
	play := BuildBrokerAckPayload(mac, "play", "", nil)
	cloudID := MQTTMsgid(play)

	bs.deliverCloudPlay(mac, "/inkjoyap/"+mac, play) // frame has no session
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("play published with no frame connected: %v", got)
	}

	bs.releaseHeldPlay(mac) // frame logged in
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		done := len(got) == 1
		mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != cloudID {
		t.Fatalf("delivered %v, want the cloud play %s once", got, cloudID)
	}
	bs.releaseHeldPlay(mac) // nothing left to deliver
}

func TestHeldPlayExpires(t *testing.T) {
	saved := heldPlayTTL
	heldPlayTTL = 0
	t.Cleanup(func() { heldPlayTTL = saved })
	bs := &bridgeSet{held: heldPlays{m: map[string]heldPlay{"m": {at: time.Now().Add(-time.Second)}}}}
	bs.releaseHeldPlay("m") // expired: must not touch the (nil) broker
}
