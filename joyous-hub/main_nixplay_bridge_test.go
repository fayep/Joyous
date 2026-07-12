//go:build nixplaybridge

package main

import (
	"bytes"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureNixplayJPEGAppliesExifOrientation covers a bug where a source that was already a
// JPEG was forwarded to Nixplay's upload API unchanged, EXIF orientation tag and all — Nixplay's
// frame rendering does not reliably honor that tag, so a photo could display correctly in every
// normal viewer (which reads EXIF) but rotated wrong on the physical frame. ensureNixplayJPEG
// must always decode-and-reencode so orientation is baked into the pixels before upload,
// regardless of the source's original file extension.
func TestEnsureNixplayJPEGAppliesExifOrientation(t *testing.T) {
	// orient3.jpg stores pixels in a 180°-rotated layout with an EXIF Orientation=3 tag.
	data, err := os.ReadFile(filepath.Join("testdata", "exif", "orient3.jpg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	out, name, err := ensureNixplayJPEG(data, "phone.jpg", 0)
	if err != nil {
		t.Fatalf("ensureNixplayJPEG: %v", err)
	}
	if name != "phone.jpg" {
		t.Fatalf("name: got %q want %q", name, "phone.jpg")
	}

	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got := pixelAt(img, 0, 0); !colorsNear(got, color.RGBA{255, 255, 0, 255}) {
		t.Fatalf("top-left after correction: got %+v want yellow (orientation not baked in)", got)
	}
	if got := pixelAt(img, 63, 63); !colorsNear(got, color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("bottom-right after correction: got %+v want red (orientation not baked in)", got)
	}

	// The re-encoded output must not carry the orientation tag forward — the pixels are
	// already correct, so a stale/duplicate tag on top of that would double-rotate.
	if got := readExifOrientation(out); got != 1 {
		t.Fatalf("output orientation tag: got %d want 1 (none — already baked into pixels)", got)
	}
}

// TestEnsureNixplayJPEGAppliesRotateOverride covers imports whose rotation lived only in the
// source app's own database (e.g. a legacy Nixplay import — see NixDatabase.db's photo.rotation
// column), never in EXIF, so decodeAnyImage's EXIF correction alone leaves them wrong.
func TestEnsureNixplayJPEGAppliesRotateOverride(t *testing.T) {
	// orient1.jpg has no rotation needed from EXIF (orientation tag 1); RotateOverride must be
	// the only thing correcting it here.
	data, err := os.ReadFile(filepath.Join("testdata", "exif", "orient1.jpg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	out, _, err := ensureNixplayJPEG(data, "legacy.jpg", 90)
	if err != nil {
		t.Fatalf("ensureNixplayJPEG: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	// orient1.jpg: top-left red, bottom-right yellow. A 90° CW rotation moves red to top-right.
	if got := pixelAt(img, 63, 0); !colorsNear(got, color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("top-right after 90 deg rotate: got %+v want red", got)
	}
}

func TestEnsureNixplayJPEGRenamesNonJPEGExtension(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "exif", "orient1.jpg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	_, name, err := ensureNixplayJPEG(data, "photo.png", 0)
	if err != nil {
		t.Fatalf("ensureNixplayJPEG: %v", err)
	}
	if name != "photo.jpg" {
		t.Fatalf("name: got %q want %q", name, "photo.jpg")
	}
}
