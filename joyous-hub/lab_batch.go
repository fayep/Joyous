package main

import (
	"image"
	"runtime"
	"sync"
)

// Row-batch sRGB <-> CIELAB. Same arithmetic as srgbToLAB / labToSRGB, but each
// row's powers go through powBatch (vector exp2·log2 under GOEXPERIMENT=simd),
// and labMapImage runs rows in parallel. Results match the per-pixel functions
// to ~1e-12, so 8-bit outputs are identical (see lab_batch_test.go).

// labScratch holds one worker's per-row buffers.
type labScratch struct {
	in, out [3][]float64
}

func (s *labScratch) grow(n int) {
	for c := range 3 {
		if cap(s.in[c]) < n {
			s.in[c] = make([]float64, n)
			s.out[c] = make([]float64, n)
		}
		s.in[c], s.out[c] = s.in[c][:n], s.out[c][:n]
	}
}

// srgbToLABRow converts sRGB in [0,1] to LAB, element-wise (dst may alias src).
func srgbToLABRow(dst, src [][3]float64, s *labScratch) {
	n := len(src)
	s.grow(n)
	for c := range 3 {
		for i := range n {
			s.in[c][i] = (src[i][c] + 0.055) / 1.055
		}
		powBatch(s.out[c], s.in[c], 2.4)
	}
	// XYZ (D65-normalized) into s.in.
	for i := range n {
		var lin [3]float64
		for c := range 3 {
			if v := src[i][c]; v <= 0.04045 {
				lin[c] = v / 12.92
			} else {
				lin[c] = s.out[c][i]
			}
		}
		for r := range 3 {
			v := 0.0
			for j := range 3 {
				v += rgbToXYZ[r][j] * lin[j]
			}
			s.in[r][i] = v / xyzD65[r]
		}
	}
	for c := range 3 {
		cbrtBatch(s.out[c], s.in[c])
	}
	for i := range n {
		var f [3]float64
		for c := range 3 {
			if v := s.in[c][i]; v > 0.008856 {
				f[c] = s.out[c][i]
			} else {
				f[c] = 7.787*v + 16.0/116
			}
		}
		dst[i] = [3]float64{116*f[1] - 16, 500 * (f[0] - f[1]), 200 * (f[1] - f[2])}
	}
}

// labToSRGBRow converts LAB to sRGB in [0,1], element-wise (dst may alias src).
func labToSRGBRow(dst, src [][3]float64, s *labScratch) {
	n := len(src)
	s.grow(n)
	for i, lab := range src {
		fy := (lab[0] + 16) / 116
		fx := lab[1]/500 + fy
		fz := fy - lab[2]/200
		var xyz [3]float64
		for c, f := range [3]float64{fx, fy, fz} {
			if f > 0.206897 {
				xyz[c] = f * f * f
			} else {
				xyz[c] = (f - 16.0/116) / 7.787
			}
			xyz[c] *= xyzD65[c]
		}
		for r := range 3 {
			v := 0.0
			for j := range 3 {
				v += xyzToRGB[r][j] * xyz[j]
			}
			if v < 0 {
				v = 0
			} else if v > 1 {
				v = 1
			}
			s.in[r][i] = v
		}
	}
	for c := range 3 {
		powBatch(s.out[c], s.in[c], 1.0/2.4)
	}
	for i := range n {
		for c := range 3 {
			v := s.in[c][i]
			if v <= 0.0031308 {
				v = 12.92 * v
			} else {
				v = 1.055*s.out[c][i] - 0.055
			}
			if v < 0 {
				v = 0
			} else if v > 1 {
				v = 1
			}
			dst[i][c] = v
		}
	}
}

// parallelRows calls fn on disjoint [y0,y1) bands covering [0,h), one band per
// worker, and waits.
func parallelRows(h int, fn func(y0, y1 int)) {
	workers := min(runtime.GOMAXPROCS(0), h)
	if workers <= 1 {
		fn(0, h)
		return
	}
	var wg sync.WaitGroup
	for k := range workers {
		y0, y1 := h*k/workers, h*(k+1)/workers
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(y0, y1)
		}()
	}
	wg.Wait()
}

