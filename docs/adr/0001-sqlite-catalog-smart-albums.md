# ADR 0001: SQLite image catalog, smart albums, and per-album ordering

**Status:** Accepted  
**Date:** 2026-06-20

## Context

Joyous Hub stores image metadata as per-file JSON (`images/{id}.json`) beside raw blobs. The album wall lists all images by scanning that directory. Recent work added drag-and-drop reorder via a doubly-linked list in each meta file plus `album_head.json`, which still materializes full chains and does not scale to filtered views or server-side rotation.

We want:

- **Tags** on images and **filtering** by tag, orientation, and saved crop / aspect format.
- **Smart albums** — named, saved filter queries whose membership updates automatically when metadata changes.
- **Per-album manual order** — the main “All photos” wall and each smart album can have its own drag order.
- **Server-side playlists** — devices rotate through a filtered album (e.g. portrait + tag `bedroom` on a bedroom frame) without scanning hundreds of JSON files per tick.

Image blobs (raw, `.bin` cache, thumbnails) should remain on the filesystem. The hub runs on a local machine (often a Mac mini / Pi); backups and inspection should stay simple.

## Decision

Introduce a **SQLite catalog** (`hub.db` under the hub data directory) as the queryable source of truth for image metadata, tags, derived facets, albums, and per-album order. Keep **files for blobs**.

### Catalog (queryable)

- **`images`** — scalar fields: id, name, size, width, height, orientation, people flags, chroma override, flat_rgb, added_at; optional **`content_hash`** (SHA-256 hex, for future dedup / external refs); **`storage_kind`** (`hub` | `photos_ref`), **`source_provider`**, **`source_asset_id`** (for Apple Photos catalog rows without hub file copies); optional **`rel_path`** for hub-owned blobs (default `images/{id}`).
- **`image_crops`** — `(image_id, format, x, y, w, h)` normalized crop rects (first-class SQL, not buried in JSON).
- **`image_tags`** — `(image_id, tag)` many-to-many, indexed by tag.
- **`image_formats`** — `(image_id, format)` derived from `image_crops` keys and optional native-aspect buckets (e.g. `16:9`, `3:4`); maintained on crop write/delete for filter indexes.
- **`albums`** — `id`, `name`, `kind` (`all` | `smart` | `manual`), `filter_json`, `default_sort` (`album_order` | `added` | `name` | `shuffle`).
- **`album_members`** — explicit membership for `manual` albums only.
- **`album_order`** — `(album_id, image_id, sort_key REAL)` for per-album drag order. **Sparse:** rows exist only for images explicitly positioned in that album; unlisted members use `default_sort` as tiebreaker.

The implicit **“All photos”** album is a row with `kind = all` (fixed id `all`).

### Salvaged from `feature/image-sqlite` (branch deleted)

An earlier prototype (`feature/image-sqlite`, two commits) explored SQLite before smart albums were designed. It is **not** merged; these ideas are carried forward:

1. **`image_crops` table** — store crop rects in SQL (`image_crops`), not in a `meta_json` blob. `image_formats` is a derived index for filtering; crop CRUD updates both.
2. **External source columns** — `storage_kind`, `source_provider`, `source_asset_id` on `images` so the catalog can reference assets outside `data/images/` (e.g. PhotoKit `localIdentifier`) without copying bytes.
3. **Content-hash dedup — deferred** — the prototype deduped uploads by `content_hash` and returned an existing id. **v1 skips upload dedup** (each upload gets a new catalog row). Revisit only for explicit “link existing file” / import-without-copy flows, not silent dedup on every POST.
4. **`RegisterPhotosRef` pattern** — insert a catalog row with `storage_kind = photos_ref` and no hub raw file; raw bytes are read at serve time via a future macOS PhotoKit helper (`ErrPhotosRefRead` until wired). `FindBySourceAsset(provider, asset_id)` prevents duplicate refs on re-import.

Use **`modernc.org/sqlite`** with WAL (as in the prototype); package name **`catalog/`** and DB file **`hub.db`** per this ADR.

### Ordering model

- **Default order** when no `album_order` row: for `all`, sort by `sort_key` if present else `id`; for smart albums, `default_sort` among filter matches (typically inherit global order from `all`).
- **Drag reorder** in an album view: update `sort_key` on affected rows only (fractional indexing / LexoRank-style keys) — O(1) writes per move.
- **Upload** inserts a catalog row; does not touch `album_order` unless appending to a manual album.
- **Delete** cascades tags, formats, and order rows.

