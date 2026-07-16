package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestVerifyInkjoyCacheServing(t *testing.T) {
	dir := t.TempDir()
	cache := NewInkJoyCache(dir)
	mux := http.NewServeMux()
	registerInkJoyCacheRoutes(mux, nil, cache)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	if err := VerifyInkjoyCacheServing(ctx, srv.URL); err != nil {
		t.Fatalf("VerifyInkjoyCacheServing: %v", err)
	}
}

func TestHubInkjoyCacheRoute(t *testing.T) {
	dir := t.TempDir()
	cache := NewInkJoyCache(dir)
	mac := "AA:BB:CC:DD:EE:FF"
	name := "cloud-img"
	bin := make([]byte, frameW*frameH*2)
	if err := cache.Save(mac, name, bin); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerInkJoyCacheRoutes(mux, nil, cache)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/inkjoy/AABBCCDDEEFF/cloud-img.bin", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if len(rr.Body.Bytes()) != len(bin) {
		t.Fatalf("got %d bytes want %d", len(rr.Body.Bytes()), len(bin))
	}

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodHead, "/inkjoy/AABBCCDDEEFF/cloud-img.bin", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("HEAD status=%d", rr.Code)
	}
	if rr.Header().Get(inkjoyCacheResponseHeader) != "1" {
		t.Fatalf("missing %s header", inkjoyCacheResponseHeader)
	}
	if rr.Header().Get("Content-Length") != strconv.Itoa(len(bin)) {
		t.Fatalf("HEAD Content-Length=%q want %d", rr.Header().Get("Content-Length"), len(bin))
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx := context.Background()
	if err := ProbeHubURL(ctx, srv.URL+"/inkjoy/AABBCCDDEEFF/cloud-img.bin", int64(len(bin))); err != nil {
		t.Fatalf("ProbeHubURL: %v", err)
	}

	want := filepath.Join(dir, "inkjoy", "AABBCCDDEEFF", "cloud-img.bin")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("cache file: %v", err)
	}
}

func TestIsInkJoyFrameBinProxyPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"D0CF13EF4080/f958e0e2f9421aa6~b9e2b8993c-p.bin", true},
		{"D0CF13EF4080/f958e0e2f9421aa6~b9e2b8993c-p", false},
		{"api/devices", false},
		{"", false},
		{"D0CF13EF4080/extra/foo.bin", false},
	}
	for _, tc := range cases {
		if got := isInkJoyFrameBinProxyPath(tc.path); got != tc.want {
			t.Fatalf("isInkJoyFrameBinProxyPath(%q)=%v want %v", tc.path, got, tc.want)
		}
	}
}
