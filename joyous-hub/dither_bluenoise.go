package main

import (
	"math"
	"math/rand"
	"sync"
)

// Blue-noise dither mask (void-and-cluster, Ulichney 1993). White noise spreads
// its energy across all spatial frequencies, so threshold/pre-dither jitter adds
// low-frequency luminance clumping ("grain") in smooth regions. Blue noise puts
// its energy at high frequencies, where the eye is least sensitive, so the same
// amount of jitter reads as fine, even texture instead of grain. The tile is
// built once and tiled toroidally; a toroidal shift of a blue-noise tile is
// still blue noise, so per-channel offsets keep the 4 channels decorrelated.

const blueNoiseN = 64

var (
	blueNoiseOnce sync.Once
	blueNoiseTile [blueNoiseN * blueNoiseN]float64 // rank-normalized to [0,1)
)

// Per-channel toroidal offsets (c = 0,1,2 for RGB pre-dither; 3 for threshold).
var blueNoiseChanOff = [4][2]int{{0, 0}, {29, 17}, {11, 43}, {47, 31}}

// blueNoiseSample returns blue-noise in (-1,1) for pixel (x,y), channel c.
func blueNoiseSample(x, y, c int) float64 {
	blueNoiseOnce.Do(buildBlueNoiseTile)
	c &= 3
	ox, oy := blueNoiseChanOff[c][0], blueNoiseChanOff[c][1]
	xx := ((x+ox)%blueNoiseN + blueNoiseN) % blueNoiseN
	yy := ((y+oy)%blueNoiseN + blueNoiseN) % blueNoiseN
	return blueNoiseTile[yy*blueNoiseN+xx]*2 - 1
}

func buildBlueNoiseTile() {
	const N = blueNoiseN
	const sigma = 1.9

	// Gaussian energy kernel (toroidal), truncated at 3σ.
	R := int(math.Ceil(3 * sigma))
	type kent struct {
		dx, dy int
		w      float64
	}
	var kernel []kent
	for dy := -R; dy <= R; dy++ {
		for dx := -R; dx <= R; dx++ {
			d2 := float64(dx*dx + dy*dy)
			if d2 == 0 || d2 > float64(R*R) {
				continue
			}
			kernel = append(kernel, kent{dx, dy, math.Exp(-d2 / (2 * sigma * sigma))})
		}
	}

	energy := make([]float64, N*N)
	pattern := make([]bool, N*N)
	wrap := func(x, y int) int {
		x = ((x % N) + N) % N
		y = ((y % N) + N) % N
		return y*N + x
	}
	add := func(p int) {
		px, py := p%N, p/N
		for _, k := range kernel {
			energy[wrap(px+k.dx, py+k.dy)] += k.w
		}
		pattern[p] = true
	}
	remove := func(p int) {
		px, py := p%N, p/N
		for _, k := range kernel {
			energy[wrap(px+k.dx, py+k.dy)] -= k.w
		}
		pattern[p] = false
	}
	tightestCluster := func() int { // argmax energy among 1s
		best, bestE := -1, math.Inf(-1)
		for i := 0; i < N*N; i++ {
			if pattern[i] && energy[i] > bestE {
				bestE, best = energy[i], i
			}
		}
		return best
	}
	largestVoid := func() int { // argmin energy among 0s
		best, bestE := -1, math.Inf(1)
		for i := 0; i < N*N; i++ {
			if !pattern[i] && energy[i] < bestE {
				bestE, best = energy[i], i
			}
		}
		return best
	}

	// Initial binary pattern: ~1/10 points, deterministic seed.
	rng := rand.New(rand.NewSource(0x5eed1234))
	ones := N * N / 10
	for placed := 0; placed < ones; {
		if p := rng.Intn(N * N); !pattern[p] {
			add(p)
			placed++
		}
	}
	// Spread clusters into voids until stable → a blue-noise initial pattern.
	// Converges well inside the cap; the cap only guards against a cycle.
	for i := 0; i < N*N; i++ {
		c := tightestCluster()
		remove(c)
		v := largestVoid()
		add(v)
		if v == c {
			break
		}
	}

	ibp := append([]bool(nil), pattern...)
	ibpEnergy := append([]float64(nil), energy...)
	dither := make([]int, N*N)

	// Phase 1: remove the IBP's tightest clusters, ranks ones-1 .. 0.
	for rank := ones - 1; rank >= 0; rank-- {
		c := tightestCluster()
		dither[c] = rank
		remove(c)
	}
	// Phase 2+3: from the IBP, fill the largest voids, ranks ones .. N*N-1.
	copy(pattern, ibp)
	copy(energy, ibpEnergy)
	for rank := ones; rank < N*N; rank++ {
		v := largestVoid()
		dither[v] = rank
		add(v)
	}

	for i := 0; i < N*N; i++ {
		blueNoiseTile[i] = (float64(dither[i]) + 0.5) / float64(N*N)
	}
}
