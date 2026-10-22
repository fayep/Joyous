package main

import (
	"image"
	"math"
)

// ApplyInkAffinityRay is the 7.09″ high-DPI path: find faces, leave the
// shadow half alone, and pull blown highlights down into that same tone.
// Off-mask pixels are unchanged — no sky-blue dust, no landscape posterize.
func ApplyInkAffinityRay(img image.Image, displayPalette [][3]float64, strength float64, portraitEnhance bool) image.Image {
	if strength <= 0 {
		return img
	}
	_ = displayPalette
	_ = portraitEnhance
	lab := newLabGrid(img)
	field := buildRayFieldLAB(lab)
	mask := buildRayFaceMaskLAB(lab, field)
	guide := buildRayLGuideLAB(lab, field, mask)
	return labMapImage(img, func(x, y int, p *[3]float64) bool {
		lab := *p
		w := mask.weight(x, y)
		if w > strength {
			w = strength
		}
		// Shadow half is already right — do not recolor it. Only the
		// blown half (L* above the face band) is pulled down.
		if w > 0.02 && lab[0] > 58 {
			inkLab := skinRayTarget(lab, [3]float64{}, [3]float64{}, guide)
			p[0] = lab[0] + w*(inkLab[0]-lab[0])
			p[1] = lab[1] + w*(inkLab[1]-lab[1])
			p[2] = lab[2] + w*(inkLab[2]-lab[2])
		}
		return true
	})
}

func inkAffinityRayTargetLAB(lab [3]float64, pal [][3]float64, guide rayLGuide, role rayRole) [3]float64 {
	k, w, y, r, bl, g := pal[0], pal[1], pal[2], pal[3], pal[4], pal[5]
	switch role {
	case rayRoleFace:
		if isFaceSkinLAB(lab) {
			return skinRayTarget(lab, r, y, guide)
		}
		// Face-field but not skin (hair, teeth, a shirt). Keep L*; never sky-dust.
		if math.Hypot(lab[1], lab[2]) < 8 {
			return projectOntoRayPreserveL(lab, k, w)
		}
		return nearestKWRayPreserveL(lab, pal)
	case rayRoleSky:
		if isPaleSkyLAB(lab) || (lab[0] >= 68 && lab[1] < 6) {
			return paleSkyRayTarget(lab, bl)
		}
		// Warm cloud / haze: keep L*. Do not run the face merge even if a* looks peach.
		return nearestKWRayPreserveL(lab, pal)
	}
	if isPaleSkyLAB(lab) {
		return paleSkyRayTarget(lab, bl)
	}
	if math.Hypot(lab[1], lab[2]) < 8 {
		return projectOntoRayPreserveL(lab, k, w)
	}
	if isFoliageLAB(lab) {
		return projectOntoRayPreserveL(lab, g, k)
	}
	return nearestKWRayPreserveL(lab, pal)
}

type rayRole uint8

const (
	rayRoleOther rayRole = iota
	rayRoleSky
	rayRoleFace
)

type rayField struct {
	minX, minY int
	cell       int
	gw, gh     int
	role       []rayRole
}

func (f rayField) at(x, y int) rayRole {
	if f.gw == 0 || f.cell <= 0 || len(f.role) == 0 {
		return rayRoleOther
	}
	cx := (x - f.minX) / f.cell
	cy := (y - f.minY) / f.cell
	if cx < 0 {
		cx = 0
	} else if cx >= f.gw {
		cx = f.gw - 1
	}
	if cy < 0 {
		cy = 0
	} else if cy >= f.gh {
		cy = f.gh - 1
	}
	return f.role[cy*f.gw+cx]
}

type rayCell struct {
	l, a, b       float64
	n             int
	skinMid       bool
	skinAny       bool
	bright        bool
	pale          bool
	warmHighlight bool
}

func buildRayField(img image.Image) rayField { return buildRayFieldLAB(newLabGrid(img)) }

