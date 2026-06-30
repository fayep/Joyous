package main

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
)

//go:embed calibration/inkjoy-primaries.png calibration/inkjoy-white.png calibration/inkjoy-green.png calibration/samsung-primaries.png
var calibrationFS embed.FS

const (
	inkjoyCalibrationFile  = "calibration/inkjoy-primaries.png"
	inkjoyWhiteFile        = "calibration/inkjoy-white.png"
	inkjoyGreenFile        = "calibration/inkjoy-green.png"
	samsungCalibrationFile = "calibration/samsung-primaries.png"
	inkjoyCalibrationName  = "color-primaries-1600x1200.png"
	inkjoyWhiteName        = "color-primaries-white-prime-1600x1200.png"
	inkjoyGreenName        = "color-primaries-green-blend-1600x1200.png"
	inkjoyGreenPetalsName  = "color-primaries-green-petals-1600x1200.png"
	inkjoyGreenUniformName   = "color-primaries-green-uniform-248-1600x1200.png"
	inkjoyLoLadderName         = "color-primaries-lo-ladder-1600x1200.png"
	inkjoyBlackUniform248Name  = "color-primaries-black-uniform-248-1600x1200.png"
	samsungCalibrationName   = "color-primaries-2560x1440.png"
)

func isProgrammaticInkJoyCalibration(name string) bool {
	return isLoLadderPrimariesCalibration(name) || isBlackUniform248Calibration(name)
}

func isBlackUniform248Calibration(name string) bool {
	return strings.EqualFold(name, inkjoyBlackUniform248Name)
}

func isLoLadderPrimariesCalibration(name string) bool {
	return strings.EqualFold(name, inkjoyLoLadderName)
}

func inkjoyCalibrationPNG() ([]byte, error) {
	return calibrationFS.ReadFile(inkjoyCalibrationFile)
}

func inkjoyWhitePNG() ([]byte, error) {
	return calibrationFS.ReadFile(inkjoyWhiteFile)
}

func inkjoyGreenPNG() ([]byte, error) {
	return calibrationFS.ReadFile(inkjoyGreenFile)
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

func (s *ImageStore) ensureInkJoyWhiteID() (string, error) {
	imgs, err := s.ListImages()
	if err != nil {
		return "", err
	}
	for _, m := range imgs {
		if m.Name == inkjoyWhiteName {
			return m.ID, nil
		}
	}
	data, err := inkjoyWhitePNG()
	if err != nil {
		return "", err
	}
	return s.Store(bytes.NewReader(data), inkjoyWhiteName)
}

func (s *ImageStore) ensureInkJoyGreenID() (string, error) {
	imgs, err := s.ListImages()
	if err != nil {
		return "", err
	}
	for _, m := range imgs {
		if m.Name == inkjoyGreenName {
			return m.ID, nil
		}
	}
	data, err := inkjoyGreenPNG()
	if err != nil {
		return "", err
	}
	return s.Store(bytes.NewReader(data), inkjoyGreenName)
}

func (s *ImageStore) ensureInkJoyGreenPetalsID() (string, error) {
	imgs, err := s.ListImages()
	if err != nil {
		return "", err
	}
	for _, m := range imgs {
		if m.Name == inkjoyGreenPetalsName {
			return m.ID, nil
		}
	}
	data, err := inkjoyGreenPNG()
	if err != nil {
		return "", err
	}
	return s.Store(bytes.NewReader(data), inkjoyGreenPetalsName)
}

func (s *ImageStore) ensureInkJoyGreenUniformID() (string, error) {
	imgs, err := s.ListImages()
	if err != nil {
		return "", err
	}
	for _, m := range imgs {
		if m.Name == inkjoyGreenUniformName {
			return m.ID, nil
		}
	}
	data, err := inkjoyGreenPNG()
	if err != nil {
		return "", err
	}
	return s.Store(bytes.NewReader(data), inkjoyGreenUniformName)
}

func (s *ImageStore) ensureInkJoyBlackUniform248ID() (string, error) {
	imgs, err := s.ListImages()
	if err != nil {
		return "", err
	}
	for _, m := range imgs {
		if m.Name == inkjoyBlackUniform248Name {
			return m.ID, nil
		}
	}
	data, err := inkjoyBlackUniform248PNG()
	if err != nil {
		return "", err
	}
	return s.Store(bytes.NewReader(data), inkjoyBlackUniform248Name)
}

func inkjoyBlackUniform248PNG() ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, frameW, frameH))
	black := color.RGBA{0, 0, 0, 255}
	for y := range frameH {
		for x := range frameW {
			img.Set(x, y, black)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *ImageStore) ensureInkJoyLoLadderPrimariesID() (string, error) {
	imgs, err := s.ListImages()
	if err != nil {
		return "", err
	}
	for _, m := range imgs {
		if m.Name == inkjoyLoLadderName {
			return m.ID, nil
		}
	}
	data, err := loLadderPrimariesPNG()
	if err != nil {
		return "", err
	}
	return s.Store(bytes.NewReader(data), inkjoyLoLadderName)
}

func loLadderPrimariesPNG() ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, loLadderPrimariesPreviewImage()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func loLadderPrimariesPreviewImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, frameW, frameH))
	black := color.RGBA{0, 0, 0, 255}
	for y := range frameH {
		for x := range frameW {
			img.Set(x, y, black)
		}
	}
	hi, _ := buildLoLadderPrimariesGrids(frameW, frameH, 8, 4, 8, 6)
	for y := range frameH {
		for x := range frameW {
			primary := 0
			for i, hb := range hiBytes {
				if hi[y][x] == hb {
					primary = i
					break
				}
			}
			c := PaletteInkJoySend[primary]
			img.Set(x, y, color.RGBA{
				R: uint8(c[0]),
				G: uint8(c[1]),
				B: uint8(c[2]),
				A: 255,
			})
		}
	}
	return img
}

func calibrationKindValid(kind string) bool {
	return kind == "inkjoy" || kind == "inkjoy-white" || kind == "inkjoy-warm" || kind == "inkjoy-green" || kind == "inkjoy-lo-ladder" || kind == "inkjoy-black-248" || kind == "samsung"
}

func calibrationPNG(kind string) ([]byte, string, error) {
	switch kind {
	case "inkjoy":
		data, err := inkjoyCalibrationPNG()
		return data, "image/png", err
	case "inkjoy-white", "inkjoy-warm":
		data, err := inkjoyWhitePNG()
		return data, "image/png", err
	case "inkjoy-green":
		data, err := inkjoyGreenPNG()
		return data, "image/png", err
	case "inkjoy-lo-ladder":
		data, err := loLadderPrimariesPNG()
		return data, "image/png", err
	case "inkjoy-black-248":
		data, err := inkjoyBlackUniform248PNG()
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
