package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"joyous-hub/catalog"
)

func TestCatalogMigrateFromJSON(t *testing.T) {
	dir := t.TempDir()
	imagesDir := filepath.Join(dir, "images")
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		t.Fatal(err)
	}
	id := "img001"
	if err := os.WriteFile(filepath.Join(imagesDir, id), []byte{1, 2, 3}, 0644); err != nil {
		t.Fatal(err)
	}
	meta := ImageMeta{
		ID: id, Name: "test.jpg", Size: 3, Width: 100, Height: 80,
		Crops: map[string]CropRect{"4:3": {W: 1, H: 0.75}},
	}
	b, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(imagesDir, id+".json"), b, 0644); err != nil {
		t.Fatal(err)
	}

	store := NewImageStore(dir)
	imgs, err := store.ListImages()
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 || imgs[0].ID != id || imgs[0].Name != "test.jpg" {
		t.Fatalf("list: %+v err=%v", imgs, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hub.db")); err != nil {
		t.Fatal("hub.db should exist")
	}
}

func TestCatalogDualWriteOnStore(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id, err := store.Store(bytes.NewReader([]byte{9, 8, 7}), "dual.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.readMeta(id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.cat.GetImage(id); err != nil {
		t.Fatal(err)
	}
}

func TestMoveAlbumImageCatalog(t *testing.T) {
	store := NewImageStore(t.TempDir())
	id1, _ := store.Store(bytes.NewReader([]byte{1}), "a.jpg")
	_, _ = store.Store(bytes.NewReader([]byte{2}), "b.jpg")
	id3, _ := store.Store(bytes.NewReader([]byte{3}), "c.jpg")

	if err := store.MoveAlbumImage(id3, id1); err != nil {
		t.Fatal(err)
	}
	imgs, _ := store.ListImages()
	if len(imgs) != 3 || imgs[0].ID != id3 || imgs[1].ID != id1 {
		t.Fatalf("order: %v %v %v", imgs[0].ID, imgs[1].ID, imgs[2].ID)
	}
	orderRev, _ := store.cat.AlbumOrderRevision(catalog.AlbumAll)
	if orderRev == "" {
		t.Fatal("expected album order revision")
	}
}
