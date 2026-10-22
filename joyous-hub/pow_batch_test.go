package main

import (
	"math"
	"math/rand"
	"testing"
)

// powBatch must track math.Pow over the ranges the LAB code feeds it: sRGB
// gamma (x in (0,1], y = 2.4 or 1/2.4) and the LAB cube root (y = 1/3).
func TestPowBatchMatchesMathPow(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	src := make([]float64, 100003) // odd length exercises the partial tail
	for i := range src {
		switch i % 3 {
		case 0:
			src[i] = rng.Float64()
		case 1:
			src[i] = math.Exp(-30 * rng.Float64()) // down to ~1e-13
		default:
			src[i] = 2 * rng.Float64()
		}
	}
	src[0], src[1], src[2] = 1, 0.5, math.SmallestNonzeroFloat64*1e300
	dst := make([]float64, len(src))
	for _, y := range []float64{2.4, 1 / 2.4, 1.0 / 3} {
		powBatch(dst, src, y)
		worst := 0.0
		for i, x := range src {
			want := math.Pow(x, y)
			if rel := math.Abs(dst[i]-want) / want; rel > worst {
				worst = rel
			}
		}
		if worst > 1e-11 {
			t.Errorf("y=%v: worst relative error %.3g, want <= 1e-11", y, worst)
		}
	}
}

func TestCbrtBatchMatchesMathCbrt(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	src := make([]float64, 10001)
	for i := range src {
		src[i] = math.Exp(-20*rng.Float64()) * 2
	}
	dst := make([]float64, len(src))
	cbrtBatch(dst, src)
	for i, x := range src {
		if want := math.Cbrt(x); math.Abs(dst[i]-want) > 1e-12*want {
			t.Fatalf("cbrt(%v) = %v, want %v", x, dst[i], want)
		}
	}
}

func TestPowBatchNonPositiveIsZero(t *testing.T) {
	src := []float64{0, -1, -1e-300, math.Inf(-1), 3}
	dst := make([]float64, len(src))
	powBatch(dst, src, 2.4)
	for i, x := range src[:4] {
		if dst[i] != 0 {
			t.Errorf("pow(%v) = %v, want 0", x, dst[i])
		}
	}
	if math.Abs(dst[4]-math.Pow(3, 2.4)) > 1e-12*dst[4] {
		t.Errorf("pow(3) = %v", dst[4])
	}
}

func benchPowSrc() []float64 {
	rng := rand.New(rand.NewSource(1))
	s := make([]float64, 4096)
	for i := range s {
		s[i] = rng.Float64()
	}
	return s
}

func BenchmarkPowBatch(b *testing.B) {
	src := benchPowSrc()
	dst := make([]float64, len(src))
	b.SetBytes(int64(8 * len(src)))
	for b.Loop() {
		powBatch(dst, src, 2.4)
	}
}

func BenchmarkPowMathLoop(b *testing.B) {
	src := benchPowSrc()
	dst := make([]float64, len(src))
	b.SetBytes(int64(8 * len(src)))
	for b.Loop() {
		for i, x := range src {
			dst[i] = math.Pow(x, 2.4)
		}
	}
}
