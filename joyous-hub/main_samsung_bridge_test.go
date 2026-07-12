//go:build samsungbridge

package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryWithProgressSucceedsAfterFailures(t *testing.T) {
	attempts := 0
	var retried []int
	err := retryWithProgress(context.Background(), time.Second, time.Millisecond,
		func() error {
			attempts++
			if attempts < 3 {
				return errors.New("not yet")
			}
			return nil
		},
		func(n int, _ error) { retried = append(retried, n) },
	)
	if err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("got %d attempts, want 3", attempts)
	}
	if len(retried) != 2 || retried[0] != 1 || retried[1] != 2 {
		t.Fatalf("got onRetry calls %v, want [1 2]", retried)
	}
}

func TestRetryWithProgressGivesUpAfterWindow(t *testing.T) {
	attempts := 0
	err := retryWithProgress(context.Background(), 30*time.Millisecond, 10*time.Millisecond,
		func() error { attempts++; return errors.New("always fails") },
		func(int, error) {},
	)
	if err == nil {
		t.Fatal("expected an error once the window elapses")
	}
	if attempts < 2 {
		t.Fatalf("expected at least 2 attempts before giving up, got %d", attempts)
	}
}

func TestRetryWithProgressStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	done := make(chan error, 1)
	go func() {
		done <- retryWithProgress(ctx, time.Minute, 50*time.Millisecond,
			func() error { attempts++; return errors.New("fails") },
			func(int, error) {},
		)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error after context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("retryWithProgress did not stop after context cancellation")
	}
}

func TestRetryWithProgressSucceedsFirstTryNoRetryCalls(t *testing.T) {
	called := false
	err := retryWithProgress(context.Background(), time.Second, time.Millisecond,
		func() error { return nil },
		func(int, error) { called = true },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("onRetry must not be called when the first attempt succeeds")
	}
}
