package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"joyous-hub/catalog"
)

func (s *ImageStore) migrateCatalogFromJSON() error {
	if s.cat == nil {
		return nil
	}
	n, err := s.cat.ImageCount()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	entries, err := os.ReadDir(s.rawDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var imported []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		data, err := os.ReadFile(filepath.Join(s.rawDir(), e.Name()))
		if err != nil {
			continue
		}
		var meta ImageMeta
		if json.Unmarshal(data, &meta) != nil {
			log.Printf("catalog migrate: skip corrupt %s", e.Name())
			continue
		}
		if meta.ID == "" {
			meta.ID = id
		}
		if _, err := os.Stat(s.rawPath(meta.ID)); err != nil {
			log.Printf("catalog migrate: skip %s (no raw file)", meta.ID)
			continue
		}
		img := metaToCatalog(meta)
		if img.AddedAt.IsZero() {
			if info, err := os.Stat(s.metaPath(meta.ID)); err == nil {
				img.AddedAt = info.ModTime().UTC()
			} else {
				img.AddedAt = time.Now().UTC()
			}
		}
		img.UpdatedAt = img.AddedAt
		if err := s.cat.UpsertImage(img, cropsToCatalog(meta.Crops)); err != nil {
			return err
		}
		if len(meta.Tags) > 0 {
			_ = s.cat.SetImageTags(meta.ID, meta.Tags)
		}
		imported = append(imported, meta.ID)
	}
	if len(imported) == 0 {
		return s.importLegacyAlbumOrder(nil)
	}
	sort.Strings(imported)
	return s.importLegacyAlbumOrder(imported)
}

func (s *ImageStore) importLegacyAlbumOrder(fallbackIDs []string) error {
	if s.cat == nil {
		return nil
	}
	has, err := s.cat.HasAlbumOrder(catalog.AlbumAll)
	if err != nil || has {
		return err
	}

	// Full list from album_order.json
	if data, err := os.ReadFile(filepath.Join(s.dir, "album_order.json")); err == nil {
		var ids []string
		if json.Unmarshal(data, &ids) == nil && len(ids) > 0 {
			return s.cat.SetAlbumOrder(catalog.AlbumAll, ids)
		}
	}

	// Linked list via album_head.json + album_prev/album_next in JSON sidecars
	headPath := filepath.Join(s.dir, "album_head.json")
	headData, err := os.ReadFile(headPath)
	if err != nil {
		return nil
	}
	var headDoc struct {
		Head string `json:"head"`
	}
	if json.Unmarshal(headData, &headDoc) != nil || headDoc.Head == "" {
		return nil
	}
	byID := make(map[string]legacyAlbumMeta)
	for _, id := range fallbackIDs {
		if m, ok := s.readLegacyAlbumMeta(id); ok {
			byID[id] = m
		}
	}
	// Also load sidecars for link fields if not in fallbackIDs
	entries, _ := os.ReadDir(s.rawDir())
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if _, ok := byID[id]; ok {
			continue
		}
		if m, ok := s.readLegacyAlbumMeta(id); ok {
			byID[id] = m
		}
	}
	var ordered []string
	seen := make(map[string]bool)
	for cur, steps := headDoc.Head, 0; cur != "" && steps < len(byID)+1; steps++ {
		m, ok := byID[cur]
		if !ok {
			break
		}
		ordered = append(ordered, cur)
		seen[cur] = true
		cur = m.AlbumNext
	}
	var rest []string
	for id := range byID {
		if !seen[id] {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	ordered = append(ordered, rest...)
	if len(ordered) > 0 {
		return s.cat.SetAlbumOrder(catalog.AlbumAll, ordered)
	}
	return nil
}

// persistMeta writes ImageMeta to SQLite and the JSON sidecar (dual-write).
func (s *ImageStore) persistMeta(meta ImageMeta) error {
	if s.cat != nil {
		img := metaToCatalog(meta)
		now := time.Now().UTC()
		if img.AddedAt.IsZero() {
			img.AddedAt = now
		}
		img.UpdatedAt = now
		if err := s.cat.UpsertImage(img, cropsToCatalog(meta.Crops)); err != nil {
			return err
		}
		if err := s.cat.SetImageTags(meta.ID, meta.Tags); err != nil {
			return err
		}
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(s.metaPath(meta.ID), b, 0644)
}

type legacyAlbumMeta struct {
	ImageMeta
	AlbumPrev string `json:"album_prev,omitempty"`
	AlbumNext string `json:"album_next,omitempty"`
}

func (s *ImageStore) readLegacyAlbumMeta(id string) (legacyAlbumMeta, bool) {
	data, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return legacyAlbumMeta{}, false
	}
	var m legacyAlbumMeta
	if json.Unmarshal(data, &m) != nil {
		return legacyAlbumMeta{}, false
	}
	if m.ID == "" {
		m.ID = id
	}
	return m, true
}