// buildRayField labels coarse cells as sky-field vs face-field. Large connected
// bright regions are atmosphere even when their LAB overlaps skin. Compact
// midtone skin clusters are faces; warm highlights touching those clusters
// (foreheads) join the face, but the same tone in the middle of a cloud does not.
func buildRayFieldLAB(g *labGrid) rayField {
	b := g.rect
	w, h := b.Dx(), b.Dy()
	if w < 1 || h < 1 {
		return rayField{}
	}
	short := w
	if h < short {
		short = h
	}
	cell := short / 40
	if cell < 8 {
		cell = 8
	}
	if cell > short {
		cell = short
	}
	gw := (w + cell - 1) / cell
	gh := (h + cell - 1) / cell
	cells := make([]rayCell, gw*gh)
	step := 1
	if cell >= 16 {
		step = 2
	}
	for y := b.Min.Y; y < b.Max.Y; y += step {
		cy := (y - b.Min.Y) / cell
		for x := b.Min.X; x < b.Max.X; x += step {
			cx := (x - b.Min.X) / cell
			lab := g.at(x, y)
			i := cy*gw + cx
			c := &cells[i]
			c.l += lab[0]
			c.a += lab[1]
			c.b += lab[2]
			c.n++
		}
	}
	for i := range cells {
		c := &cells[i]
		if c.n == 0 {
			continue
		}
		inv := 1 / float64(c.n)
		c.l *= inv
		c.a *= inv
		c.b *= inv
		mean := [3]float64{c.l, c.a, c.b}
		c.skinAny = isSkinRayLAB(mean)
		c.skinMid = c.skinAny && c.l < 72 && math.Hypot(c.a, c.b) >= 8
		c.bright = c.l >= 70
		c.pale = isPaleSkyLAB(mean)
		c.warmHighlight = c.skinAny && c.l >= 70
	}

	roles := make([]rayRole, gw*gh)
	minSky := 8
	if n := gw * gh; n*6/100 > minSky {
		minSky = n * 6 / 100
	}
	skySeed := make([]bool, gw*gh)
	for i, c := range cells {
		skySeed[i] = c.n > 0 && c.bright
	}
	labelConnected(gw, gh, skySeed, minSky, func(idx int) {
		roles[idx] = rayRoleSky
	})

	faceSeed := make([]bool, gw*gh)
	for i, c := range cells {
		faceSeed[i] = c.skinMid
	}
	labelConnected(gw, gh, faceSeed, 1, func(idx int) {
		roles[idx] = rayRoleFace
	})

	// Foreheads sit on the face/sky border and look like warm cloud. Claim
	// warm highlights that touch a face; do not claim pale/cool atmosphere.
	dilateFaceIntoHighlights(roles, cells, gw, gh, 2)

	if !hasRole(roles, rayRoleFace) && !hasRole(roles, rayRoleSky) {
		for i, c := range cells {
			if c.skinAny {
				roles[i] = rayRoleFace
			} else if c.pale {
				roles[i] = rayRoleSky
			}
		}
	} else if !hasRole(roles, rayRoleSky) {
		for i, c := range cells {
			if c.pale && roles[i] != rayRoleFace {
				roles[i] = rayRoleSky
			}
		}
	}

	return rayField{minX: b.Min.X, minY: b.Min.Y, cell: cell, gw: gw, gh: gh, role: roles}
}

type rayMask struct {
	minX, minY, w, h int
	wt               []float32
}

func (m rayMask) at(x, y int) bool {
	return m.weight(x, y) > 0.08
}

func (m rayMask) weight(x, y int) float64 {
	if m.w == 0 || len(m.wt) == 0 {
		return 0
	}
	xx, yy := x-m.minX, y-m.minY
	if xx < 0 || yy < 0 || xx >= m.w || yy >= m.h {
		return 0
	}
	return float64(m.wt[yy*m.w+xx])
}

