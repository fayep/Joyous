package main

import (
	"image"
	"image/color"
	"image/draw"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

var (
	overlayFontOnce  sync.Once
	overlayFontTTF   []byte
	overlayFontErr   error
	overlayFaceCache sync.Map // int(size*10) -> font.Face
)

func initOverlayFonts() {
	overlayFontOnce.Do(func() {
		overlayFontTTF = goregular.TTF
		if _, err := opentype.Parse(overlayFontTTF); err != nil {
			overlayFontErr = err
		}
	})
}

func overlayFace(size float64) font.Face {
	initOverlayFonts()
	key := int(size * 10)
	if v, ok := overlayFaceCache.Load(key); ok {
		return v.(font.Face)
	}
	tt, err := opentype.Parse(overlayFontTTF)
	if err != nil {
		return nil
	}
	face, err := opentype.NewFace(tt, &opentype.FaceOptions{Size: size, DPI: 72})
	if err != nil {
		return nil
	}
	overlayFaceCache.Store(key, face)
	return face
}

func overlayFacesStandard() (large, medium, small font.Face) {
	return overlayFace(overlayFontLarge), overlayFace(overlayFontMedium), overlayFace(overlayFontSmall)
}

func drawWeatherOverlay(src image.Image, cfg OverlayConfig, weather WeatherSnapshot, photoName string, portrait bool) image.Image {
	lines, err := overlayRenderedLines(cfg, weather)
	if err != nil {
		lines = nil
	}
	hasBox := len(lines) > 0
	hasCaption := cfg.ShowPhotoName && strings.TrimSpace(photoName) != ""
	if !hasBox && !hasCaption {
		return imageToRGBA(src)
	}
	if portrait {
		upright := imageToRGBA(rotate90(src))
		upright = drawWeatherOverlayOnImage(upright, cfg, lines, photoName)
		return rotate90CCW(upright)
	}
	return drawWeatherOverlayOnImage(imageToRGBA(src), cfg, lines, photoName)
}

func drawWeatherOverlayOnImage(dst *image.RGBA, cfg OverlayConfig, lines []overlayLine, photoName string) *image.RGBA {
	if len(lines) > 0 {
		if overlayWeatherUsesOutline(cfg) {
			dst = drawWeatherOverlayOutlined(dst, lines)
		} else {
			dst = drawWeatherOverlayBox(dst, lines)
		}
	}
	if cfg.ShowPhotoName {
		drawPhotoNameCaption(dst, photoName, cfg.PhotoNamePosition)
	}
	return dst
}

func drawWeatherOverlayOutlined(dst *image.RGBA, lines []overlayLine) *image.RGBA {
	b := dst.Bounds()
	w, h := b.Dx(), b.Dy()
	marginX := overlayPadForWidth(w)
	marginY := overlayPadForWidth(h)
	if marginY < overlayPadMin {
		marginY = overlayPadMin
	}
	x := b.Min.X + marginX
	y := b.Max.Y - marginY - overlayContentHeight(lines)
	for i, ln := range lines {
		if ln.face != nil && ln.text != "" {
			textW := fontMeasureString(ln.face, ln.text)
			ascent := ln.face.Metrics().Ascent.Ceil()
			descent := ln.face.Metrics().Descent.Ceil()
			fill := overlayContrastingColor(dst, x, y, textW, ascent+descent)
			fontSize := float64(overlayLineFontSize(i))
			drawOutlinedOverlayText(dst, ln.text, x, y, ln.face, fill, overlayOutlineColor(fill), fontSize)
		}
		y += overlayLineStepAfter(i)
	}
	return dst
}

func drawWeatherOverlayBox(dst *image.RGBA, lines []overlayLine) *image.RGBA {
	b := dst.Bounds()
	w, h := b.Dx(), b.Dy()

	marginX := overlayPadForWidth(w)
	marginY := overlayPadForWidth(h)
	if marginY < overlayPadMin {
		marginY = overlayPadMin
	}
	pad := overlayPadMin
	boxW, boxH := overlayBoxSize(lines)

	x0 := b.Min.X + marginX
	y0 := b.Max.Y - marginY - boxH
	x1 := x0 + boxW
	y1 := b.Max.Y - marginY
	drawSolidPanel(dst, x0, y0, x1, y1, 210)

	drawOverlayLines(dst, x0+pad, y0+pad, lines)
	return dst
}

func drawOverlayLines(dst *image.RGBA, x, y int, lines []overlayLine) {
	for i, ln := range lines {
		drawOverlayText(dst, ln.text, x, y, ln.face, overlayLineColor(i))
		y += overlayLineStepAfter(i)
	}
}

func overlayLineColor(index int) color.Color {
	switch index {
	case 0:
		return color.RGBA{220, 220, 220, 255}
	case 1:
		return color.RGBA{255, 255, 255, 255}
	default:
		return color.RGBA{230, 230, 230, 255}
	}
}

func imageToRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, src, b.Min, draw.Src)
	return dst
}

