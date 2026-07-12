package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"joyous-hub/protocol"
)

func TestInkJoyBinRequestRejectedByBridgeProxy(t *testing.T) {
	hub := &Hub{}
	mux := http.NewServeMux()
	registerBridgeRoutes(mux, hub)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/inkjoy/D0CF13EF4080/f958e0e2f9421aa6~b9e2b8993c-p.bin", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "hub cache") {
		t.Fatalf("body=%q want hub cache message", rr.Body.String())
	}
}

func TestHubInkjoyBinServedBeforeBridgeProxy(t *testing.T) {
	dir := t.TempDir()
	cache := NewInkJoyCache(dir)
	mac := "D0CF13EF4080"
	name := "f958e0e2f9421aa6~b9e2b8993c-p"
	bin := []byte{1, 2, 3}
	if err := cache.Save(mac, name, bin); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerInkJoyCacheRoutes(mux, cache)
	registerBridgeRoutes(mux, &Hub{})

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/inkjoy/"+mac+"/"+name+".bin", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if string(rr.Body.Bytes()) != string(bin) {
		t.Fatalf("got %v want %v", rr.Body.Bytes(), bin)
	}
}

func TestInkJoyAPIStillUsesBridgeProxyPath(t *testing.T) {
	if !isInkJoyFrameBinProxyPath("api/devices") {
		return
	}
	t.Fatal("api/devices must not be treated as frame bin path")
}

func TestInkJoyBinProxyGuardBridgeKind(t *testing.T) {
	if protocol.KindInkJoy != "inkjoy" {
		t.Fatalf("unexpected kind constant %q", protocol.KindInkJoy)
	}
}
