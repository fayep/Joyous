package catalog

import "time"

const (
	SchemaVersion = 1

	AlbumAll = "all"

	StorageHub       = "hub"
	StoragePhotosRef = "photos_ref"

	SortAlbumOrder = "album_order"
	SortAdded      = "added"
	SortName       = "name"
	SortShuffle    = "shuffle"
)

// Crop is a normalized (0–1) rectangle within the source image.
type Crop struct {
	X, Y, W, H float64
}

// Image is a catalog row with optional crops (populated by Get/List).
type Image struct {
	ID              string
	Name            string
	Size            int64
	Width           int
	Height          int
	Orientation     string
	FlatRGB         bool
	ChromaBoost     *bool
	PeopleLikely    bool
	PeopleAnalyzed  bool
	PeopleDetectVer int
	ContentHash     string
	StorageKind     string
	SourceProvider  string
	SourceAssetID   string
	RelPath         string
	AddedAt         time.Time
	UpdatedAt       time.Time
	Crops           map[string]Crop
}

// Album is a saved collection (all photos, smart, or manual).
type Album struct {
	ID          string
	Name        string
	Kind        string
	FilterJSON  string
	DefaultSort string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
