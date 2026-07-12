package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHubInkjoyBinRoutePrecedence(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /inkjoy/{mac}/{file}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "BIN %s", r.PathValue("file"))
	})
	mux.HandleFunc("GET /inkjoy/{path...}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("MQTT"))
	})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/inkjoy/D0CF13EF4080/foo.bin", nil))
	if rr.Body.String() != "BIN foo.bin" {
		t.Fatalf("got %q want bin handler", rr.Body.String())
	}
}