### Smart albums

A smart album is a **saved filter**, not a copied ID list. `filter_json` holds criteria (tags all/any/none, orientation, formats, optional booleans). Listing runs SQL against the catalog, then applies per-album `album_order` + tiebreaker.

### Device playlists (follow-on)

Devices gain a `playlist_source` referencing an `album_id` (usually a smart album). A rotation worker calls the same list/ordering code as the UI, advances a cursor, and sends the next image. This ADR does not mandate rotation timing; it requires the catalog API to support efficient filtered, ordered lists.

### Migration

On hub startup (or first run after upgrade):

1. Open/create `hub.db`, apply schema migrations.
2. If catalog empty but `images/*.json` exist, import each meta into SQLite and build tags/formats/orientation.
3. Import legacy `album_order.json` or per-meta `album_prev`/`album_next` / `album_head.json` into `album_order` for `album_id = all`.
4. Continue writing `{id}.json` during transition **or** treat SQLite as canonical and JSON as export-only — implementation plan prefers **SQLite canonical**, JSON kept in sync on write for rollback until migration is stable.

### API surface (target)

- `GET /api/images?tag=&orientation=&format=&album_id=` — filtered list in album order.
- CRUD for `/api/albums` and `/api/albums/{id}/images`, `/api/albums/{id}/count`.
- `PATCH /api/albums/{id}/order` — move one image within an album.
- `PATCH /api/images/{id}` — tags and existing meta patches.

## Consequences

### Positive

- Filtered queries and smart-album counts are indexed O(log n), not O(n) directory scans.
- Tags, smart albums, UI filters, and device rotation share one query engine.
- Per-album order is a natural `(album_id, image_id)` table; no proliferation of pointer fields in JSON.
- Blobs stay easy to back up and inspect; `hub.db` is a single file to include in backups.

### Negative / tradeoffs

- New dependency: `modernc.org/sqlite` or `github.com/mattn/go-sqlite3` (prefer pure Go / modernc for cross-compile simplicity).
- Dual-write or migration period while JSON metas remain on disk.
- Schema migrations must be versioned and tested; corrupt DB requires a rebuild-from-JSON path.
- Fractional `sort_key` keys occasionally need rebalancing if keys get too dense (rare at album scale).

### Out of scope for this ADR

- Apple Photos import bridge ([future-ideas](../future-ideas.md)) — schema hooks (`photos_ref`) are in scope; PhotoKit helper is not.
- Upload content-hash dedup on every POST (see salvaged ideas — explicit import-only if ever added).
- Per-image stickers, post-its, themes on the album wall.
- Moving Samsung/device registry or color config into SQLite (may follow later).
- Automatic rotation timers (designed for, implemented separately).

## Alternatives considered

| Alternative | Why not |
|-------------|---------|
| Per-image JSON only + in-memory filter | O(n) scan per request; painful for device rotation at scale. |
| Inverted tag index file (`tags.json`) | Another file to keep consistent; reinvents a subset of SQLite. |
| Full linked list in every `{id}.json` | Only one global order; many writes on init; awkward for per-album order. |
| Sparse linked list in JSON (`album_next` only when non-default) | Better than full chain, still cannot express per-album order without N×album fields. |
| Central `album_order.json` (full ID list) | Rewrites entire list on every drag; rejected earlier in design. |
| Merge `feature/image-sqlite` branch | Stale base (~15 commits behind `main`); no tags/albums/smart albums; aggressive JSON deletion and upload dedup. Ideas salvaged above; branch deleted. |
| Store images in SQLite | Wrong tool for multi-MB blobs; complicates backup and serving. |
| PostgreSQL / external DB | Overkill for a single-user LAN hub; ops burden on Pi/Mac. |

## References

- Implementation plan: [docs/plans/sqlite-catalog-implementation.md](../plans/sqlite-catalog-implementation.md)
- Album UI brainstorm: [docs/future-ideas.md](../future-ideas.md)
- Supersedes experimental linked-list order in `joyous-hub/album_list.go` (to be removed after migration).
- Prototype branch `feature/image-sqlite` (deleted 2026-06-20): `joyous-hub/imagedb`, `joyous.db`, content-hash dedup — not merged; see **Salvaged** above.
