package main

import (
	"context"
	"errors"
	"sync"
)

// errBridgeSendSuperseded is returned when a newer send/push for the same device
// cancelled this one. Without per-device serialization, concurrent send.image
// goroutines race on the frame PNG cache: an older encode can finish after a
// newer one and overwrite it, so the frame displays the previously selected
// (undelivered) image instead of the latest.
var errBridgeSendSuperseded = errors.New("superseded by a newer send")

// bridgeDeviceGate runs at most one long-running job per deviceID. Starting a
// new job cancels the previous context and waits for it to exit before the new
// job proceeds (so cache writes stay ordered).
type bridgeDeviceGate struct {
	mu      sync.Mutex
	runners map[string]*bridgeDeviceRunner
}

type bridgeDeviceRunner struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (g *bridgeDeviceGate) Run(parent context.Context, deviceID string, fn func(context.Context) error) error {
	if deviceID == "" {
		return fn(parent)
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	r := &bridgeDeviceRunner{cancel: cancel, done: done}

	g.mu.Lock()
	if g.runners == nil {
		g.runners = make(map[string]*bridgeDeviceRunner)
	}
	prev := g.runners[deviceID]
	g.runners[deviceID] = r
	g.mu.Unlock()

	if prev != nil {
		prev.cancel()
		<-prev.done
	}

	defer func() {
		cancel()
		close(done)
		g.mu.Lock()
		if g.runners[deviceID] == r {
			delete(g.runners, deviceID)
		}
		g.mu.Unlock()
	}()

	if err := parent.Err(); err != nil {
		return err
	}
	err := fn(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return errBridgeSendSuperseded
	}
	return nil
}
