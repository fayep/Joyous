package main

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"
)

// The hub's single-page UI, embedded from web/:
//
//	web/index.html   page markup; references the assets below with ?v=<revision>
//	web/app.css      styles
//	web/js/NN-*.js   the main script, one file per area, served concatenated in
//	                 filename order as /ui/app.js — one script, exactly as when it
//	                 was inline, so top-level code can still call functions
//	                 declared further down (hoisting is per script)
//	web/crop.js      the crop editor (its own script, after the crop modal markup)
//
// Every "__JOYOUS_UI_REVISION__" is replaced with uiRevision at serve time.
//
//go:embed web
var webFS embed.FS

type uiAsset struct {
	body        string
	contentType string
}

// uiIndexHTML and uiAssets are built at package init (before any init func,
// so ui_revision.go's init can hash them).
var uiIndexHTML, uiAssets = loadUIAssets()

func loadUIAssets() (string, map[string]uiAsset) {
	read := func(name string) string {
		b, err := webFS.ReadFile(name)
		if err != nil {
			panic(fmt.Sprintf("embedded UI: %v", err))
		}
		return string(b)
	}
	js, err := fs.Glob(webFS, "web/js/*.js")
	if err != nil || len(js) == 0 {
		panic(fmt.Sprintf("embedded UI: no web/js/*.js (%v)", err))
	}
	sort.Strings(js)
	var app strings.Builder
	for _, name := range js {
		app.WriteString(read(name))
	}
	const jsType, cssType = "text/javascript; charset=utf-8", "text/css; charset=utf-8"
	return read("web/index.html"), map[string]uiAsset{
		"app.js":  {app.String(), jsType},
		"crop.js": {read("web/crop.js"), jsType},
		"app.css": {read("web/app.css"), cssType},
	}
}

// uiContentForRevision is everything the UI revision hashes, in a fixed order.
func uiContentForRevision() string {
	names := make([]string, 0, len(uiAssets))
	for n := range uiAssets {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(uiIndexHTML)
	for _, n := range names {
		b.WriteString("\x00" + n + "\x00" + uiAssets[n].body)
	}
	return b.String()
}

// handleUIAsset serves GET /ui/{file}.
func (h *Hub) handleUIAsset(w http.ResponseWriter, r *http.Request) {
	a, ok := uiAssets[path.Base(r.PathValue("file"))]
	if !ok {
		http.NotFound(w, r)
		return
	}
	etag := `"` + uiRevision + `"`
	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write([]byte(strings.ReplaceAll(a.body, uiRevisionPlaceholder, uiRevision)))
}
