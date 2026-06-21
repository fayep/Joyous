//go:build cgo

package main

import _ "github.com/jdeng/goheif" // registers HEIC/HEIF with image.Decode; requires CGO
