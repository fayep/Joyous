package main

import (
	_ "embed"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

// Caveat (OFL) — same handwriting face as the album grid captions on the hub UI.
//
//go:embed fonts/Caveat-Variable.ttf
var overlayCaveatTTF []byte

var (
	overlayCaveatOnce      sync.Once
	overlayCaveatErr       error
	overlayCaveatFaceCache sync.Map // int(size*10) -> font.Face
)

func initOverlayCaveatFont() {
	overlayCaveatOnce.Do(func() {
		if _, err := opentype.Parse(overlayCaveatTTF); err != nil {
			overlayCaveatErr = err
		}
	})
}

func overlayCaveatFace(size float64) font.Face {
	initOverlayCaveatFont()
	if overlayCaveatErr != nil {
		return nil
	}
	key := int(size * 10)
	if v, ok := overlayCaveatFaceCache.Load(key); ok {
		return v.(font.Face)
	}
	tt, err := opentype.Parse(overlayCaveatTTF)
	if err != nil {
		return nil
	}
	face, err := opentype.NewFace(tt, &opentype.FaceOptions{Size: size, DPI: 72})
	if err != nil {
		return nil
	}
	overlayCaveatFaceCache.Store(key, face)
	return face
}
