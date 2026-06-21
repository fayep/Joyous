package main

import (
	"bytes"
	"image"
	"image/color"

	"github.com/rwcarlsen/goexif/exif"
)

func readExifOrientation(data []byte) int {
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 {
		return parseExifOrientation(data)
	}
	if len(data) >= 12 && string(data[4:8]) == "ftyp" {
		return exifOrientationFromHEIC(data)
	}
	return 1
}

func parseExifOrientation(data []byte) int {
	x, err := exif.Decode(bytes.NewReader(data))
	if err != nil {
		return 1
	}
	tag, err := x.Get(exif.Orientation)
	if err != nil {
		return 1
	}
	orient, err := tag.Int(0)
	if err != nil || orient < 1 || orient > 8 {
		return 1
	}
	return orient
}

func applyExifOrientation(img image.Image, orientation int) image.Image {
	switch orientation {
	case 1:
		return img
	case 2:
		return flipHorizontal(img)
	case 3:
		return rotate180(img)
	case 4:
		return flipVertical(img)
	case 5:
		return rotate90(flipHorizontal(img))
	case 6:
		return rotate90(img)
	case 7:
		return rotate270(flipHorizontal(img))
	case 8:
		return rotate270(img)
	default:
		return img
	}
}

func flipHorizontal(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(b.Max.X-1-x+b.Min.X, y-b.Min.Y, img.At(x, y))
		}
	}
	return dst
}

func flipVertical(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(x-b.Min.X, b.Max.Y-1-y+b.Min.Y, img.At(x, y))
		}
	}
	return dst
}

func rotate180(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(b.Max.X-1-x+b.Min.X, b.Max.Y-1-y+b.Min.Y, img.At(x, y))
		}
	}
	return dst
}

// rotate90 rotates the image 90° clockwise.
func rotate90(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(h-1-(y-b.Min.Y), x-b.Min.X, img.At(x, y))
		}
	}
	return dst
}

// rotate270 rotates the image 90° counter-clockwise.
func rotate270(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(y-b.Min.Y, w-1-(x-b.Min.X), img.At(x, y))
		}
	}
	return dst
}

func pixelAt(img image.Image, x, y int) color.RGBA {
	r, g, b, a := img.At(x, y).RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}
