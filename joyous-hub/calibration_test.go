package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCalibrationPNGEmbedded(t *testing.T) {
	if _, err := inkjoyCalibrationPNG(); err != nil {
		t.Fatal(err)
	}
	if _, err := samsungCalibrationPNG(); err != nil {
		t.Fatal(err)
	}
}

func TestCalibrationPNGHandler(t *testing.T) {
	dir := t.TempDir()
	h := &Hub{images: NewImageStore(dir)}
	rec := httptest.NewRecorder()
	h.handleCalibrationPNG(rec, httptest.NewRequest(http.MethodGet, "/api/calibration/inkjoy", nil), "inkjoy")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type %q", ct)
	}
	if len(rec.Body.Bytes()) < 1000 {
		t.Fatal("png too small")
	}
}

func TestEnsureInkJoyCalibrationID(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id1, err := store.ensureInkJoyCalibrationID()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := store.ensureInkJoyCalibrationID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("expected same id, got %s and %s", id1, id2)
	}
	meta, err := store.readMeta(id1)
	if err != nil || !meta.FlatRGB {
		t.Fatalf("expected flat_rgb calibration meta: %+v err=%v", meta, err)
	}
}
