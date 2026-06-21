//go:build !cgo

package main

func exifOrientationFromHEIC(data []byte) int { return 1 }
