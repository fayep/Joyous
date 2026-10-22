//go:build !goexperiment.simd

package main

import "math"

func powBatchImpl(dst, src []float64, y float64) {
	for i, x := range src {
		if x > 0 {
			dst[i] = math.Pow(x, y)
		} else {
			dst[i] = 0
		}
	}
}

func cbrtBatchImpl(dst, src []float64) {
	for i, x := range src {
		if x > 0 {
			dst[i] = math.Cbrt(x)
		} else {
			dst[i] = 0
		}
	}
}
