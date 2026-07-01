# TODO

## SQLite catalog / smart albums (`feature/sqlite-catalog`)

Follow-ups from self-review ([PR #3](https://github.com/fayep/Joyous/pull/3)).

### UX

- [ ] **Smart album tag editing** — Allow removing tag chips on a saved smart album without clicking “Edit filters” first (same immediacy as **Exclude**).
- [ ] **Filter apply clarity** — On All photos, consider auto-applying tag/orientation changes or clearer “draft vs applied” state (today most fields need **Apply**; Exclude reloads immediately).

### Performance

- [ ] **Fractional album reorder** — `MoveInAlbum` currently rewrites all `sort_key` values 1…n per drag; implement sparse/fractional keys per [ADR 0001](docs/adr/0001-sqlite-catalog-smart-albums.md).
- [ ] **Batch image load** — `ListFilteredImages` does N+1 `GetImage` calls; batch-load rows for large albums (500+ fixture target in plan).

### Ops / migration

- [ ] **Document forced re-migrate** — Note in upgrade guide: delete `hub.db` to re-import from JSON if catalog is empty/corrupt (migration only runs when `ImageCount()==0`).
- [ ] **Live smoke test on hub** — Back up `images/`, deploy to `m1ni`, verify migration, filters, Exclude, reorder, delete; then mark PR #3 ready for review.
- [ ] **CI note** — Pre-existing failures on `main`: `TestServeBinAppliesCrop`, overlay metrics tests, `TestServeBinPreservesStoredBinWipe` (unrelated to catalog work).

### Phase 5 (device playlists)

- [ ] Device `playlist_source` → `album_id` in `devices.json`
- [ ] `NextPlaylistImage(deviceID)` using catalog list + order
- [ ] Rotation worker (interval, sleep windows, existing send paths)
- [ ] Device UI: pick album, interval, shuffle override

See [implementation plan](docs/plans/sqlite-catalog-implementation.md).