func isChalkFaceLAB(lab [3]float64) bool {
	if isPaleSkyLAB(lab) || isFoliageLAB(lab) {
		return false
	}
	L, a := lab[0], lab[1]
	c := math.Hypot(lab[1], lab[2])
	// a* ≥ 1 keeps sunlit grey fabric (a* ≈ 0) from counting as forehead.
	return L >= 58 && L <= 96 && a >= 1 && c < 14
}

func isRayPaleHighlightLAB(lab [3]float64) bool {
	if isPaleSkyLAB(lab) {
		return true
	}
	L, a := lab[0], lab[1]
	c := math.Hypot(lab[1], lab[2])
	return L > 78 && a < 2 && c < 14
}

func isRayFleshSeedLAB(lab [3]float64) bool {
	L, a := lab[0], lab[1]
	c := math.Hypot(lab[1], lab[2])
	if L < 22 || L > 64 || a < 3 || c < 8 || c > 32 {
		return false
	}
	return isSkinRayLAB(lab) || isFaceSkinLAB(lab)
}

func isFaceWalkableLAB(lab [3]float64, role rayRole) bool {
	c := math.Hypot(lab[1], lab[2])
	// Cool blown atmosphere only. A sunlit forehead in a sky cell still has a* ≥ 1.
	if role == rayRoleSky && lab[0] > 78 && lab[1] < 2 && c < 12 {
		return false
	}
	if isPaleSkyLAB(lab) || isFoliageLAB(lab) {
		return false
	}
	if isFaceSkinLAB(lab) || isChalkFaceLAB(lab) {
		return true
	}
	L, a := lab[0], lab[1]
	if L < 22 || L > 90 || a < 1 || c > 28 || c < 6 {
		return false
	}
	return true
}

func buildRayFaceMask(img image.Image, field rayField) rayMask {
	return buildRayFaceMaskLAB(newLabGrid(img), field)
}

