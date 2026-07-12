package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"joyous-hub/bridgehub"
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

// TestHandleStaticForwardsBridgeRegisteredPath covers the catch-all HTTP route recognizing a
// path a connected bridge registered via HelloPayload.HTTPPaths (e.g. Samsung frames' own
// /content-transfer-progress callback) and attempting to forward it, instead of serving the
// SPA. With no real MQTT broker wired up the forward itself can't complete, but that failure
// (502, not the SPA's 200) is exactly what proves handleStatic recognized and routed the path
// rather than falling through to serving index.html.
func TestHandleStaticForwardsBridgeRegisteredPath(t *testing.T) {
	coord := bridgehub.NewCoordinator(nil, nil)
	payload, err := protocol.NewEnvelope(protocol.TypeHello, "samsung", protocol.HelloPayload{
		Kind:      "samsung",
		HTTPPaths: []string{"/content-transfer-progress"},
	})
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	coord.HandleMessage(protocol.BridgeTopic("samsung", "presence"), payload)

	hub := &Hub{bridgeCoord: coord}
	mux := http.NewServeMux()
	mux.HandleFunc("/", hub.handleStatic)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/content-transfer-progress", strings.NewReader(`{}`)))
	if rr.Code == http.StatusOK {
		t.Fatalf("got 200 (served the SPA instead of forwarding); body=%q", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "broker unavailable") {
		t.Fatalf("expected the request to be recognized and forwarded (failing only on the missing broker), got status=%d body=%q", rr.Code, rr.Body.String())
	}

	// An unregistered path must still fall through to serving the SPA as before.
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/some-other-path", nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("unregistered path: status=%d want 200 (SPA)", rr2.Code)
	}
}
