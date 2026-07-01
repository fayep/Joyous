package main

import (
	"joyous-hub/catalog"
)

func metaToCatalog(m ImageMeta) catalog.Image {
	img := catalog.Image{
		ID:              m.ID,
		Name:            m.Name,
		Size:            m.Size,
		Width:           m.Width,
		Height:          m.Height,
		FlatRGB:         m.FlatRGB,
		ChromaBoost:     m.ChromaBoost,
		PeopleLikely:    m.PeopleLikely,
		PeopleAnalyzed:  m.PeopleAnalyzed,
		PeopleDetectVer: m.PeopleDetectVer,
		RelPath:         catalog.DefaultRelPath(m.ID),
		StorageKind:     catalog.StorageHub,
		Tags:            m.Tags,
	}
	img.Orientation = catalog.OrientationFromDimensions(m.Width, m.Height)
	return img
}

func cropsToCatalog(crops map[string]CropRect) map[string]catalog.Crop {
	if len(crops) == 0 {
		return nil
	}
	out := make(map[string]catalog.Crop, len(crops))
	for k, r := range crops {
		out[k] = catalog.Crop{X: r.X, Y: r.Y, W: r.W, H: r.H}
	}
	return out
}

func catalogToMeta(img catalog.Image) ImageMeta {
	m := ImageMeta{
		ID:              img.ID,
		Name:            img.Name,
		Size:            img.Size,
		Width:           img.Width,
		Height:          img.Height,
		FlatRGB:         img.FlatRGB,
		ChromaBoost:     img.ChromaBoost,
		PeopleLikely:    img.PeopleLikely,
		PeopleAnalyzed:  img.PeopleAnalyzed,
		PeopleDetectVer: img.PeopleDetectVer,
		Tags:            img.Tags,
	}
	if len(img.Crops) > 0 {
		m.Crops = make(map[string]CropRect, len(img.Crops))
		for k, c := range img.Crops {
			m.Crops[k] = CropRect{X: c.X, Y: c.Y, W: c.W, H: c.H}
		}
	}
	return m
}
