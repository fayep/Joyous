package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type stubWeatherClient struct {
	calls   int
	err     error
	snap    WeatherSnapshot
	delay   time.Duration
}

func (s *stubWeatherClient) Fetch(ctx context.Context, cfg OverlayConfig) (WeatherSnapshot, error) {
	s.calls++
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.err != nil {
		return WeatherSnapshot{}, s.err
	}
	return s.snap, nil
}

func TestCachedWeatherReturnsFreshWithoutSecondCall(t *testing.T) {
	dir := t.TempDir()
	inner := &stubWeatherClient{snap: WeatherSnapshot{City: "SF", TempC: 18}}
	c := newCachedWeatherFetcher(inner, dir)
	cfg := OverlayConfig{Location: "San Francisco", Latitude: 37.77, Longitude: -122.42}

	s1, err := c.Fetch(context.Background(), cfg)
	if err != nil || s1.City != "SF" {
		t.Fatalf("first fetch: %v %q", err, s1.City)
	}
	s2, err := c.Fetch(context.Background(), cfg)
	if err != nil || s2.City != "SF" {
		t.Fatalf("second fetch: %v %q", err, s2.City)
	}
	if inner.calls != 1 {
		t.Fatalf("expected 1 upstream call, got %d", inner.calls)
	}
}

func TestCachedWeatherStaleOnError(t *testing.T) {
	dir := t.TempDir()
	inner := &stubWeatherClient{snap: WeatherSnapshot{City: "SF", TempC: 20}}
	c := newCachedWeatherFetcher(inner, dir)
	cfg := OverlayConfig{Location: "San Francisco"}

	if _, err := c.Fetch(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	// Expire fresh TTL but keep within stale window.
	c.mu.Lock()
	key := weatherCacheKey(cfg)
	ent := c.entries[key]
	ent.FetchedAt = time.Now().Add(-weatherCacheFresh - time.Minute)
	c.entries[key] = ent
	c.mu.Unlock()

	inner.err = errors.New("tls handshake timeout")
	snap, err := c.Fetch(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected stale fallback, got %v", err)
	}
	if snap.City != "SF" || snap.TempC != 20 {
		t.Fatalf("stale snapshot wrong: %+v", snap)
	}
	if inner.calls != 2 {
		t.Fatalf("expected refresh attempt, got %d calls", inner.calls)
	}
}

func TestCachedWeatherPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	inner := &stubWeatherClient{snap: WeatherSnapshot{City: "Oakland", TempC: 22}}
	c1 := newCachedWeatherFetcher(inner, dir)
	cfg := OverlayConfig{Location: "Oakland"}
	if _, err := c1.Fetch(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, weatherCacheFile)); err != nil {
		t.Fatalf("cache file missing: %v", err)
	}

	inner2 := &stubWeatherClient{snap: WeatherSnapshot{City: "Berkeley"}}
	c2 := newCachedWeatherFetcher(inner2, dir)
	snap, err := c2.Fetch(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if snap.City != "Oakland" {
		t.Fatalf("expected disk cache hit, got %+v", snap)
	}
	if inner2.calls != 0 {
		t.Fatalf("expected no upstream call within fresh TTL, got %d", inner2.calls)
	}
}

func TestWeatherCacheKeyUsesCoords(t *testing.T) {
	a := weatherCacheKey(OverlayConfig{Location: "X", Latitude: 1, Longitude: 2})
	b := weatherCacheKey(OverlayConfig{Location: "X", Latitude: 1.0001, Longitude: 2})
	if a == b {
		t.Fatal("expected distinct keys for different coords")
	}
}
