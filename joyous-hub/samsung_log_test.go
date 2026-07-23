package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSamsungLogSkipsHTTPPhotoLines(t *testing.T) {
	b := NewSamsungLogBuffer(20)
	prev := samsungHandshakeLog
	samsungHandshakeLog = b
	t.Cleanup(func() { samsungHandshakeLog = prev })

	recordSamsungOutbound("http GET http://hub/samsung/x.png 200 12ms", nil)
	recordSamsungOutbound("mdc banner ok ip=%s", []any{"192.168.1.108"})
	recordSamsungOutbound("discover found type=samsung id=%s ip=%s", []any{"samsung:x", "192.168.1.108"})

	ents := b.Snapshot()
	if len(ents) != 2 {
		t.Fatalf("entries=%d want 2 (http photo lines excluded): %#v", len(ents), ents)
	}
	if ents[0].Phase != "mdc" || ents[1].Phase != "discover" {
		t.Fatalf("phases=%q,%q", ents[0].Phase, ents[1].Phase)
	}
}

func TestHandleSamsungLogsLocal(t *testing.T) {
	h := buildTestHub(t)
	prev := samsungHandshakeLog
	samsungHandshakeLog = NewSamsungLogBuffer(20)
	t.Cleanup(func() { samsungHandshakeLog = prev })
	samsungHandshakeLog.Add("mdc", "192.168.1.108", "mdc banner ok ip=192.168.1.108")

	rec := httptest.NewRecorder()
	h.handleSamsungLogs(rec, httptest.NewRequest(http.MethodGet, "/api/samsung/logs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Entries []SamsungLogEntry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 1 || out.Entries[0].Phase != "mdc" {
		t.Fatalf("got %#v", out.Entries)
	}
}
