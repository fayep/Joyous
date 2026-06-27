package main

import (
	"image"
	"image/color"
	"image/draw"
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

func overlayPanelWidthFor(cfg OverlayConfig, weather WeatherSnapshot) int {
	large, medium, small := overlayFacesStandard()
	maxTextW := 0
	measure := func(face font.Face, text string) {
		if face == nil || text == "" {
			return
		}
		if w := font.MeasureString(face, text).Ceil(); w > maxTextW {
			maxTextW = w
		}
	}
	if cfg.ShowCity && weather.City != "" {
		measure(small, weather.City)
	}
	if cfg.ShowDate {
		measure(large, formatOverlayDate(weather.DisplayDate, cfg.DateStyle))
	}
	if cfg.ShowTemp {
		measure(large, formatOverlayTemp(weather.TempC, cfg.UseFahrenheit))
	}
	if cfg.ShowCondition && weather.Condition != "" {
		measure(medium, weather.Condition)
	}
	return maxTextW + 2*overlayPadMin
}

func drawWeatherOverlay(src image.Image, cfg OverlayConfig, weather WeatherSnapshot, portrait bool) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if overlayLayoutForSize(w, h) == overlayLayoutPanel {
		return drawWeatherOverlayPanel(src, cfg, weather)
	}
	if portrait {
		upright := rotate90(src)
		upright = drawWeatherOverlayBar(upright, cfg, weather)
		return rotate90CCW(upright)
	}
	return drawWeatherOverlayBar(src, cfg, weather)
}

func drawWeatherOverlayBar(src image.Image, cfg OverlayConfig, weather WeatherSnapshot) image.Image {
	dst := imageToRGBA(src)
	b := dst.Bounds()
	w, h := b.Dx(), b.Dy()
	large, medium, small := overlayFacesStandard()

	barH := overlayBarHeight(h)
	y0 := b.Max.Y - barH
	drawVerticalGradientBar(dst, b.Min.X, y0, b.Max.X, b.Max.Y)

	pad := overlayPadForWidth(w)
	x := b.Min.X + pad
	y := y0 + pad/2

	if cfg.ShowCity && weather.City != "" {
		drawOverlayText(dst, weather.City, x, y, small, color.RGBA{220, 220, 220, 255})
		y += overlayLineStep
	}
	if cfg.ShowDate {
		drawOverlayText(dst, formatOverlayDate(weather.DisplayDate, cfg.DateStyle), x, y, large, color.RGBA{255, 255, 255, 255})
		y += overlayDateStep
	}
	rightX := b.Max.X - pad
	if cfg.ShowTemp {
		temp := formatOverlayTemp(weather.TempC, cfg.UseFahrenheit)
		drawOverlayTextRight(dst, temp, rightX, y0+pad/2, large, color.RGBA{255, 255, 255, 255})
	}
	if cfg.ShowCondition && weather.Condition != "" {
		drawOverlayTextRight(dst, weather.Condition, rightX, y0+pad/2+overlayDateStep, medium, color.RGBA{230, 230, 230, 255})
	}
	return dst
}

func drawWeatherOverlayPanel(src image.Image, cfg OverlayConfig, weather WeatherSnapshot) image.Image {
	dst := imageToRGBA(src)
	b := dst.Bounds()
	w, h := b.Dx(), b.Dy()
	large, medium, small := overlayFacesStandard()

	marginX := overlayPadForWidth(w)
	marginY := overlayPadForWidth(h)
	if marginY < overlayPadMin {
		marginY = overlayPadMin
	}
	pad := overlayPadMin
	panelW := overlayPanelWidthFor(cfg, weather)
	panelH := overlayPanelHeightFor(cfg, weather)

	x0 := b.Min.X + marginX
	y0 := b.Max.Y - marginY - panelH
	x1 := x0 + panelW
	y1 := b.Max.Y - marginY
	drawSolidPanel(dst, x0, y0, x1, y1, 210)

	// InkJoy left column, then temp/condition from the right side stacked below.
	x := x0 + pad
	y := y0 + pad
	if cfg.ShowCity && weather.City != "" {
		drawOverlayText(dst, weather.City, x, y, small, color.RGBA{220, 220, 220, 255})
		y += overlayLineStep
	}
	if cfg.ShowDate {
		drawOverlayText(dst, formatOverlayDate(weather.DisplayDate, cfg.DateStyle), x, y, large, color.RGBA{255, 255, 255, 255})
		y += overlayDateStep
	}
	if cfg.ShowTemp {
		drawOverlayText(dst, formatOverlayTemp(weather.TempC, cfg.UseFahrenheit), x, y, large, color.RGBA{255, 255, 255, 255})
		y += overlayDateStep
	}
	if cfg.ShowCondition && weather.Condition != "" {
		drawOverlayText(dst, weather.Condition, x, y, medium, color.RGBA{230, 230, 230, 255})
	}
	return dst
}

func imageToRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, src, b.Min, draw.Src)
	return dst
}

func drawVerticalGradientBar(dst *image.RGBA, x0, y0, x1, y1 int) {
	for y := y0; y < y1; y++ {
		t := float64(y-y0) / float64(y1-y0)
		alpha := uint8(140 + t*100)
		if alpha > 235 {
			alpha = 235
		}
		c := color.RGBA{20, 22, 28, alpha}
		for x := x0; x < x1; x++ {
			blendPixel(dst, x, y, c)
		}
	}
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
