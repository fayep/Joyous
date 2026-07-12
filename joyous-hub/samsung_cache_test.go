package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestIsSamsungFramePullProxyPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"192-168-1-108/content.json", true},
		{"192-168-1-108/image", true},
		{"192-168-1-108/status", true},
		{"B0F2F657D5CD.png", true},
		{"B0F2F657D5CD.lock", true},
		{"api/samsung", false},
		{"api/list", false},
		{"B0F2F657D5CD/preview", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isSamsungFramePullProxyPath(tc.path); got != tc.want {
			t.Errorf("isSamsungFramePullProxyPath(%q)=%v want %v", tc.path, got, tc.want)
		}
	}
}

func TestSamsungFramePullRejectedByBridgeProxy(t *testing.T) {
	hub := &Hub{}
	mux := http.NewServeMux()
	registerBridgeRoutes(mux, hub)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/samsung/192-168-1-108/image", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "hub cache") {
		t.Fatalf("body=%q want hub cache message", rr.Body.String())
	}
}

func TestHubSamsungImageServedBeforeBridgeProxy(t *testing.T) {
	dir := t.TempDir()
	store := NewSamsungStore(dir)
	frameID := "testframe"
	png := testPNG()
	if err := store.writePNGLocked(frameID, png); err != nil {
		t.Fatal(err)
	}

	hub := &Hub{samsung: store}
	mux := http.NewServeMux()
	registerRoutes(mux, hub)
	registerBridgeRoutes(mux, hub)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/samsung/"+frameID+"/image", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if rr.Header().Get(samsungCacheResponseHeader) != "1" {
		t.Fatalf("missing %s header", samsungCacheResponseHeader)
	}
	if len(rr.Body.Bytes()) != len(png) {
		t.Fatalf("got %d bytes want %d", len(rr.Body.Bytes()), len(png))
	}

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodHead, "/samsung/"+frameID+"/image", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("HEAD status=%d", rr.Code)
	}
	if rr.Header().Get("Content-Length") != strconv.Itoa(len(png)) {
		t.Fatalf("HEAD Content-Length=%q want %d", rr.Header().Get("Content-Length"), len(png))
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx := context.Background()
	if err := VerifySamsungCacheServing(ctx, srv.URL); err != nil {
		t.Fatalf("VerifySamsungCacheServing: %v", err)
	}
	if err := ProbeSamsungHubURL(ctx, srv.URL+"/samsung/"+frameID+"/image", int64(len(png))); err != nil {
		t.Fatalf("ProbeSamsungHubURL: %v", err)
	}

	want := filepath.Join(dir, "samsung", frameID+".png")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("cache file: %v", err)
	}
}
