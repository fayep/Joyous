package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBridgeDeviceGateSerializesAndSupersedes(t *testing.T) {
	var g bridgeDeviceGate
	var order []int
	var mu sync.Mutex
	var firstStarted sync.WaitGroup
	firstStarted.Add(1)

	errCh := make(chan error, 2)
	go func() {
		errCh <- g.Run(context.Background(), "dev1", func(ctx context.Context) error {
			firstStarted.Done()
			select {
			case <-ctx.Done():
				mu.Lock()
				order = append(order, 1)
				mu.Unlock()
				return ctx.Err()
			case <-time.After(2 * time.Second):
				t.Error("first job should have been cancelled")
				return nil
			}
		})
	}()
	firstStarted.Wait()

	secondDone := make(chan struct{})
	go func() {
		errCh <- g.Run(context.Background(), "dev1", func(ctx context.Context) error {
			mu.Lock()
			order = append(order, 2)
			mu.Unlock()
			close(secondDone)
			return nil
		})
	}()
	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("second job blocked too long")
	}

	err1 := <-errCh
	err2 := <-errCh
	if !errors.Is(err1, context.Canceled) && !errors.Is(err1, errBridgeSendSuperseded) {
		// First returns ctx.Err() from fn; gate may wrap if fn returned nil after cancel.
		if err1 != nil && !errors.Is(err1, context.Canceled) {
			t.Fatalf("first err=%v", err1)
		}
	}
	if err2 != nil {
		t.Fatalf("second err=%v", err2)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 || order[len(order)-1] != 2 {
		t.Fatalf("order=%v, want …2 last", order)
	}
}

func TestBridgeDeviceGateCancelAbortsWait(t *testing.T) {
	var g bridgeDeviceGate
	var polls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := g.Run(ctx, "dev", func(jobCtx context.Context) error {
		return waitForMDCAwakeManual(jobCtx, "127.0.0.1", "", 5*time.Second, 5*time.Millisecond, func(string, int) {
			polls.Add(1)
		})
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want canceled", err)
	}
	if polls.Load() == 0 {
		t.Fatal("expected at least one wake poll before cancel")
	}
}

func TestFailOthersForDevice(t *testing.T) {
	tr := NewSendDeliveryTracker()
	a := tr.Register("dev", "imgA")
	b := tr.Register("dev", "imgB")
	other := tr.Register("other", "imgC")
	failed := tr.FailOthersForDevice("dev", b.ID, "superseded by a newer send")
	if len(failed) != 1 || failed[0] != a.ID {
		t.Fatalf("failed=%v, want [%s]", failed, a.ID)
	}
	if d := tr.Get(a.ID); d == nil || d.Status != sendStatusFailed || d.LastError != "superseded by a newer send" {
		t.Fatalf("a: %+v", d)
	}
	if d := tr.Get(b.ID); d == nil || d.Status != sendStatusPending {
		t.Fatalf("b should stay pending: %+v", d)
	}
	if d := tr.Get(other.ID); d == nil || d.Status != sendStatusPending {
		t.Fatalf("other device should stay pending: %+v", d)
	}
}