// buildRayFaceMask walks the chromatic face from flesh seeds, then grows
// up the same columns onto sunlit forehead chalk. It does not dilate into
// pale sky beside the head or down into a grey jacket.
func buildRayFaceMaskLAB(g *labGrid, field rayField) rayMask {
	b := g.rect
	w, h := b.Dx(), b.Dy()
	if w < 1 || h < 1 {
		return rayMask{}
	}
	m := rayMask{minX: b.Min.X, minY: b.Min.Y, w: w, h: h, wt: make([]float32, w*h)}
	seed := make([]bool, w*h)
	nSeed := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if !isRayFleshSeedLAB(g.at(x, y)) {
				continue
			}
			if field.gw > 0 && field.at(x, y) != rayRoleFace {
				continue
			}
			seed[(y-b.Min.Y)*w+(x-b.Min.X)] = true
			nSeed++
		}
	}
	if nSeed == 0 {
		hasSky := false
		if field.gw > 0 {
			for _, r := range field.role {
				if r == rayRoleSky {
					hasSky = true
					break
				}
			}
		}
		if !hasSky {
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					lab := g.at(x, y)
					if !isSkinRayLAB(lab) && !isFaceSkinLAB(lab) {
						continue
					}
					seed[(y-b.Min.Y)*w+(x-b.Min.X)] = true
					nSeed++
				}
			}
		}
		if nSeed == 0 {
			return m
		}
	}

	short := w
	if h < short {
		short = h
	}
	up := short / 10
	if up < 12 {
		up = 12
	} else if up > 110 {
		up = 110
	}
	down := short / 80
	if down < 3 {
		down = 3
	} else if down > 10 {
		down = 10
	}

	seen := make([]bool, w*h)
	comp := make([]int, 0, 64)
	stack := make([]int, 0, 64)
	minComp := 12
	if w*h < 400 {
		minComp = 1
	} else if w*h > 200000 {
		minComp = 80
	}
	for i, ok := range seed {
		if !ok || seen[i] {
			continue
		}
		comp = comp[:0]
		stack = append(stack[:0], i)
		seen[i] = true
		minX, minY := w, h
		maxX, maxY := 0, 0
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp = append(comp, cur)
			cx, cy := cur%w, cur/w
			if cx < minX {
				minX = cx
			}
			if cy < minY {
				minY = cy
			}
			if cx > maxX {
				maxX = cx
			}
			if cy > maxY {
				maxY = cy
			}
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := cx+d[0], cy+d[1]
				if nx < 0 || ny < 0 || nx >= w || ny >= h {
					continue
				}
				ni := ny*w + nx
				if seen[ni] || !seed[ni] {
					continue
				}
				seen[ni] = true
				stack = append(stack, ni)
			}
		}
		if len(comp) < minComp {
			continue
		}
		var sumL, sumA float64
		for _, idx := range comp {
			lab := g.at(b.Min.X+idx%w, b.Min.Y+idx/w)
			sumL += lab[0]
			sumA += lab[1]
		}
		inv := 1 / float64(len(comp))
		meanL, meanA := sumL*inv, sumA*inv
		if w*h > 400 && (meanA < 5 || meanL < 24 || meanL > 58) {
			continue
		}
		cw, ch := maxX-minX+1, maxY-minY+1
		if ch > cw*3 && ch > 40 {
			continue
		}
		if w >= 800 && cw > w*28/100 {
			continue
		}

		// Walk the chromatic face (shadow + chalk cheek). Do not walk pale
		// haze — that is how sky next to a head got a peach column.
		inside := make([]bool, w*h)
		q := append([]int(nil), comp...)
		for _, idx := range comp {
			inside[idx] = true
		}
		inMinX, inMaxX, inMinY, inMaxY := minX, maxX, minY, maxY
		for qi := 0; qi < len(q); qi++ {
			cur := q[qi]
			cx, cy := cur%w, cur/w
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := cx+d[0], cy+d[1]
				if nx < 0 || ny < 0 || nx >= w || ny >= h {
					continue
				}
				ni := ny*w + nx
				if inside[ni] {
					continue
				}
				lab := g.at(b.Min.X+nx, b.Min.Y+ny)
				if isFoliageLAB(lab) || isRayPaleHighlightLAB(lab) {
					continue
				}
				c := math.Hypot(lab[1], lab[2])
				walk := (isFaceSkinLAB(lab) && c >= 8) ||
					(isChalkFaceLAB(lab) && lab[1] >= 3 && lab[2] >= 0)
				if !walk {
					continue
				}
				inside[ni] = true
				q = append(q, ni)
				if nx < inMinX {
					inMinX = nx
				}
				if nx > inMaxX {
					inMaxX = nx
				}
				if ny < inMinY {
					inMinY = ny
				}
				if ny > inMaxY {
					inMaxY = ny
				}
			}
		}
		colTop := make([]int, w)
		colBot := make([]int, w)
		for x := 0; x < w; x++ {
			colTop[x] = 0xffff
			colBot[x] = -1
		}
		for _, idx := range q {
			sx, sy := idx%w, idx/w
			if sy < colTop[sx] {
				colTop[sx] = sy
			}
			if sy > colBot[sx] {
				colBot[sx] = sy
			}
			if m.wt[idx] < 1 {
				m.wt[idx] = 1
			}
		}
		pad := 8
		x0 := inMinX - pad
		if x0 < 0 {
			x0 = 0
		}
		x1 := inMaxX + pad
		if x1 >= w {
			x1 = w - 1
		}
		for xx := x0; xx <= x1; xx++ {
			if colTop[xx] != 0xffff {
				continue
			}
			left, right := -1, -1
			for x := xx - 1; x >= x0; x-- {
				if colTop[x] != 0xffff {
					left = x
					break
				}
			}
			for x := xx + 1; x <= x1; x++ {
				if colTop[x] != 0xffff {
					right = x
					break
				}
			}
			switch {
			case left >= 0 && right >= 0:
				colTop[xx] = (colTop[left] + colTop[right]) / 2
				colBot[xx] = (colBot[left] + colBot[right]) / 2
			case left >= 0:
				colTop[xx] = colTop[left]
				colBot[xx] = colBot[left]
			case right >= 0:
				colTop[xx] = colTop[right]
				colBot[xx] = colBot[right]
			}
		}
		for xx := x0; xx <= x1; xx++ {
			if colTop[xx] == 0xffff {
				continue
			}
			y0 := colTop[xx] - up
			if y0 < 0 {
				y0 = 0
			}
			feather := 16
			for yy := y0; yy < colTop[xx]; yy++ {
				lab := g.at(b.Min.X+xx, b.Min.Y+yy)
				if lab[0] < 50 || isFoliageLAB(lab) {
					continue
				}
				if !isRayPaleHighlightLAB(lab) && !isChalkFaceLAB(lab) && !isFaceSkinLAB(lab) {
					continue
				}
				dist := colTop[xx] - yy
				if isRayPaleHighlightLAB(lab) && field.at(b.Min.X+xx, b.Min.Y+yy) == rayRoleSky && dist > 70 {
					continue
				}
				cover := float32(1)
				if dist > up-feather {
					cover = 1 - float32(dist-(up-feather))/float32(feather)
				}
				if cover < 0.15 {
					continue
				}
				i := yy*w + xx
				if cover > m.wt[i] {
					m.wt[i] = cover
				}
			}
			if colBot[xx] < 0 {
				continue
			}
			y1 := colBot[xx] + down
			if y1 >= h {
				y1 = h - 1
			}
			for yy := colBot[xx] + 1; yy <= y1; yy++ {
				lab := g.at(b.Min.X+xx, b.Min.Y+yy)
				if isFoliageLAB(lab) || isRayPaleHighlightLAB(lab) {
					continue
				}
				if lab[1] < 1 && math.Hypot(lab[1], lab[2]) < 12 {
					continue
				}
				if !isFaceSkinLAB(lab) && !isChalkFaceLAB(lab) {
					continue
				}
				i := yy*w + xx
				if m.wt[i] < 0.6 {
					m.wt[i] = 0.6
				}
			}
		}
		_ = inMinY
		_ = inMaxY
	}
	return m
}

