package receipt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	img.SetGray(0, 0, color.Gray{Y: 200})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pngHeader is a PNG that is only a signature and an IHDR chunk claiming a
// size: all that is read of it to learn the size.
func pngHeader(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8], ihdr[9] = 8, 0 // 8 bits, grayscale
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&b, binary.BigEndian, uint32(13)) // the IHDR chunk is 13 bytes
	crc := crc32.NewIEEE()
	crc.Write([]byte("IHDR"))
	crc.Write(ihdr)
	b.WriteString("IHDR")
	b.Write(ihdr)
	_ = binary.Write(&b, binary.BigEndian, crc.Sum32())
	return b.Bytes()
}

// webpFile is a RIFF/WEBP container whose first chunk is called chunk and has this payload.
func webpFile(chunk string, payload []byte) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(4+8+len(payload))) //nolint:gosec // a small test size
	b.WriteString("WEBP")
	b.WriteString(chunk)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(payload))) //nolint:gosec // a small test size
	b.Write(payload)
	return b.Bytes()
}

// webpLossy has the header of a lossy picture: a frame tag, the start code and the two sizes.
func webpLossy(w, h uint16) []byte {
	p := []byte{0x30, 0x01, 0x00, 0x9d, 0x01, 0x2a}
	p = binary.LittleEndian.AppendUint16(p, w)
	p = binary.LittleEndian.AppendUint16(p, h)
	return webpFile("VP8 ", append(p, make([]byte, 8)...))
}

// webpLossless has the header of a lossless picture: the signature, then 14 bits of width-1 and 14 of height-1.
func webpLossless(w, h uint32) []byte {
	p := binary.LittleEndian.AppendUint32([]byte{0x2f}, (w-1)|(h-1)<<14)
	return webpFile("VP8L", append(p, make([]byte, 8)...))
}

// webpExtended has the header of an extended picture: flags, then the canvas size minus one in 24 bits each.
func webpExtended(w, h uint32) []byte {
	p := []byte{0x10, 0, 0, 0}
	p = append(p, binary.LittleEndian.AppendUint32(nil, w-1)[:3]...)
	p = append(p, binary.LittleEndian.AppendUint32(nil, h-1)[:3]...)
	return webpFile("VP8X", append(p, make([]byte, 8)...))
}

func TestInspectAcceptsJPEGPNGAndWebP(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want Info
	}{
		{"jpeg", jpegBytes(t, 40, 30), Info{ContentType: "image/jpeg", Ext: "jpg", Width: 40, Height: 30}},
		{"png", pngBytes(t, 20, 50), Info{ContentType: "image/png", Ext: "png", Width: 20, Height: 50}},
		{"webp lossy", webpLossy(640, 480), Info{ContentType: "image/webp", Ext: "webp", Width: 640, Height: 480}},
		{"webp lossless", webpLossless(320, 200), Info{ContentType: "image/webp", Ext: "webp", Width: 320, Height: 200}},
		{"webp extended", webpExtended(1000, 2000), Info{ContentType: "image/webp", Ext: "webp", Width: 1000, Height: 2000}},
	}
	for _, c := range cases {
		got, err := Inspect(c.data)
		if err != nil || got != c.want {
			t.Errorf("%s: %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
}

func TestInspectRefusesWhatIsNotAReceipt(t *testing.T) {
	truncatedPNG := pngBytes(t, 20, 50)[:20]
	truncatedWebP := webpLossy(64, 64)[:30]
	noStartCode := webpLossy(64, 64)
	noStartCode[24] = 0x00
	noSignature := webpLossless(64, 64)
	noSignature[20] = 0x00
	shortExtended := webpFile("VP8X", make([]byte, 6))
	cases := map[string][]byte{
		"html":              []byte("<!doctype html><script>alert(1)</script>"),
		"svg":               []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"pdf":               []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n"),
		"gif":               []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"),
		"bmp":               append([]byte("BM"), make([]byte, 60)...),
		"text":              []byte(strings.Repeat("a receipt, honest ", 20)),
		"zip":               []byte("PK\x03\x04" + strings.Repeat("\x00", 40)),
		"exe":               append([]byte("MZ"), make([]byte, 60)...),
		"jpeg magic only":   {0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0},
		"png magic only":    []byte("\x89PNG\r\n\x1a\n"),
		"truncated png":     truncatedPNG,
		"webp unknown":      webpFile("ABCD", make([]byte, 16)),
		"webp truncated":    truncatedWebP,
		"webp no start":     noStartCode,
		"webp no signature": noSignature,
		"webp short header": shortExtended,
		"webp no size":      webpLossy(0, 0),
		"riff but not webp": append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 40)...),
	}
	for name, data := range cases {
		if _, err := Inspect(data); !errors.Is(err, ErrType) {
			t.Errorf("%s: %v, want ErrType", name, err)
		}
	}
}

