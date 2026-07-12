package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"joyous-hub/catalog"
)

func (h *Hub) handleTagsList(w http.ResponseWriter, r *http.Request) {
	tags, err := h.images.ListTags()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tags == nil {
		tags = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tags)
}

func (h *Hub) handleAlbumsList(w http.ResponseWriter, r *http.Request) {
	albums, err := h.images.ListAlbums()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if albums == nil {
		albums = []catalog.Album{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(albums)
}

func (h *Hub) handleAlbumsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string         `json:"name"`
		Filter      catalog.Filter `json:"filter"`
		DefaultSort string         `json:"default_sort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	id := newID()
	a, err := h.images.CreateSmartAlbum(id, name, body.Filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.DefaultSort != "" {
		sort := body.DefaultSort
		a, err = h.images.UpdateAlbum(id, nil, nil, &sort)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(a)
}

func (h *Hub) handleAlbumGet(w http.ResponseWriter, r *http.Request, id string) {
	a, err := h.images.GetAlbum(id)
	if err != nil {
		code := http.StatusNotFound
		if !strings.Contains(err.Error(), "not found") {
			code = http.StatusInternalServerError
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a)
}

func (h *Hub) handleAlbumPatch(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Name        *string         `json:"name"`
		Filter      *catalog.Filter `json:"filter"`
		DefaultSort *string         `json:"default_sort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.Name == nil && body.Filter == nil && body.DefaultSort == nil {
		http.Error(w, "name, filter, or default_sort required", http.StatusBadRequest)
		return
	}
	a, err := h.images.UpdateAlbum(id, body.Name, body.Filter, body.DefaultSort)
	if err != nil {
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			code = http.StatusNotFound
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a)
}

func (h *Hub) handleAlbumDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.images.DeleteAlbum(id); err != nil {
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			code = http.StatusNotFound
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (h *Hub) handleAlbumImages(w http.ResponseWriter, r *http.Request, id string) {
	imgs, err := h.images.ListAlbumImages(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if imgs == nil {
		imgs = []ImageMeta{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(imgs)
}

func (h *Hub) handleAlbumCount(w http.ResponseWriter, r *http.Request, id string) {
	n, err := h.images.AlbumImageCount(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"count": n})
}

func (h *Hub) handleAlbumOrder(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Move *struct {
			ID     string `json:"id"`
			Target string `json:"target"`
		} `json:"move"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Move == nil {
		http.Error(w, `{"move":{"id","target"}} required`, http.StatusBadRequest)
		return
	}
	if body.Move.ID == "" || body.Move.Target == "" {
		http.Error(w, "move.id and move.target required", http.StatusBadRequest)
		return
	}
	if err := h.images.MoveAlbumImage(id, body.Move.ID, body.Move.Target); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.publishAlbumReordered(id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func parseOptionalBoolParam(v string) *bool {
	if v == "1" || v == "true" {
		b := true
		return &b
	}
	if v == "0" || v == "false" {
		b := false
		return &b
	}
	return nil
}

func parseImageListFilter(r *http.Request) (albumID string, f catalog.Filter) {
	albumID = r.URL.Query().Get("album_id")
	if albumID == "" {
		albumID = catalog.AlbumAll
	}
	f = catalog.QueryFilterFromParams(
		r.URL.Query()["tag"],
		r.URL.Query()["tag_any"],
		r.URL.Query()["tag_none"],
		r.URL.Query()["format"],
		r.URL.Query()["exclude"],
		r.URL.Query().Get("orientation"),
		parseOptionalBoolParam(r.URL.Query().Get("people_likely")),
		parseOptionalBoolParam(r.URL.Query().Get("no_saved_crops")),
		parseOptionalBoolParam(r.URL.Query().Get("untagged")),
	)
	return albumID, f
}