func hasRole(roles []rayRole, want rayRole) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

func labelConnected(gw, gh int, seed []bool, minSize int, paint func(int)) {
	seen := make([]bool, gw*gh)
	stack := make([]int, 0, 32)
	comp := make([]int, 0, 32)
	for i, ok := range seed {
		if !ok || seen[i] {
			continue
		}
		comp = comp[:0]
		stack = append(stack[:0], i)
		seen[i] = true
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp = append(comp, cur)
			cx, cy := cur%gw, cur/gw
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := cx+d[0], cy+d[1]
				if nx < 0 || ny < 0 || nx >= gw || ny >= gh {
					continue
				}
				ni := ny*gw + nx
				if seen[ni] || !seed[ni] {
					continue
				}
				seen[ni] = true
				stack = append(stack, ni)
			}
		}
		if len(comp) < minSize {
			continue
		}
		for _, idx := range comp {
			paint(idx)
		}
	}
}

func dilateFaceIntoHighlights(roles []rayRole, cells []rayCell, gw, gh, times int) {
	for range times {
		next := append([]rayRole(nil), roles...)
		for i, r := range roles {
			if r == rayRoleFace {
				continue
			}
			c := cells[i]
			chroma := math.Hypot(c.a, c.b)
			// Only claim skin-like highlights. The old low-C catch-all ate grey jackets.
			if c.pale && chroma < 8 {
				continue
			}
			if chroma < 6 {
				continue
			}
			if !c.warmHighlight && !c.skinAny {
				continue
			}
			cx, cy := i%gw, i/gw
			touch := false
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := cx+d[0], cy+d[1]
				if nx < 0 || ny < 0 || nx >= gw || ny >= gh {
					continue
				}
				if roles[ny*gw+nx] == rayRoleFace {
					touch = true
					break
				}
			}
			if touch {
				next[i] = rayRoleFace
			}
		}
		copy(roles, next)
	}
}

