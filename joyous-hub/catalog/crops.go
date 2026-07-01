package catalog

import (
	"fmt"
	"time"
)

// SetCrop stores one crop rect and updates image_formats.
func (db *DB) SetCrop(id, format string, c Crop) error {
	if c.W <= 0 || c.H <= 0 {
		return fmt.Errorf("invalid crop dimensions")
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO image_crops (image_id, format, x, y, w, h) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(image_id, format) DO UPDATE SET x=excluded.x, y=excluded.y, w=excluded.w, h=excluded.h`,
		id, format, c.X, c.Y, c.W, c.H)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO image_formats (image_id, format) VALUES (?, ?)
		ON CONFLICT(image_id, format) DO NOTHING`, id, format)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE images SET updated_at = ? WHERE id = ?`, formatTime(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteCrop removes one crop and format row.
func (db *DB) DeleteCrop(id, format string) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM image_crops WHERE image_id = ? AND format = ?`, id, format); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM image_formats WHERE image_id = ? AND format = ?`, id, format); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE images SET updated_at = ? WHERE id = ?`, formatTime(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
