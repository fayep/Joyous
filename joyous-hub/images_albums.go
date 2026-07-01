package main

import (
	"encoding/json"
	"fmt"
	"os"

	"joyous-hub/catalog"
)

func (s *ImageStore) ListTags() ([]string, error) {
	if s.cat == nil {
		return nil, nil
	}
	return s.cat.ListTags()
}

func (s *ImageStore) SetImageTags(id string, tags []string) error {
	if s.cat == nil {
		return fmt.Errorf("catalog not available")
	}
	if _, err := s.readMeta(id); err != nil {
		return err
	}
	if err := s.cat.SetImageTags(id, tags); err != nil {
		return err
	}
	meta, err := s.readMeta(id)
	if err != nil {
		return err
	}
	meta.Tags, _ = s.cat.TagsFor(id)
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(s.metaPath(id), b, 0644)
}

func (s *ImageStore) ListAlbums() ([]catalog.Album, error) {
	if s.cat == nil {
		return nil, nil
	}
	return s.cat.ListAlbums()
}

func (s *ImageStore) GetAlbum(id string) (catalog.Album, error) {
	if s.cat == nil {
		return catalog.Album{}, fmt.Errorf("catalog not available")
	}
	return s.cat.GetAlbum(id)
}

func (s *ImageStore) CreateSmartAlbum(id, name string, filter catalog.Filter) (catalog.Album, error) {
	if s.cat == nil {
		return catalog.Album{}, fmt.Errorf("catalog not available")
	}
	a := catalog.Album{
		ID:           id,
		Name:         name,
		Kind:         "smart",
		FilterJSON:   filter.ToJSON(),
		DefaultSort:  catalog.SortAlbumOrder,
	}
	if err := s.cat.CreateAlbum(a); err != nil {
		return catalog.Album{}, err
	}
	return s.cat.GetAlbum(id)
}

func (s *ImageStore) UpdateAlbum(id string, name *string, filter *catalog.Filter, defaultSort *string) (catalog.Album, error) {
	if s.cat == nil {
		return catalog.Album{}, fmt.Errorf("catalog not available")
	}
	return s.cat.UpdateAlbum(id, name, filter, defaultSort)
}

func (s *ImageStore) DeleteAlbum(id string) error {
	if s.cat == nil {
		return fmt.Errorf("catalog not available")
	}
	return s.cat.DeleteAlbum(id)
}

func (s *ImageStore) AlbumImageCount(id string) (int, error) {
	if s.cat == nil {
		return 0, fmt.Errorf("catalog not available")
	}
	return s.cat.AlbumCount(id)
}

func (s *ImageStore) ListAlbumImages(albumID string) ([]ImageMeta, error) {
	return s.ListImagesQuery(albumID, catalog.Filter{})
}
