package main

import (
	"image"
	"image/color"
	"testing"
)

func TestSkinToneWeightDarkAndLight(t *testing.T) {
	dark := srgbToLAB([3]float64{0.35, 0.22, 0.15})
	light := srgbToLAB([3]float64{0.92, 0.78, 0.65})
	if skinToneWeight(dark) < 0.2 {
		t.Fatalf("dark skin weight too low: %v", skinToneWeight(dark))
	}
	if skinToneWeight(light) < 0.2 {
		t.Fatalf("light skin weight too low: %v", skinToneWeight(light))
	}
	if skinToneWeight(srgbToLAB([3]float64{0.2, 0.5, 0.9})) > 0.1 {
		t.Fatal("blue sky should not match skin")
	}
}

func TestApplySkinToneProcessingWarmsShadow(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{80, 55, 45, 255})
	before := srgbToLAB([3]float64{80.0 / 255, 55.0 / 255, 45.0 / 255})
	out := ApplySkinToneProcessing(img, 1.0)
	r8, g8, b8, _ := out.At(0, 0).RGBA()
	after := srgbToLAB([3]float64{float64(r8>>8) / 255, float64(g8>>8) / 255, float64(b8>>8) / 255})
	if after[1] <= before[1]+0.5 {
		t.Fatalf("expected warmer a*, got %v -> %v", before[1], after[1])
	}
	if after[2] >= before[2]-0.2 {
		t.Fatalf("expected less yellow-green b*, got %v -> %v", before[2], after[2])
	}
}
