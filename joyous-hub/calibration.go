package main

import (
	"bytes"
	"embed"
	"fmt"
	"io"
)

//go:embed calibration/inkjoy-primaries.png calibration/samsung-primaries.png
var calibrationFS embed.FS

const (
	inkjoyCalibrationFile   = "calibration/inkjoy-primaries.png"
	samsungCalibrationFile  = "calibration/samsung-primaries.png"
	inkjoyCalibrationName   = "color-primaries-1600x1200.png"
	samsungCalibrationName  = "color-primaries-2560x1440.png"
)

func inkjoyCalibrationPNG() ([]byte, error) {
	return calibrationFS.ReadFile(inkjoyCalibrationFile)
}

func samsungCalibrationPNG() ([]byte, error) {
	return calibrationFS.ReadFile(samsungCalibrationFile)
}

func (s *ImageStore) ensureInkJoyCalibrationID() (string, error) {
	imgs, err := s.ListImages()
	if err != nil {
		return "", err
	}
	for _, m := range imgs {
		if m.Name == inkjoyCalibrationName {
			return m.ID, nil
		}
	}
	data, err := inkjoyCalibrationPNG()
	if err != nil {
		return "", err
	}
	return s.Store(bytes.NewReader(data), inkjoyCalibrationName)
}

func calibrationKindValid(kind string) bool {
	return kind == "inkjoy" || kind == "samsung"
}

func calibrationPNG(kind string) ([]byte, string, error) {
	switch kind {
	case "inkjoy":
		data, err := inkjoyCalibrationPNG()
		return data, "image/png", err
	case "samsung":
		data, err := samsungCalibrationPNG()
		return data, "image/png", err
	default:
		return nil, "", fmt.Errorf("unknown calibration kind %q", kind)
	}
}

func writeCalibrationPNG(w io.Writer, kind string) error {
	data, _, err := calibrationPNG(kind)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