func TestInspectLimits(t *testing.T) {
	if _, err := Inspect(nil); !errors.Is(err, ErrEmpty) {
		t.Errorf("nil: %v", err)
	}
	if _, err := Inspect([]byte{}); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty: %v", err)
	}
	// Size is judged before anything else, so no header of a huge upload is parsed.
	big := make([]byte, MaxBytes+1)
	copy(big, pngBytes(t, 4, 4))
	if _, err := Inspect(big); !errors.Is(err, ErrTooLarge) {
		t.Errorf("over the size limit: %v", err)
	}
	exact := append(webpLossy(100, 100), make([]byte, MaxBytes-len(webpLossy(100, 100)))...)
	if len(exact) != MaxBytes {
		t.Fatalf("test data is %d bytes", len(exact))
	}
	if _, err := Inspect(exact); err != nil {
		t.Errorf("exactly the limit: %v", err)
	}
	// A header that claims a gigantic picture is refused unread.
	for name, data := range map[string][]byte{
		"wide png":   pngHeader(20000, 10),
		"tall png":   pngHeader(10, 16385),
		"huge png":   pngHeader(15000, 15000),
		"zero width": pngHeader(0, 10),
	} {
		_, err := Inspect(data)
		if name == "zero width" {
			if !errors.Is(err, ErrType) {
				t.Errorf("%s: %v, want ErrType", name, err)
			}
			continue
		}
		if !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: %v, want ErrTooLarge", name, err)
		}
	}
	if _, err := Inspect(pngHeader(16384, 100)); err != nil {
		t.Errorf("a wide but allowed picture: %v", err)
	}
}

// A picture's size is read from its header whatever the format, because the
// dashboard shows every receipt in an <img> and so decodes all of it.
func TestInspectLimitsThePictureOfEveryFormat(t *testing.T) {
	tooLarge := map[string][]byte{
		"png of 42 megapixels":        pngHeader(7000, 6000),
		"lossy webp over every limit": webpLossy(16383, 16383),
		"lossless webp 16384 square":  webpLossless(16384, 16384), // a header of 29 bytes that claims 268 megapixels
		"lossless webp 42 megapixels": webpLossless(7000, 6000),
		"extended webp too wide":      webpExtended(20000, 10),
		"extended webp 16 million":    webpExtended(1<<24, 1<<24),
		"extended webp 42 megapixels": webpExtended(6500, 6500),
	}
	for name, data := range tooLarge {
		if _, err := Inspect(data); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: %v, want ErrTooLarge", name, err)
		}
	}
	allowed := map[string][]byte{
		"png of 36 megapixels":        pngHeader(6000, 6000),
		"lossless webp 16384 wide":    webpLossless(16384, 100),
		"extended webp 36 megapixels": webpExtended(6000, 6000),
		"lossy webp of a phone":       webpLossy(4000, 3000),
	}
	for name, data := range allowed {
		if _, err := Inspect(data); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPhotoSize(t *testing.T) {
	cases := []struct {
		info Info
		want bool
	}{
		{Info{ContentType: "image/jpeg", Width: 4000, Height: 3000}, true},
		{Info{ContentType: "image/png", Width: 1080, Height: 2400}, true},
		{Info{ContentType: "image/png", Width: 5000, Height: 5000}, true},   // the sides add up to 10000
		{Info{ContentType: "image/jpeg", Width: 6000, Height: 5000}, false}, // 11000
		{Info{ContentType: "image/png", Width: 1000, Height: 20000}, false}, // beyond Telegram's sum
		{Info{ContentType: "image/png", Width: 500, Height: 10000}, false},  // ratio 20, but the sum is 10500
		{Info{ContentType: "image/png", Width: 400, Height: 8000}, true},    // ratio exactly 20
		{Info{ContentType: "image/png", Width: 399, Height: 8000}, false},   // ratio just over 20
		{Info{ContentType: "image/png", Width: 1, Height: 100}, false},
		{Info{ContentType: "image/webp"}, false},
		{Info{ContentType: "image/jpeg"}, false}, // a size never read
	}
	for _, c := range cases {
		if got := c.info.PhotoSize(); got != c.want {
			t.Errorf("%+v: %v, want %v", c.info, got, c.want)
		}
	}
}