// rayLGuide is the important L* band for face-field skin, from the image histogram.
// Highlights above skinHi are eased toward it; rank order is kept so
// faces do not flatten into a two-tone mask.
type rayLGuide struct {
	skinLo, skinMode, skinHi float64
	// midA, midB are the good (shadow / midtone) half of the face.
	midA, midB float64
}

func defaultRayLGuide() rayLGuide {
	return rayLGuide{skinLo: 22, skinMode: 40, skinHi: 50, midA: 10, midB: 8}
}

func buildRayLGuide(img image.Image, field rayField, mask rayMask) rayLGuide {
	return buildRayLGuideLAB(newLabGrid(img), field, mask)
}

func buildRayLGuideLAB(labs *labGrid, field rayField, mask rayMask) rayLGuide {
	g := defaultRayLGuide()
	var hist [101]int
	n := 0
	var sumA, sumB float64
	nMid := 0
	b := labs.rect
	step := 2
	if b.Dx()*b.Dy() < 400 {
		step = 1
	}
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			if mask.w > 0 && mask.weight(x, y) < 0.5 {
				continue
			} else if mask.w == 0 && field.gw > 0 && field.at(x, y) != rayRoleFace {
				continue
			}
			lab := labs.at(x, y)
			if lab[0] < 16 || isFoliageLAB(lab) {
				continue
			}
			if lab[0] >= 28 && lab[0] <= 50 && lab[1] >= 2 && math.Hypot(lab[1], lab[2]) >= 8 {
				sumA += lab[1]
				sumB += lab[2]
				nMid++
			}
			bin := int(lab[0] + 0.5)
			if bin < 0 {
				bin = 0
			} else if bin > 100 {
				bin = 100
			}
			hist[bin]++
			n++
		}
	}
	if n < 8 {
		return g
	}
	var sm [101]int
	for i := range hist {
		sm[i] = hist[i]
		if i > 0 {
			sm[i] += hist[i-1]
		}
		if i < 100 {
			sm[i] += hist[i+1]
		}
	}
	mode, best := 40, 0
	for i := 18; i <= 64; i++ {
		if sm[i] > best {
			best = sm[i]
			mode = i
		}
	}
	g.skinMode = float64(mode)
	acc, p10, p90 := 0, -1, 80
	loN, hiN := n/10, n*9/10
	for i, c := range hist {
		acc += c
		if acc >= loN && p10 < 0 {
			p10 = i
		}
		if acc >= hiN {
			p90 = i
			break
		}
	}
	if p10 < 0 {
		p10 = 20
	}
	g.skinLo = float64(p10)
	g.skinHi = float64(p90)
	if g.skinHi < g.skinLo+12 {
		g.skinHi = math.Min(g.skinLo+12, 92)
	}
	if nMid >= 4 {
		g.midA = sumA / float64(nMid)
		g.midB = sumB / float64(nMid)
	}
	return g
}

func pixelLABAt(img image.Image, x, y int) [3]float64 {
	r8, g8, b8, _ := img.At(x, y).RGBA()
	return srgbToLAB([3]float64{float64(r8>>8) / 255.0, float64(g8>>8) / 255.0, float64(b8>>8) / 255.0})
}

func labToY(L float64) float64 {
	if L <= 8 {
		return L / 903.3
	}
	t := (L + 16) / 116
	return t * t * t
}

func yToLab(Y float64) float64 {
	if Y < 0 {
		Y = 0
	}
	if Y <= 0.008856 {
		return 903.3 * Y
	}
	return 116*math.Cbrt(Y) - 16
}

func labStops(a, b float64) float64 {
	ya, yb := labToY(a), labToY(b)
	if ya < 1e-5 {
		ya = 1e-5
	}
	if yb < 1e-5 {
		yb = 1e-5
	}
	return math.Abs(math.Log2(yb / ya))
}

