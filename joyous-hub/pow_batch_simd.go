//go:build goexperiment.simd

package main

import (
	"math"
	"simd"
)

// powBatchImpl computes exp2(y·log2 x) per lane, 0 where x <= 0. The kernel is
// written as one straight-line loop with constants hoisted: split into helper
// functions, the vector code isn't inlined and every chunk pays call/spill
// overhead (about 2.4x slower than this form).
//
// log2: x = 2^e · m with m in [√½, √2); log(m) = 2·atanh(s), s = (m-1)/(m+1),
// an odd series in s. |s| <= 0.1716, so terms through s^15 leave ~1e-14.
//
// exp2: round t = y·log2 x to n + r, |r| <= ½; 2^n is built directly in the
// exponent field, 2^r = e^(r ln2) is a degree-11 Taylor polynomial (~2e-13).
// t is clamped so 2^n stays a normal double.
func powBatchImpl(dst, src []float64, y float64) {
	n := simd.Float64s{}.Len()
	tail := len(src) % n
	powChunks(dst[:len(src)-tail], src[:len(src)-tail], y)
	if tail > 0 {
		var in, out [8]float64 // >= any vector length the experiment supports
		copy(in[:], src[len(src)-tail:])
		in[tail] = 1
		powChunks(out[:n], in[:n], y)
		copy(dst[len(src)-tail:], out[:tail])
	}
}

// powChunks requires len(src) to be a multiple of the vector length.
func powChunks(dst, src []float64, y float64) {
	b := simd.BroadcastFloat64s
	u := simd.BroadcastUint64s
	var (
		yv       = b(y)
		zero     = simd.Float64s{}
		one      = b(1)
		half     = b(0.5)
		sqrt2    = b(math.Sqrt2)
		expMask  = u(0x7ff)
		mantMask = u(0x000fffffffffffff)
		oneBits  = u(0x3ff0000000000000)
		twoP52   = u(0x4330000000000000)
		eBias    = b(1<<52 + 1023)
		twoOvLn2 = b(2 / math.Ln2)
		lo, hi   = b(-1020), b(1020)
		magic    = b(1.5 * (1 << 52)) // adding it rounds to an integer in the low mantissa bits
		bias     = u(1023)

		l15, l13, l11, l9 = b(1.0 / 15), b(1.0 / 13), b(1.0 / 11), b(1.0 / 9)
		l7, l5, l3        = b(1.0 / 7), b(1.0 / 5), b(1.0 / 3)

		e11, e10, e9, e8 = b(exp2Taylor[11]), b(exp2Taylor[10]), b(exp2Taylor[9]), b(exp2Taylor[8])
		e7, e6, e5, e4   = b(exp2Taylor[7]), b(exp2Taylor[6]), b(exp2Taylor[5]), b(exp2Taylor[4])
		e3, e2, e1       = b(exp2Taylor[3]), b(exp2Taylor[2]), b(exp2Taylor[1])
	)
	n := zero.Len()
	for i := 0; i+n <= len(src); i += n {
		x := simd.LoadFloat64s(src[i:])

		// log2(x)
		bits := x.ToBits()
		m := bits.And(mantMask).Or(oneBits).BitsToFloat64()
		// Integer → float via 2^52: OR the exponent field into 2^52's mantissa,
		// subtract 2^52 and the bias.
		e := bits.ShiftAllRight(52).And(expMask).Or(twoP52).BitsToFloat64().Sub(eBias)
		big := m.Greater(sqrt2)
		m = m.Mul(half).IfElse(big, m)
		e = e.Add(one).IfElse(big, e)
		s := m.Sub(one).Div(m.Add(one))
		s2 := s.Mul(s)
		p := l15.MulAdd(s2, l13).MulAdd(s2, l11).MulAdd(s2, l9).MulAdd(s2, l7).
			MulAdd(s2, l5).MulAdd(s2, l3).MulAdd(s2, one)
		lg := s.Mul(p).MulAdd(twoOvLn2, e)

		// exp2(y·log2 x)
		t := yv.Mul(lg).Max(lo).Min(hi)
		tm := t.Add(magic)
		r := t.Sub(tm.Sub(magic))
		q := e11.MulAdd(r, e10).MulAdd(r, e9).MulAdd(r, e8).MulAdd(r, e7).MulAdd(r, e6).
			MulAdd(r, e5).MulAdd(r, e4).MulAdd(r, e3).MulAdd(r, e2).MulAdd(r, e1).MulAdd(r, one)
		// tm's low bits hold n (two's complement); shifting n+1023 up 52 places
		// drops the magic constant's high bits and leaves exactly 2^n.
		scale := tm.ToBits().Add(bias).ShiftAllLeft(52).BitsToFloat64()

		q.Mul(scale).IfElse(x.Greater(zero), zero).Store(dst[i:])
	}
}

// exp2Taylor[k] = (ln 2)^k / k!
var exp2Taylor = func() (c [12]float64) {
	c[0] = 1
	for k := 1; k < len(c); k++ {
		c[k] = c[k-1] * math.Ln2 / float64(k)
	}
	return
}()

func cbrtBatchImpl(dst, src []float64) { powBatchImpl(dst, src, 1.0/3) }
