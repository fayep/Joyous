package catalog

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// GetAlbum loads one album row.
func (db *DB) GetAlbum(id string) (Album, error) {
	row := db.sql.QueryRow(`SELECT id, name, kind, filter_json, default_sort, created_at, updated_at FROM albums WHERE id = ?`, id)
	var a Album
	var createdAt, updatedAt string
	err := row.Scan(&a.ID, &a.Name, &a.Kind, &a.FilterJSON, &a.DefaultSort, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return Album{}, fmt.Errorf("album %s not found", id)
	}
	if err != nil {
		return Album{}, err
	}
	a.CreatedAt = parseTime(createdAt)
	a.UpdatedAt = parseTime(updatedAt)
	return a, nil
}

// ListAlbums returns user-defined albums (excludes the implicit all album).
func (db *DB) ListAlbums() ([]Album, error) {
	rows, err := db.sql.Query(`SELECT id, name, kind, filter_json, default_sort, created_at, updated_at
		FROM albums WHERE id != ? ORDER BY name`, AlbumAll)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Album
	for rows.Next() {
		var a Album
		var createdAt, updatedAt string
		if err := rows.Scan(&a.ID, &a.Name, &a.Kind, &a.FilterJSON, &a.DefaultSort, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTime(createdAt)
		a.UpdatedAt = parseTime(updatedAt)
		out = append(out, a)
	}
	return out, rows.Err()
}

// CreateAlbum inserts a smart or manual album.
func (db *DB) CreateAlbum(a Album) error {
	if a.ID == "" {
		return fmt.Errorf("album id required")
	}
	if a.ID == AlbumAll {
		return fmt.Errorf("cannot create reserved album id %q", AlbumAll)
	}
	if a.Kind != "smart" && a.Kind != "manual" {
		return fmt.Errorf("kind must be smart or manual")
	}
	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = now
	}
	if a.DefaultSort == "" {
		a.DefaultSort = SortAlbumOrder
	}
	if a.FilterJSON == "" {
		a.FilterJSON = "{}"
	}
	_, err := db.sql.Exec(`INSERT INTO albums (id, name, kind, filter_json, default_sort, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Kind, a.FilterJSON, a.DefaultSort, formatTime(a.CreatedAt), formatTime(a.UpdatedAt))
	return err
}

// UpdateAlbum patches name, filter, or default_sort.
func (db *DB) UpdateAlbum(id string, name *string, filter *Filter, defaultSort *string) (Album, error) {
	a, err := db.GetAlbum(id)
	if err != nil {
		return Album{}, err
	}
	if id == AlbumAll {
		return Album{}, fmt.Errorf("cannot modify reserved album")
	}
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" {
			return Album{}, fmt.Errorf("name required")
		}
		a.Name = n
	}
	if filter != nil {
		a.FilterJSON = filter.ToJSON()
	}
	if defaultSort != nil {
		a.DefaultSort = *defaultSort
	}
	a.UpdatedAt = time.Now().UTC()
	_, err = db.sql.Exec(`UPDATE albums SET name = ?, filter_json = ?, default_sort = ?, updated_at = ? WHERE id = ?`,
		a.Name, a.FilterJSON, a.DefaultSort, formatTime(a.UpdatedAt), id)
	if err != nil {
		return Album{}, err
	}
	return a, nil
}

// DeleteAlbum removes an album and its order rows (not images).
func (db *DB) DeleteAlbum(id string) error {
	if id == AlbumAll {
		return fmt.Errorf("cannot delete reserved album")
	}
	_, err := db.sql.Exec(`DELETE FROM albums WHERE id = ?`, id)
	return err
}
