package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTagsAPI(t *testing.T) {
	h := buildTestHub(t)
	id, _ := h.images.Store(bytes.NewReader([]byte{1}), "tagged.jpg")

	body := `{"tags":["Bedroom","Kids"]}`
	req := httptest.NewRequest("PATCH", "/api/images/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleImagePatch(rec, req, id)
	if rec.Code != 200 {
		t.Fatalf("patch tags: %d %s", rec.Code, rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	h.handleTagsList(rec2, httptest.NewRequest("GET", "/api/tags", nil))
	var tags []string
	json.NewDecoder(rec2.Body).Decode(&tags)
	if len(tags) != 2 || tags[0] != "bedroom" {
		t.Fatalf("tags list: %v", tags)
	}

	rec3 := httptest.NewRecorder()
	h.handleImages(rec3, httptest.NewRequest("GET", "/api/images?tag=bedroom&tag=kids", nil))
	var imgs []ImageMeta
	json.NewDecoder(rec3.Body).Decode(&imgs)
	if len(imgs) != 1 || imgs[0].ID != id {
		t.Fatalf("filtered images: %+v", imgs)
	}
}

func TestSmartAlbumAPI(t *testing.T) {
	h := buildTestHub(t)
	id, _ := h.images.Store(bytes.NewReader([]byte{1}), "room.jpg")
	_ = h.images.SetImageTags(id, []string{"bedroom"})

	createBody := `{"name":"Bedroom","filter":{"tags_all":["bedroom"]}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/albums", strings.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	h.handleAlbumsCreate(rec, req)
	if rec.Code != 201 {
		t.Fatalf("create album: %d %s", rec.Code, rec.Body.String())
	}
	var album struct {
		ID string `json:"id"`
	}
	json.NewDecoder(rec.Body).Decode(&album)

	rec2 := httptest.NewRecorder()
	h.handleAlbumCount(rec2, httptest.NewRequest("GET", "/api/albums/"+album.ID+"/count", nil), album.ID)
	var count struct {
		Count int `json:"count"`
	}
	json.NewDecoder(rec2.Body).Decode(&count)
	if count.Count != 1 {
		t.Fatalf("count=%d", count.Count)
	}

	rec3 := httptest.NewRecorder()
	h.handleAlbumImages(rec3, httptest.NewRequest("GET", "/api/albums/"+album.ID+"/images", nil), album.ID)
	var imgs []ImageMeta
	json.NewDecoder(rec3.Body).Decode(&imgs)
	if len(imgs) != 1 || imgs[0].ID != id {
		t.Fatalf("album images: %+v", imgs)
	}
}