// readRowRGBA fills px (8-bit RGBA) and rgb (in [0,1]) for image row y,
// reading Pix directly for *image.RGBA.
func readRowRGBA(img image.Image, y int, px [][4]uint8, rgb [][3]float64) {
	b := img.Bounds()
	if m, ok := img.(*image.RGBA); ok {
		row := m.Pix[m.PixOffset(b.Min.X, y):]
		for x := range px {
			copy(px[x][:], row[4*x:4*x+4])
		}
	} else {
		for x := range px {
			r8, g8, b8, a8 := img.At(b.Min.X+x, y).RGBA()
			px[x] = [4]uint8{uint8(r8 >> 8), uint8(g8 >> 8), uint8(b8 >> 8), uint8(a8 >> 8)}
		}
	}
	for x, p := range px {
		rgb[x] = [3]float64{float64(p[0]) / 255.0, float64(p[1]) / 255.0, float64(p[2]) / 255.0}
	}
}

// labMapImage passes every pixel's LAB to fn, which edits it in place and
// reports whether it changed; changed pixels are converted back to sRGB,
// unchanged ones keep their original bytes. Rows run in parallel, so fn must
// be safe for concurrent calls (pure in x, y and lab).
func labMapImage(img image.Image, fn func(x, y int, lab *[3]float64) bool) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(b)
	w := b.Dx()
	parallelRows(b.Dy(), func(y0, y1 int) {
		var s labScratch
		px := make([][4]uint8, w)
		lab := make([][3]float64, w)
		changed := make([]bool, w)
		for yy := y0; yy < y1; yy++ {
			y := b.Min.Y + yy
			readRowRGBA(img, y, px, lab)
			srgbToLABRow(lab, lab, &s)
			for x := range w {
				changed[x] = fn(b.Min.X+x, y, &lab[x])
			}
			labToSRGBRow(lab, lab, &s)
			row := out.Pix[out.PixOffset(b.Min.X, y):]
			for x := range w {
				p := row[4*x : 4*x+4 : 4*x+4]
				if !changed[x] {
					copy(p, px[x][:])
					continue
				}
				p[0], p[1], p[2], p[3] = floatTo8(lab[x][0]), floatTo8(lab[x][1]), floatTo8(lab[x][2]), px[x][3]
			}
		}
	})
	return out
}

// labRowsParallel calls fn with each row's LAB, rows spread across workers;
// fn must only touch state for its own row.
func labRowsParallel(img image.Image, fn func(y int, lab [][3]float64)) {
	b := img.Bounds()
	parallelRows(b.Dy(), func(y0, y1 int) {
		var s labScratch
		px := make([][4]uint8, b.Dx())
		lab := make([][3]float64, b.Dx())
		for yy := y0; yy < y1; yy++ {
			y := b.Min.Y + yy
			readRowRGBA(img, y, px, lab)
			srgbToLABRow(lab, lab, &s)
			fn(y, lab)
		}
	})
}

// labRows calls fn with each row's LAB, top to bottom, on the calling goroutine.
func labRows(img image.Image, fn func(y int, lab [][3]float64)) {
	b := img.Bounds()
	var s labScratch
	px := make([][4]uint8, b.Dx())
	lab := make([][3]float64, b.Dx())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		readRowRGBA(img, y, px, lab)
		srgbToLABRow(lab, lab, &s)
		fn(y, lab)
	}
}

// labGrid is an image's CIELAB, converted once (batched, in parallel) for
// passes that sample LAB repeatedly, e.g. the ray field/mask/guide builders.
type labGrid struct {
	rect image.Rectangle
	lab  [][3]float64
}

func newLabGrid(img image.Image) *labGrid {
	b := img.Bounds()
	g := &labGrid{rect: b, lab: make([][3]float64, b.Dx()*b.Dy())}
	labRowsParallel(img, func(y int, row [][3]float64) {
		copy(g.lab[(y-b.Min.Y)*b.Dx():], row)
	})
	return g
}

func (g *labGrid) at(x, y int) [3]float64 {
	return g.lab[(y-g.rect.Min.Y)*g.rect.Dx()+(x-g.rect.Min.X)]
}
