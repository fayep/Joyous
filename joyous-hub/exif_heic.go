//go:build cgo

package main

import (
	"bytes"

	"github.com/jdeng/goheif"
	"github.com/jdeng/goheif/heif"
)

func exifOrientationFromHEIC(data []byte) int {
	raw, err := goheif.ExtractExif(bytes.NewReader(data))
	if err == nil {
		return parseExifOrientation(raw)
	}
	return heicIrotToExif(data)
}

// heicIrotToExif maps HEIF irot quarter-turns (CCW) to EXIF orientation values.
func heicIrotToExif(data []byte) int {
	hf := heif.Open(bytes.NewReader(data))
	it, err := hf.PrimaryItem()
	if err != nil {
		return 1
	}
	switch it.Rotations() {
	case 1:
		return 8
	case 2:
		return 3
	case 3:
		return 6
	default:
		return 1
	}
}
