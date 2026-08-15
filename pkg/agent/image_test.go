package agent

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func createTestImage(width, height int, alpha bool) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			if alpha && x < width/2 {
				img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 128}) // semi-transparent red
			} else {
				img.Set(x, y, color.RGBA{R: 0, G: 255, B: 0, A: 255}) // solid green
			}
		}
	}
	var buf bytes.Buffer
	if alpha {
		_ = png.Encode(&buf, img)
	} else {
		_ = jpeg.Encode(&buf, img, nil)
	}
	return buf.Bytes()
}

func TestNormalizeAndResizeImage_Downscale(t *testing.T) {
	raw := createTestImage(1000, 500, false)
	out, mime, err := NormalizeAndResizeImage(bytes.NewReader(raw), 400)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mime != "image/jpeg" {
		t.Errorf("expected MIME image/jpeg, got %s", mime)
	}

	decoded, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("failed to decode output JPEG: %v", err)
	}

	b := decoded.Bounds()
	if b.Dx() != 400 || b.Dy() != 200 {
		t.Errorf("expected resized dimensions 400x200, got %dx%d", b.Dx(), b.Dy())
	}
}

func TestNormalizeAndResizeImage_NoUpscale(t *testing.T) {
	raw := createTestImage(200, 100, false)
	out, mime, err := NormalizeAndResizeImage(bytes.NewReader(raw), 400)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mime != "image/jpeg" {
		t.Errorf("expected MIME image/jpeg, got %s", mime)
	}

	decoded, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("failed to decode output JPEG: %v", err)
	}

	b := decoded.Bounds()
	if b.Dx() != 200 || b.Dy() != 100 {
		t.Errorf("expected original dimensions 200x100 (no upscaling), got %dx%d", b.Dx(), b.Dy())
	}
}

func TestNormalizeAndResizeImage_AlphaFlattening(t *testing.T) {
	raw := createTestImage(100, 100, true)
	out, mime, err := NormalizeAndResizeImage(bytes.NewReader(raw), 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mime != "image/jpeg" {
		t.Errorf("expected MIME image/jpeg, got %s", mime)
	}

	decoded, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("failed to decode output JPEG: %v", err)
	}

	// Verify that transparency didn't cause black background or failure
	b := decoded.Bounds()
	if b.Dx() != 100 || b.Dy() != 100 {
		t.Errorf("expected 100x100, got %dx%d", b.Dx(), b.Dy())
	}
}

func TestNormalizeAndResizeImage_InvalidFormat(t *testing.T) {
	badData := []byte("not an image file")
	_, _, err := NormalizeAndResizeImage(bytes.NewReader(badData), 500)
	if err == nil {
		t.Fatal("expected error for invalid image data, got nil")
	}
}
