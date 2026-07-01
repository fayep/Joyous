package catalog_test

import (
	"testing"
	"time"

	"joyous-hub/catalog"
)

func TestOpenUpsertList(t *testing.T) {
	dir := t.TempDir()
	db, err := catalog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC().Truncate(time.Second)
	img := catalog.Image{
		ID:       "abc",
		Name:     "vacation.jpg",
		Size:     100,
		Width:    800,
		Height:   600,
		RelPath:  catalog.DefaultRelPath("abc"),
		AddedAt:  now,
		UpdatedAt: now,
	}
	if err := db.UpsertImage(img, map[string]catalog.Crop{
		"4:3": {X: 0, Y: 0, W: 1, H: 0.75},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetImage("abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.Orientation != "landscape" || got.Name != "vacation.jpg" {
		t.Fatalf("got %+v", got)
	}
	list, err := db.ListAlbumImages(catalog.AlbumAll)
	if err != nil || len(list) != 1 {
		t.Fatalf("list len=%d err=%v", len(list), err)
	}
}

func TestMoveInAlbum(t *testing.T) {
	dir := t.TempDir()
	db, err := catalog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	for _, id := range []string{"a", "b", "c"} {
		if err := db.UpsertImage(catalog.Image{ID: id, Name: id + ".jpg", RelPath: catalog.DefaultRelPath(id), AddedAt: now, UpdatedAt: now}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.MoveInAlbum(catalog.AlbumAll, "c", "a"); err != nil {
		t.Fatal(err)
	}
	ids, _ := db.ListAlbumImageIDs(catalog.AlbumAll)
	if len(ids) != 3 || ids[0] != "c" || ids[1] != "a" {
		t.Fatalf("order: %v", ids)
	}
}