// mergeSkinL leaves the shadow/midtone half alone and only pulls blown
// highlights down into the same band. Rank above destHi is flattened on
// purpose: that tail is chalk, not shading.
func mergeSkinL(L float64, g rayLGuide) float64 {
	_ = g
	const destHi = 50.0
	if L <= destHi {
		return L
	}
	return destHi
}

// skinRayTarget lowers highlight L* into the face band. The shadow half
// already looks right — leave its chroma. Blown/cool highlights take that
// same midtone chroma instead of going grey, blue, or brick-red.
func skinRayTarget(lab, red, yellow [3]float64, guide rayLGuide) [3]float64 {
	_ = red
	_ = yellow
	L := mergeSkinL(lab[0], guide)
	a, b := lab[1], lab[2]
	if lab[0] > 58 {
		cool := lab[2] < 0 || lab[1] < 2
		if cool {
			a, b = guide.midA, guide.midB
			c := math.Hypot(a, b)
			if c > 16 && c > 0 {
				s := 16 / c
				a, b = a*s, b*s
			}
			if a < 8 {
				a = 8
			}
			if b < 2 {
				b = 2
			}
		}
	}
	return [3]float64{L, a, b}
}

func isSkinRayLAB(lab [3]float64) bool {
	if skinToneWeight(lab) > 0.15 {
		return true
	}
	// Daylight shadows and sky-fill highlights go cool (b* −6 to −10) while
	// staying on the red side of a*. Those used to miss skin and land on the
	// blue–white ray — the “faces got sky treatment” bug on Mum & Dad.
	L, a := lab[0], lab[1]
	c := math.Hypot(lab[1], lab[2])
	return a >= 2 && L >= 18 && L <= 90 && c < 32
}

// isFaceSkinLAB is the merge gate inside a face field. Fair sunlit cheeks sit
// at a* ≈ 1–2 (the global gate needs 2) and khaki grass/clothes sit at hue ~87°.
// Role already separated cloud from face; this only has to keep shirts/grass out.
func isFaceSkinLAB(lab [3]float64) bool {
	if isSkinRayLAB(lab) {
		return true
	}
	L, a, b := lab[0], lab[1], lab[2]
	if L < 18 || L > 90 {
		return false
	}
	c := math.Hypot(a, b)
	// Chalk specular on a cheek: almost white, still in a face cell.
	// a* ≥ 1 so sunlit grey cloth does not count as skin.
	if L >= 58 && a >= 1 && b > -12 && c < 16 {
		return true
	}
	if a < 0 {
		return false
	}
	if c < 4 || c > 34 {
		return false
	}
	deg := math.Mod(math.Atan2(b, a)*180/math.Pi+360, 360)
	if deg <= 80 {
		return true
	}
	// Cool daylight (sky bounce on a cheek): hue in the blues, a* still ≥ 0.
	return b < 2 && (deg >= 250 || deg <= 90)
}

func paleSkyRayTarget(lab, blue [3]float64) [3]float64 {
	// White ink leans green. Aim a little blue on the B–W ray so haze
	// dithers as white + blue dust instead of sage paper.
	hue := math.Atan2(blue[2], blue[1])
	c := math.Hypot(lab[1], lab[2])
	if c < 8 {
		c = 8
	}
	if c > 14 {
		c = 14
	}
	return [3]float64{lab[0], c * math.Cos(hue), c * math.Sin(hue)}
}

