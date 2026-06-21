//go:build !cgo

package main

// HEIC decoding is not available in CGO-disabled builds.
// .heic files are stored as-is; ServeBin will return "unsupported image format".
