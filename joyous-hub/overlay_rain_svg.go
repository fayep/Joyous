package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strconv"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

//go:embed assets/rain-cloud.svg
var rainCloudSVGTemplate []byte

const (
	rainCloudViewW = 24
	rainCloudViewH = 31
	// SVG art is authored at stroke-width 2; thicker strokes read better on e-ink.
	rainCloudStrokeWidth       = 3.5
	rainCloudStrokeWidthBorder = 5.0
)

func overlayRainIconHeight(width int) int {
	if width < 1 {
		return 1
	}
	return width * rainCloudViewH / rainCloudViewW
}

func colorHex(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", byte(r>>8), byte(g>>8), byte(b>>8))
}

func renderRainCloudSVG(width int, stroke color.Color, strokeWidth float64) *image.RGBA {
	if width < 1 {
		return nil
	}
	height := overlayRainIconHeight(width)
	svg := bytes.ReplaceAll(rainCloudSVGTemplate, []byte("__STROKE__"), []byte(colorHex(stroke)))
	svg = bytes.Replace(svg, []byte("__STROKE_WIDTH__"), []byte(strconv.FormatFloat(strokeWidth, 'f', -1, 64)), 1)
	icon, err := oksvg.ReadIconStream(bytes.NewReader(svg))
	if err != nil {
		return nil
	}
	icon.SetTarget(0, 0, float64(width), float64(height))
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	scanner := rasterx.NewScannerGV(width, height, rgba, rgba.Bounds())
	raster := rasterx.NewDasher(width, height, scanner)
	icon.Draw(raster, 1.0)
	return rgba
}

func blitRainCloudIcon(dst *image.RGBA, src *image.RGBA, x, y int) {
	if dst == nil || src == nil {
		return
	}
	r := src.Bounds().Add(image.Pt(x, y))
	draw.Draw(dst, r, src, src.Bounds().Min, draw.Over)
}

func drawRainCloudIcon(dst *image.RGBA, x, y, width int, col color.Color, strokeWidth float64) {
	src := renderRainCloudSVG(width, col, strokeWidth)
	blitRainCloudIcon(dst, src, x, y)
}
