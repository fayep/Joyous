package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	weatherCacheFile  = "weather_cache.json"
	weatherCacheFresh = 30 * time.Minute
	weatherCacheStale = 24 * time.Hour
)

type weatherCacheEntry struct {
	Snapshot  WeatherSnapshot `json:"snapshot"`
	FetchedAt time.Time       `json:"fetched_at"`
}

type cachedWeatherFetcher struct {
	inner   weatherClient
	dir     string
	mu      sync.Mutex
	entries map[string]weatherCacheEntry
}

func newCachedWeatherFetcher(inner weatherClient, dataDir string) *cachedWeatherFetcher {
	c := &cachedWeatherFetcher{
		inner:   inner,
		dir:     dataDir,
		entries: make(map[string]weatherCacheEntry),
	}
	c.load()
	return c
}

func weatherCacheKey(cfg OverlayConfig) string {
	return fmt.Sprintf("%s|%.4f|%.4f|%s",
		strings.TrimSpace(cfg.Location),
		cfg.Latitude,
		cfg.Longitude,
		strings.TrimSpace(cfg.Timezone),
	)
}

func (c *cachedWeatherFetcher) Fetch(ctx context.Context, cfg OverlayConfig) (WeatherSnapshot, error) {
	key := weatherCacheKey(cfg)
	now := time.Now()

	c.mu.Lock()
	if ent, ok := c.entries[key]; ok && now.Sub(ent.FetchedAt) < weatherCacheFresh {
		snap := ent.Snapshot
		c.mu.Unlock()
		return snap, nil
	}
	c.mu.Unlock()

	snap, err := c.inner.Fetch(ctx, cfg)
	if err == nil {
		c.store(key, snap)
		return snap, nil
	}

	c.mu.Lock()
	ent, ok := c.entries[key]
	c.mu.Unlock()
	if ok && now.Sub(ent.FetchedAt) < weatherCacheStale {
		log.Printf("weather: open-meteo unavailable (%v); using cached data from %s ago",
			err, now.Sub(ent.FetchedAt).Round(time.Second))
		return ent.Snapshot, nil
	}
	return WeatherSnapshot{}, err
}

func (c *cachedWeatherFetcher) store(key string, snap WeatherSnapshot) {
	ent := weatherCacheEntry{Snapshot: snap, FetchedAt: time.Now()}
	c.mu.Lock()
	c.entries[key] = ent
	c.mu.Unlock()
	c.persist()
}

func (c *cachedWeatherFetcher) cachePath() string {
	return filepath.Join(c.dir, weatherCacheFile)
}

func (c *cachedWeatherFetcher) load() {
	if c.dir == "" {
		return
	}
	data, err := os.ReadFile(c.cachePath())
	if err != nil {
		return
	}
	var entries map[string]weatherCacheEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Printf("weather: ignore corrupt cache: %v", err)
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, ent := range entries {
		if now.Sub(ent.FetchedAt) <= weatherCacheStale {
			c.entries[key] = ent
		}
	}
}

func (c *cachedWeatherFetcher) persist() {
	if c.dir == "" {
		return
	}
	c.mu.Lock()
	entries := make(map[string]weatherCacheEntry, len(c.entries))
	now := time.Now()
	for key, ent := range c.entries {
		if now.Sub(ent.FetchedAt) <= weatherCacheStale {
			entries[key] = ent
		}
	}
	c.mu.Unlock()
	if err := os.MkdirAll(c.dir, 0755); err != nil {
		log.Printf("weather: cache mkdir: %v", err)
		return
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(c.cachePath(), b, 0644); err != nil {
		log.Printf("weather: cache write: %v", err)
	}
}

func newWeatherClient(dataDir string) weatherClient {
	inner := &weatherFetcher{client: &http.Client{Timeout: 12 * time.Second}}
	return newCachedWeatherFetcher(inner, dataDir)
}
