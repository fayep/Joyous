package main

import "testing"

// The tile must be a permutation of ranks: every threshold level used once,
// so the noise is uniform over (-1,1) with zero mean (no brightness bias).
func TestBlueNoiseTileIsUniformPermutation(t *testing.T) {
	blueNoiseOnce.Do(buildBlueNoiseTile)
	const n = blueNoiseN * blueNoiseN
	seen := make([]bool, n)
	for _, v := range blueNoiseTile {
		r := int(v*n - 0.5 + 1e-9)
		if r < 0 || r >= n || seen[r] {
			t.Fatalf("value %v is not a unique rank", v)
		}
		seen[r] = true
	}
}

// Blue noise has little low-frequency energy: a box-blurred patch of it varies
// far less than the same blur of white noise with the same distribution.
func TestBlueNoiseHasLessLowFrequencyEnergyThanWhite(t *testing.T) {
	lowFreqVar := func(sample func(x, y, c int) float64) float64 {
		const n, k = 128, 4 // k×k box blur
		var sum, sum2 float64
		cnt := 0
		for y := 0; y+k <= n; y += k {
			for x := 0; x+k <= n; x += k {
				m := 0.0
				for dy := 0; dy < k; dy++ {
					for dx := 0; dx < k; dx++ {
						m += sample(x+dx, y+dy, 3)
					}
				}
				m /= k * k
				sum += m
				sum2 += m * m
				cnt++
			}
		}
		mean := sum / float64(cnt)
		return sum2/float64(cnt) - mean*mean
	}
	b, w := lowFreqVar(blueNoiseSample), lowFreqVar(whiteNoiseSample)
	if b > w/4 {
		t.Fatalf("blue-noise 4x4-blurred variance %.5f not well below white %.5f", b, w)
	}
}
