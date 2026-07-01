package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Filter is a smart-album or query filter (JSON-serializable).
type Filter struct {
	TagsAll      []string `json:"tags_all,omitempty"`
	TagsAny      []string `json:"tags_any,omitempty"`
	TagsNone     []string `json:"tags_none,omitempty"`
	Orientation  string   `json:"orientation,omitempty"`
	FormatsAny   []string `json:"formats_any,omitempty"`
	PeopleLikely *bool    `json:"people_likely,omitempty"`
}

func (f Filter) normalized() Filter {
	out := f
	out.TagsAll = normTags(f.TagsAll)
	out.TagsAny = normTags(f.TagsAny)
	out.TagsNone = normTags(f.TagsNone)
	out.Orientation = strings.TrimSpace(strings.ToLower(f.Orientation))
	out.FormatsAny = normTags(f.FormatsAny) // format keys like 4:3 kept as-is except trim
	for i, k := range out.FormatsAny {
		out.FormatsAny[i] = strings.TrimSpace(k)
	}
	return out
}

func normTags(in []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, t := range in {
		t = NormalizeTag(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// ParseFilterJSON decodes filter_json from an album row.
func ParseFilterJSON(raw string) (Filter, error) {
	if strings.TrimSpace(raw) == "" || raw == "{}" {
		return Filter{}, nil
	}
	var f Filter
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return Filter{}, err
	}
	return f.normalized(), nil
}

func (f Filter) ToJSON() string {
	return f.toJSON()
}

func (f Filter) toJSON() string {
	b, _ := json.Marshal(f.normalized())
	return string(b)
}

// imageIDsMatchingFilter returns image ids satisfying the filter.
func (db *DB) imageIDsMatchingFilter(f Filter) ([]string, error) {
	f = f.normalized()
	var where []string
	var args []any

	if len(f.TagsAll) > 0 {
		placeholders := strings.Repeat("?,", len(f.TagsAll))
		placeholders = placeholders[:len(placeholders)-1]
		for _, t := range f.TagsAll {
			args = append(args, t)
		}
		where = append(where, fmt.Sprintf(`i.id IN (
			SELECT image_id FROM image_tags WHERE tag IN (%s)
			GROUP BY image_id HAVING COUNT(DISTINCT tag) = %d)`, placeholders, len(f.TagsAll)))
	}
	if len(f.TagsAny) > 0 {
		placeholders := strings.Repeat("?,", len(f.TagsAny))
		placeholders = placeholders[:len(placeholders)-1]
		for _, t := range f.TagsAny {
			args = append(args, t)
		}
		where = append(where, fmt.Sprintf(`i.id IN (SELECT DISTINCT image_id FROM image_tags WHERE tag IN (%s))`, placeholders))
	}
	if len(f.TagsNone) > 0 {
		placeholders := strings.Repeat("?,", len(f.TagsNone))
		placeholders = placeholders[:len(placeholders)-1]
		for _, t := range f.TagsNone {
			args = append(args, t)
		}
		where = append(where, fmt.Sprintf(`i.id NOT IN (SELECT image_id FROM image_tags WHERE tag IN (%s))`, placeholders))
	}
	if f.Orientation != "" {
		where = append(where, `i.orientation = ?`)
		args = append(args, f.Orientation)
	}
	if len(f.FormatsAny) > 0 {
		placeholders := strings.Repeat("?,", len(f.FormatsAny))
		placeholders = placeholders[:len(placeholders)-1]
		for _, fmt := range f.FormatsAny {
			args = append(args, fmt)
		}
		where = append(where, fmt.Sprintf(`i.id IN (SELECT image_id FROM image_formats WHERE format IN (%s))`, placeholders))
	}
	if f.PeopleLikely != nil {
		v := 0
		if *f.PeopleLikely {
			v = 1
		}
		where = append(where, `i.people_likely = ?`)
		args = append(args, v)
	}

	q := `SELECT i.id FROM images i`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY i.added_at, i.id`

	rows, err := db.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AlbumCount returns the number of images matching an album's membership rules.
func (db *DB) AlbumCount(albumID string) (int, error) {
	ids, err := db.listAlbumMemberIDs(albumID)
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (db *DB) listAlbumMemberIDs(albumID string) ([]string, error) {
	if albumID == "" {
		albumID = AlbumAll
	}
	album, err := db.GetAlbum(albumID)
	if err != nil {
		return nil, err
	}
	switch album.Kind {
	case "all":
		f := Filter{}
		return db.imageIDsMatchingFilter(f)
	case "smart":
		f, err := ParseFilterJSON(album.FilterJSON)
		if err != nil {
			return nil, err
		}
		return db.imageIDsMatchingFilter(f)
	case "manual":
		rows, err := db.sql.Query(`SELECT image_id FROM album_members WHERE album_id = ? ORDER BY image_id`, albumID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	default:
		return nil, fmt.Errorf("unknown album kind %q", album.Kind)
	}
}

func (db *DB) orderClause(defaultSort string) string {
	switch defaultSort {
	case SortName:
		return `i.name, i.id`
	case SortAdded:
		return `i.added_at, i.id`
	default: // album_order: inherit global all-album order for tiebreaker
		return `g.sort_key IS NULL, g.sort_key, i.added_at, i.id`
	}
}

// listAlbumMemberIDsOrdered returns member ids in album display order.
func (db *DB) listAlbumMemberIDsOrdered(album Album) ([]string, error) {
	memberIDs, err := db.listAlbumMemberIDs(album.ID)
	if err != nil {
		return nil, err
	}
	if len(memberIDs) == 0 {
		return nil, nil
	}
	// Build ordered list via SQL for member subset.
	placeholders := strings.Repeat("?,", len(memberIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(memberIDs)+1)
	args = append(args, album.ID)
	for _, id := range memberIDs {
		args = append(args, id)
	}

	order := `o.sort_key IS NULL, o.sort_key, i.added_at, i.id`
	joinGlobal := ""
	if album.DefaultSort == SortAlbumOrder && album.Kind != "all" {
		joinGlobal = `LEFT JOIN album_order g ON g.album_id = '` + AlbumAll + `' AND g.image_id = i.id`
		order = `o.sort_key IS NULL, o.sort_key, g.sort_key IS NULL, g.sort_key, i.added_at, i.id`
	} else if album.DefaultSort == SortName {
		order = `o.sort_key IS NULL, o.sort_key, i.name, i.id`
	} else if album.DefaultSort == SortAdded {
		order = `o.sort_key IS NULL, o.sort_key, i.added_at, i.id`
	}

	q := fmt.Sprintf(`SELECT i.id FROM images i
		LEFT JOIN album_order o ON o.album_id = ? AND o.image_id = i.id
		%s
		WHERE i.id IN (%s)
		ORDER BY %s`, joinGlobal, placeholders, order)

	rows, err := db.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// QueryFilterFromParams builds a filter from HTTP query parameters.
// Repeated tag= means tags_all; tag_any= for any; tag_none= for exclusion.
func QueryFilterFromParams(tagsAll, tagsAny, tagsNone, formats []string, orientation string, peopleLikely *bool) Filter {
	return Filter{
		TagsAll:      tagsAll,
		TagsAny:      tagsAny,
		TagsNone:     tagsNone,
		Orientation:  orientation,
		FormatsAny:   formats,
		PeopleLikely: peopleLikely,
	}.normalized()
}
