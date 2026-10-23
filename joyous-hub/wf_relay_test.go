package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func wfTestPush(url string, size int, crc any) []byte {
	b, _ := json.Marshal(map[string]any{
		"action": "wf_update", "msgid": "123",
		"data": map[string]any{
			"url": url, "wf_panel": "T10A01", "wf_batch": "SPR0Y3", "wf_date": "260901",
			"wf_scene": "VD", "wf_crc32": crc, "wf_file_size": size,
		},
	})
	return b
}

func TestRewriteWFUpdateCachesAndServesLocally(t *testing.T) {
	wf := bytes.Repeat([]byte{0xA5, 0x5A, 0x01}, 4000)
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(wf) }))
	defer cloud.Close()

	cache := NewInkJoyCache(t.TempDir())
	h := &Hub{inkjoy: cache, publicHTTPSURL: "https://joyous.example/", serverAddr: "hub.lan:18080"}
	crc := crc32.ChecksumIEEE(wf)
	for _, pushedCRC := range []any{fmt.Sprintf("%08X", crc), crc} {
		out, err := h.rewriteWFUpdate("D0CF13EF4080", wfTestPush(cloud.URL+"/wf/epd.wf", len(wf), pushedCRC))
		if err != nil {
			t.Fatal(err)
		}
		var msg struct {
			Data map[string]any `json:"data"`
		}
		json.Unmarshal(out, &msg)
		got, _ := msg.Data["url"].(string)
		name := fmt.Sprintf("wf-T10A01-SPR0Y3-260901-VD-%08X", crc)
		if want := "https://joyous.example/inkjoy/D0CF13EF4080/" + name + ".wf"; got != want {
			t.Fatalf("url = %q, want %q", got, want)
		}
		if msg.Data["wf_crc32"] == nil || msg.Data["wf_file_size"] == nil {
			t.Fatal("other wf_update fields must be kept")
		}

		// The hub serves the copy at that path.
		mux := http.NewServeMux()
		registerInkJoyCacheRoutes(mux, nil, cache)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", strings.TrimPrefix(got, "https://joyous.example"), nil))
		if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), wf) {
			t.Fatalf("serve: %d, %d bytes", rec.Code, rec.Body.Len())
		}
	}
}

func TestRewriteWFUpdateRejectsWrongSize(t *testing.T) {
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("short")) }))
	defer cloud.Close()
	h := &Hub{inkjoy: NewInkJoyCache(t.TempDir())}
	push := wfTestPush(cloud.URL+"/epd.wf", 9999, "00000000")
	out, err := h.rewriteWFUpdate("D0CF13EF4080", push)
	if err == nil || !bytes.Equal(out, push) {
		t.Fatalf("want error and the original payload, got err=%v", err)
	}
}

func TestRewriteWFUpdateHTTPFallbackURL(t *testing.T) {
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("wave")) }))
	defer cloud.Close()
	h := &Hub{inkjoy: NewInkJoyCache(t.TempDir()), serverAddr: "hub.lan:18080", devices: NewDeviceRegistry(t.TempDir())}
	out, err := h.rewriteWFUpdate("D0CF13EF4080", wfTestPush(cloud.URL+"/epd.wf", 4, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"url":"http://hub.lan:18080/inkjoy/D0CF13EF4080/wf-`) {
		t.Fatalf("no public HTTPS base: want the hub's http URL, got %s", out)
	}
}
