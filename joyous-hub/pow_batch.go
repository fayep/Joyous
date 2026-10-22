package main

// powBatch sets dst[i] = src[i]**y for src[i] > 0, and 0 otherwise. It is the
// hot primitive behind the batch sRGB<->LAB conversions (lab_batch.go): with
// GOEXPERIMENT=simd it runs as vector exp2(y*log2 x) (pow_batch_simd.go);
// otherwise it falls back to math.Pow (pow_batch_scalar.go). The two agree to
// ~1e-12 relative, well below anything the 8-bit pipeline can resolve.
func powBatch(dst, src []float64, y float64) {
	powBatchImpl(dst[:len(src)], src, y)
}

// cbrtBatch sets dst[i] = ∛src[i] for src[i] > 0, and 0 otherwise (math.Cbrt
// in the scalar build, the SIMD pow kernel with y = 1/3 otherwise).
func cbrtBatch(dst, src []float64) {
	cbrtBatchImpl(dst[:len(src)], src)
}