func nearestKWRayPreserveL(lab [3]float64, pal [][3]float64) [3]float64 {
	k, w, y, r, bl := pal[0], pal[1], pal[2], pal[3], pal[4]
	type ray struct{ a, b [3]float64 }
	rays := []ray{{r, w}, {y, k}, {y, w}}
	if lab[1] < 2 {
		rays = append(rays, ray{bl, w})
	}
	hue := math.Atan2(lab[2], lab[1])
	best := rays[0]
	bestD := math.MaxFloat64
	for _, ray := range rays {
		mid := [3]float64{
			(ray.a[0] + ray.b[0]) / 2,
			(ray.a[1] + ray.b[1]) / 2,
			(ray.a[2] + ray.b[2]) / 2,
		}
		d := math.Abs(angleDiff(hue, math.Atan2(mid[2], mid[1])))
		if d < bestD {
			bestD = d
			best = ray
		}
	}
	return projectOntoRayPreserveL(lab, best.a, best.b)
}

func projectOntoRayPreserveL(lab, a, b [3]float64) [3]float64 {
	t := 0.5
	if denom := b[0] - a[0]; math.Abs(denom) > 0.5 {
		t = (lab[0] - a[0]) / denom
	}
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return [3]float64{
		lab[0],
		a[1] + t*(b[1]-a[1]),
		a[2] + t*(b[2]-a[2]),
	}
}

func isPaleSkyLAB(lab [3]float64) bool {
	if isSkinRayLAB(lab) {
		return false
	}
	L, a, b := lab[0], lab[1], lab[2]
	c := math.Hypot(a, b)
	// Faces sit at a* > 2. Haze sky on this panel is a* ≈ −2, b* ≈ −4.
	if L < 70 || c > 22 || a >= 2 || b > -2 {
		return false
	}
	deg := math.Mod(math.Atan2(b, a)*180/math.Pi+360, 360)
	if deg >= 200 && deg <= 290 {
		return true
	}
	return c < 12 && L >= 82 && deg >= 180 && deg <= 300
}

func isFoliageLAB(lab [3]float64) bool {
	if skinToneWeight(lab) > 0.15 {
		return false
	}
	L, a, b := lab[0], lab[1], lab[2]
	c := math.Hypot(a, b)
	if a >= 0 || c < 10 || L < 20 || L > 78 {
		return false
	}
	deg := math.Mod(math.Atan2(b, a)*180/math.Pi+360, 360)
	return deg >= 95 && deg <= 170
}

func inkAffinityRayWeight(lab [3]float64, strength float64, portraitEnhance bool, role rayRole) float64 {
	if strength <= 0 {
		return 0
	}
	if role == rayRoleSky || (role != rayRoleFace && isPaleSkyLAB(lab)) {
		t := strength * 0.75
		if t > 1 {
			return 1
		}
		return t
	}
	if role == rayRoleFace && isFaceSkinLAB(lab) {
		t := strength
		if t > 1 {
			return 1
		}
		if t < 0.55 {
			return 0.55
		}
		return t
	}
	w := inkAffinityWeight(lab, strength, false)
	if portraitEnhance && skinToneWeight(lab) > 0 {
		w *= 0.85
	}
	if w < 0 {
		return 0
	}
	if w > 1 {
		return 1
	}
	return w
}

// raySkinForbidsGreen is the remapped-face band: peach at panel-safe L*,
// far enough from green ink that G is only a luminance crutch.
func raySkinForbidsGreen(rgb [3]float64) bool {
	return raySkinForbidsGreenLAB(srgbToLAB(paletteRGB01(rgb)))
}

func raySkinForbidsGreenLAB(lab [3]float64) bool {
	return lab[0] >= 16 && lab[0] <= 72 && lab[1] >= 6 && lab[2] >= 2
}

func shouldApplyInkAffinityRay(pipe ColorPipeline, img image.Image, flatRGB bool) bool {
	if flatRGB || !pipe.LABInkAffinityRayEnabled || pipe.LABInkAffinityRayStrength <= 0 {
		return false
	}
	// The ray pass recolours blown face highlights toward skin; its face mask
	// is colour-only, so on landscapes it latches onto cloud, warm foliage and
	// roofs (orange flecks in the sky). Only run it where people were detected;
	// otherwise StuckiTwoPalette falls through to affinity/mix.
	if !pipe.PeopleLikely {
		return false
	}
	return UniqueColors(img) > 6
}
