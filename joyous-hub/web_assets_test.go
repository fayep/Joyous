package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The page and its assets are served from the embedded FS with every
// revision placeholder filled in, so a deploy busts the browser cache.
func TestUIAssetsServed(t *testing.T) {
	h := buildTestHub(t)
	rec := httptest.NewRecorder()
	h.handleStatic(rec, httptest.NewRequest("GET", "/", nil))
	page := rec.Body.String()
	if rec.Code != 200 || strings.Contains(page, uiRevisionPlaceholder) {
		t.Fatalf("index: %d, placeholder left: %v", rec.Code, strings.Contains(page, uiRevisionPlaceholder))
	}
	for _, ref := range []string{"/ui/app.css?v=", "/ui/app.js?v=", "/ui/crop.js?v="} {
		if !strings.Contains(page, ref+uiRevision) {
			t.Fatalf("index lacks %s%s", ref, uiRevision)
		}
	}
	for file, want := range map[string]string{
		"app.js":  "function connectJoyousEvents",
		"crop.js": "const FORMATS",
		"app.css": "{",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/ui/"+file, nil)
		req.SetPathValue("file", file)
		h.handleUIAsset(rec, req)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) || strings.Contains(rec.Body.String(), uiRevisionPlaceholder) {
			t.Fatalf("%s: %d, has %q: %v", file, rec.Code, want, strings.Contains(rec.Body.String(), want))
		}
		etag := rec.Header().Get("ETag")
		rec = httptest.NewRecorder()
		req.Header.Set("If-None-Match", etag)
		h.handleUIAsset(rec, req)
		if rec.Code != 304 {
			t.Fatalf("%s with matching ETag: %d", file, rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ui/nope.js", nil)
	req.SetPathValue("file", "nope.js")
	h.handleUIAsset(rec, req)
	if rec.Code != 404 {
		t.Fatalf("unknown asset: %d", rec.Code)
	}
}
