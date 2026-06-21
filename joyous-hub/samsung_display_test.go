package main

import (
	"testing"
)

func TestInferSamsungDisplayEM32(t *testing.T) {
	p := inferSamsungDisplay("Samsung EM32DX", "<modelName>EM32DX</modelName>")
	if p.CropFormat != "16:9" || p.Width != 2560 || p.Height != 1440 {
		t.Fatalf("unexpected profile %+v", p)
	}
}

func TestInferSamsungDisplayFromResolution(t *testing.T) {
	p := inferSamsungDisplay("", "screen 1440x2560 portrait")
	if p.CropFormat != "9:16" || p.Width != 1440 || p.Height != 2560 {
		t.Fatalf("unexpected profile %+v", p)
	}
}

func TestCropFormatForSize(t *testing.T) {
	if got := cropFormatForSize(2560, 1440); got != "16:9" {
		t.Fatalf("got %q", got)
	}
	if got := cropFormatForSize(1440, 2560); got != "9:16" {
		t.Fatalf("got %q", got)
	}
}

func TestConvertToSamsungPNGUsesSavedCrop(t *testing.T) {
	raw := testPNG()
	profile := defaultSamsungDisplayProfile()
	crop := CropRect{X: 0.25, Y: 0.25, W: 0.5, H: 0.5}
	withCrop, err := convertToSamsungPNG(raw, profile, crop, true)
	if err != nil {
		t.Fatal(err)
	}
	center, err := convertToSamsungPNG(raw, profile, CropRect{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(withCrop) == len(center) {
		t.Fatal("expected cropped output to differ from center crop")
	}
}
