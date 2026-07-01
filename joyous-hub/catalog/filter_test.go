package catalog_test

import (
	"testing"
	"time"

	"joyous-hub/catalog"
)

func TestTagsAndFilter(t *testing.T) {
	dir := t.TempDir()
	db, err := catalog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	for _, spec := range []struct {
		id, name, orient string
		tags             []string
	}{
		{"a", "a.jpg", "landscape", []string{"bedroom", "kids"}},
		{"b", "b.jpg", "portrait", []string{"bedroom"}},
		{"c", "c.jpg", "landscape", []string{"vacation"}},
	} {
		if err := db.UpsertImage(catalog.Image{
			ID: spec.id, Name: spec.name, RelPath: catalog.DefaultRelPath(spec.id),
			Orientation: spec.orient, AddedAt: now, UpdatedAt: now,
		}, nil); err != nil {
			t.Fatal(err)
		}
		if err := db.SetImageTags(spec.id, spec.tags); err != nil {
			t.Fatal(err)
		}
	}

	f := catalog.Filter{TagsAll: []string{"bedroom"}}
	ids, err := db.ListFilteredImages(catalog.AlbumAll, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("tags_all bedroom: got %d", len(ids))
	}

	f2 := catalog.Filter{Orientation: "portrait"}
	ids2, err := db.ListFilteredImages(catalog.AlbumAll, f2)
	if err != nil || len(ids2) != 1 || ids2[0].ID != "b" {
		t.Fatalf("portrait filter: %+v err=%v", ids2, err)
	}
}

func TestSmartAlbumCRUD(t *testing.T) {
	dir := t.TempDir()
	db, err := catalog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	if err := db.UpsertImage(catalog.Image{ID: "x", Name: "x.jpg", RelPath: catalog.DefaultRelPath("x"), AddedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	_ = db.SetImageTags("x", []string{"bedroom"})

	filter := catalog.Filter{TagsAll: []string{"bedroom"}}
	if err := db.CreateAlbum(catalog.Album{
		ID: "smart1", Name: "Bedroom", Kind: "smart", FilterJSON: filter.ToJSON(), DefaultSort: catalog.SortAlbumOrder,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	n, err := db.AlbumCount("smart1")
	if err != nil || n != 1 {
		t.Fatalf("count=%d err=%v", n, err)
	}
	imgs, err := db.ListAlbumImages("smart1")
	if err != nil || len(imgs) != 1 || imgs[0].ID != "x" {
		t.Fatalf("images: %+v err=%v", imgs, err)
	}
}

func TestExcludeIDsFilter(t *testing.T) {
	dir := t.TempDir()
	db, err := catalog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	for _, spec := range []struct {
		id, orient string
	}{
		{"p1", "portrait"},
		{"p2", "portrait"},
		{"p3", "portrait"},
		{"land", "landscape"},
	} {
		if err := db.UpsertImage(catalog.Image{
			ID: spec.id, Name: spec.id + ".jpg", RelPath: catalog.DefaultRelPath(spec.id),
			Orientation: spec.orient, AddedAt: now, UpdatedAt: now,
		}, nil); err != nil {
			t.Fatal(err)
		}
	}

	ids, err := db.ListFilteredImages(catalog.AlbumAll, catalog.Filter{
		Orientation: "portrait",
		ExcludeIDs:  []string{"p2", "p3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0].ID != "p1" {
		t.Fatalf("portrait excluding p2,p3: %+v", ids)
	}
}

func TestNoSavedCropsFilter(t *testing.T) {
	dir := t.TempDir()
	db, err := catalog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	if err := db.UpsertImage(catalog.Image{ID: "plain", Name: "plain.jpg", RelPath: catalog.DefaultRelPath("plain"), AddedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertImage(catalog.Image{ID: "framed", Name: "framed.jpg", RelPath: catalog.DefaultRelPath("framed"), AddedAt: now, UpdatedAt: now}, map[string]catalog.Crop{
		"4:3": {X: 0, Y: 0, W: 1, H: 1},
	}); err != nil {
		t.Fatal(err)
	}

	noCrops := true
	ids, err := db.ListFilteredImages(catalog.AlbumAll, catalog.Filter{NoSavedCrops: &noCrops})
	if err != nil || len(ids) != 1 || ids[0].ID != "plain" {
		t.Fatalf("no_saved_crops=true: %+v err=%v", ids, err)
	}

	hasCrops := false
	ids2, err := db.ListFilteredImages(catalog.AlbumAll, catalog.Filter{NoSavedCrops: &hasCrops})
	if err != nil || len(ids2) != 1 || ids2[0].ID != "framed" {
		t.Fatalf("no_saved_crops=false: %+v err=%v", ids2, err)
	}
}

func TestPerAlbumOrderIsolation(t *testing.T) {
	dir := t.TempDir()
	db, err := catalog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	for _, id := range []string{"1", "2", "3"} {
		if err := db.UpsertImage(catalog.Image{ID: id, Name: id + ".jpg", RelPath: catalog.DefaultRelPath(id), AddedAt: now, UpdatedAt: now}, nil); err != nil {
			t.Fatal(err)
		}
	}
	filter := catalog.Filter{TagsAll: []string{"x"}}
	_ = db.CreateAlbum(catalog.Album{ID: "sa", Name: "SA", Kind: "smart", FilterJSON: filter.ToJSON(), DefaultSort: catalog.SortAlbumOrder, CreatedAt: now, UpdatedAt: now})
	_ = db.SetImageTags("1", []string{"x"})
	_ = db.SetImageTags("2", []string{"x"})

	if err := db.MoveInAlbum("sa", "2", "1"); err != nil {
		t.Fatal(err)
	}
	saIDs, _ := db.ListAlbumImageIDs("sa")
	if len(saIDs) != 2 || saIDs[0] != "2" {
		t.Fatalf("smart album order: %v", saIDs)
	}
	allIDs, _ := db.ListAlbumImageIDs(catalog.AlbumAll)
	if allIDs[0] != "1" {
		t.Fatalf("all album order unchanged: %v", allIDs)
	}
}
