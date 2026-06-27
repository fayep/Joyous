package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAccessLogMiddleware(t *testing.T) {
	handler := accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("body %q", rec.Body.String())
	}
}

func TestQuietAccessLogger(t *testing.T) {
	q := newQuietAccessLogger(30 * time.Millisecond)

	if !q.shouldLog() {
		t.Fatal("first hit should log")
	}
	if q.shouldLog() {
		t.Fatal("immediate second hit should be suppressed")
	}
	if q.shouldLog() {
		t.Fatal("third hit within quiet window should be suppressed")
	}

	time.Sleep(35 * time.Millisecond)

	if !q.shouldLog() {
		t.Fatal("hit after quiet window should log again")
	}
	if q.shouldLog() {
		t.Fatal("next hit should be suppressed again")
	}
}

func TestDevicesListPollAccessLogSuppressed(t *testing.T) {
	orig := devicesListAccessLog
	origImages := imagesRevisionAccessLog
	origMQTT := mqttLogsAccessLog
	origSamsung := samsungListAccessLog
	t.Cleanup(func() {
		devicesListAccessLog = orig
		imagesRevisionAccessLog = origImages
		mqttLogsAccessLog = origMQTT
		samsungListAccessLog = origSamsung
	})
	devicesListAccessLog = newQuietAccessLogger(30 * time.Second)
	imagesRevisionAccessLog = newQuietAccessLogger(30 * time.Second)
	mqttLogsAccessLog = newQuietAccessLogger(30 * time.Second)
	samsungListAccessLog = newQuietAccessLogger(30 * time.Second)

	var buf bytes.Buffer
	origLog := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(origLog) })

	handler := accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/devices", nil))
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 access log line, got %d:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "GET /api/devices") {
		t.Fatalf("unexpected log line: %q", lines[0])
	}

	buf.Reset()
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/images/revision", nil))
	}
	lines = strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 images revision access log line, got %d:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "GET /api/images/revision") {
		t.Fatalf("unexpected log line: %q", lines[0])
	}

	buf.Reset()
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mqtt/logs", nil))
	}
	lines = strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 mqtt logs access log line, got %d:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "GET /api/mqtt/logs") {
		t.Fatalf("unexpected log line: %q", lines[0])
	}

	buf.Reset()
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/samsung", nil))
	}
	lines = strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 samsung access log line, got %d:\n%s", len(lines), buf.String())
	}
}

func TestImageThumb304AccessLogSuppressed(t *testing.T) {
	var buf bytes.Buffer
	origLog := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(origLog) })

	notModified := accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		notModified.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/images/abc123/thumb", nil))
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no access logs for thumb 304, got:\n%s", buf.String())
	}

	okHandler := accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("jpeg"))
	}))
	rec := httptest.NewRecorder()
	okHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/images/abc123/thumb", nil))
	if buf.Len() == 0 {
		t.Fatal("thumb 200 should still log")
	}
}

func TestSamsungPNG304AccessLogSuppressed(t *testing.T) {
	var buf bytes.Buffer
	origLog := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(origLog) })

	notModified := accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		notModified.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/samsung/192-168-1-108.png", nil))
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no access logs for samsung png 304, got:\n%s", buf.String())
	}

	okHandler := accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("png"))
	}))
	rec := httptest.NewRecorder()
	okHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/samsung/192-168-1-108.png", nil))
	if buf.Len() == 0 {
		t.Fatal("samsung png 200 should still log")
	}
}