func drawSolidPanel(dst *image.RGBA, x0, y0, x1, y1 int, alpha uint8) {
	if alpha == 0 {
		alpha = 210
	}
	c := color.RGBA{20, 22, 28, alpha}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if x < dst.Bounds().Min.X || x >= dst.Bounds().Max.X || y < dst.Bounds().Min.Y || y >= dst.Bounds().Max.Y {
				continue
			}
			blendPixel(dst, x, y, c)
		}
	}
}

func blendPixel(dst *image.RGBA, x, y int, c color.RGBA) {
	i := dst.PixOffset(x, y)
	bgR := dst.Pix[i]
	bgG := dst.Pix[i+1]
	bgB := dst.Pix[i+2]
	a := float64(c.A) / 255
	inv := 1 - a
	dst.Pix[i] = uint8(float64(c.R)*a + float64(bgR)*inv)
	dst.Pix[i+1] = uint8(float64(c.G)*a + float64(bgG)*inv)
	dst.Pix[i+2] = uint8(float64(c.B)*a + float64(bgB)*inv)
	dst.Pix[i+3] = 255
}

func drawPlainOverlayText(dst *image.RGBA, text string, x, y int, face font.Face, col color.Color) {
	if face == nil || text == "" {
		return
	}
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(col),
		Face: face,
		Dot:  fixed.P(x, y+int(face.Metrics().Ascent.Ceil())),
	}
	d.DrawString(text)
}

func overlayOutlineColor(fill color.Color) color.Color {
	r, g, b, _ := fill.RGBA()
	if int(r>>8)+int(g>>8)+int(b>>8) > 128*3 {
		return color.RGBA{0, 0, 0, 255}
	}
	return color.RGBA{255, 255, 255, 255}
}

func overlayTextOutlineStep(fontSize float64) int {
	if fontSize >= 58 {
		return 2
	}
	return 1
}

func drawOutlinedOverlayText(dst *image.RGBA, text string, x, y int, face font.Face, fill, outline color.Color, fontSize float64) {
	if face == nil || text == "" {
		return
	}
	step := overlayTextOutlineStep(fontSize)
	baseY := y + int(face.Metrics().Ascent.Ceil())
	offsets := [][2]int{
		{-step, 0}, {step, 0}, {0, -step}, {0, step},
		{-step, -step}, {-step, step}, {step, -step}, {step, step},
	}
	for _, off := range offsets {
		d := &font.Drawer{
			Dst:  dst,
			Src:  image.NewUniform(outline),
			Face: face,
			Dot:  fixed.P(x+off[0], baseY+off[1]),
		}
		d.DrawString(text)
	}
	drawPlainOverlayText(dst, text, x, y, face, fill)
}

func fontMeasureString(face font.Face, text string) int {
	return font.MeasureString(face, text).Ceil()
}

func drawOverlayText(dst *image.RGBA, text string, x, y int, face font.Face, col color.Color) {
	if face == nil || text == "" {
		return
	}
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(col),
		Face: face,
		Dot:  fixed.P(x, y+int(face.Metrics().Ascent.Ceil())),
	}
	shadow := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(color.RGBA{0, 0, 0, 160}),
		Face: face,
		Dot:  fixed.P(x+2, y+2+int(face.Metrics().Ascent.Ceil())),
	}
	shadow.DrawString(text)
	d.DrawString(text)
}

func drawOverlayTextRight(dst *image.RGBA, text string, rightX, y int, face font.Face, col color.Color) {
	if face == nil || text == "" {
		return
	}
	width := font.MeasureString(face, text).Ceil()
	drawOverlayText(dst, text, rightX-width, y, face, col)
}
