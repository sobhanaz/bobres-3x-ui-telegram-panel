package branding

import (
	"bytes"
	"image/jpeg"
	"testing"
)

// The embedded default photo is a square JPEG Telegram accepts (at least 160px).
func TestDefaultBotPhotoIsASquareJPEG(t *testing.T) {
	img, err := jpeg.Decode(bytes.NewReader(DefaultBotPhoto()))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != b.Dy() || b.Dx() < 160 {
		t.Fatalf("size %dx%d", b.Dx(), b.Dy())
	}
	p := DefaultBotPhoto()
	p[0] = 0
	if DefaultBotPhoto()[0] == 0 {
		t.Fatal("DefaultBotPhoto shares its buffer")
	}
}
