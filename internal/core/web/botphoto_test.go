package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// bandsPNG is a w x h picture in three upright bands: opaque red, half
// transparent red, fully transparent.
func bandsPNG(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			switch {
			case x < w/3:
				img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
			case x < 2*w/3:
				img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 128})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func jpegOf(w, h int, c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95})
	return buf.Bytes()
}

// pngHeader is only a PNG's signature and header (IHDR) for a w x h picture:
// its size can be read, the picture cannot be decoded.
func pngHeader(w, h uint32) []byte {
	chunk := binary.BigEndian.AppendUint32([]byte("IHDR"), w)
	chunk = binary.BigEndian.AppendUint32(chunk, h)
	chunk = append(chunk, 8, 6, 0, 0, 0)                  // 8-bit RGBA, no interlace
	b := append([]byte("\x89PNG\r\n\x1a\n"), 0, 0, 0, 13) // the header's 13 bytes
	b = append(b, chunk...)
	return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(chunk))
}

// near: each channel within a JPEG's error of want.
func near(t *testing.T, img image.Image, x, y int, want color.RGBA, what string) {
	t.Helper()
	r, g, b, _ := img.At(x, y).RGBA()
	diff := func(a uint32, w uint8) int { return int(a>>8) - int(w) }
	for _, d := range []int{diff(r, want.R), diff(g, want.G), diff(b, want.B)} {
		if d < -24 || d > 24 {
			t.Errorf("%s at %d,%d: %v, want about %v", what, x, y, img.At(x, y), want)
			return
		}
	}
}

func storedBotPhoto(t *testing.T, f *fixture) (*store.Asset, image.Image) {
	t.Helper()
	a, err := f.st.GetAsset(context.Background(), f.st.Conn(), store.AssetBotPhoto)
	if err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(bytes.NewReader(a.Data))
	if err != nil || format != "jpeg" || a.ContentType != "image/jpeg" {
		t.Fatalf("stored photo: %s %s %v", a.ContentType, format, err)
	}
	return a, img
}

func TestBotPhotoUpload(t *testing.T) {
	f, b, bot := setupFixture(t)
	put := func(ctype string, body []byte) (int, map[string]any) {
		t.Helper()
		return b.raw(http.MethodPut, "/api/v1/branding/bot-photo", ctype, body)
	}
	msg := func(out map[string]any) string { m, _ := out["message"].(string); return m }

	// PNG or JPEG only: no WebP decoder, no SVG.
	if code, out := put("image/webp", webpOf(640, 640)); code != 415 || !strings.Contains(msg(out), "WebP") {
		t.Fatalf("webp: %d %v", code, out)
	}
	if code, out := put("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)); code != 415 {
		t.Fatalf("svg: %d %v", code, out)
	}
	if code, out := put("image/gif", []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")); code != 415 {
		t.Fatalf("gif: %d %v", code, out)
	}
	if code, _ := put("image/png", append(bandsPNG(300, 300), make([]byte, 600<<10)...)); code != 413 {
		t.Fatalf("over 512 KB: %d", code)
	}
	// Sides of 160 to 2048, read from the header: nothing is decoded first
	// (these have no picture data at all).
	for _, img := range [][]byte{pngHeader(8000, 8000), pngHeader(300, 2049), pngHeader(159, 300), pngOf(100, 400)} {
		if code, out := put("image/png", img); code != 400 || !strings.Contains(msg(out), "160 to 2048 pixels") {
			t.Errorf("wrong size: %d %v", code, out)
		}
	}
	if code, out := put("image/png", pngHeader(300, 300)); code != 400 || msg(out) != "could not read the image" {
		t.Fatalf("a header only, of the right size: %d %v", code, out)
	}
	if _, err := f.st.AssetInfo(context.Background(), f.st.Conn(), store.AssetBotPhoto); err == nil {
		t.Fatal("a refused picture was stored")
	}

	// A wide PNG with transparency: a square JPEG, the picture centred on
	// white, the transparency flattened onto white.
	before := bot.refreshCount()
	code, out := put("image/png", bandsPNG(300, 200))
	photo, _ := out["bot_photo"].(map[string]any)
	if code != 200 || photo == nil || !strings.HasPrefix(photo["url"].(string), "/api/v1/branding/bot-photo?v=") || photo["size"] == float64(0) {
		t.Fatalf("upload: %d %v", code, out)
	}
	waitFor(t, "the bot told about the photo", func() bool { return bot.refreshCount() == before+1 })
	a, img := storedBotPhoto(t, f)
	if b := img.Bounds(); b.Dx() != 300 || b.Dy() != 300 {
		t.Fatalf("stored %v, want a 300x300 square", b)
	}
	white, red, pink := color.RGBA{255, 255, 255, 255}, color.RGBA{255, 0, 0, 255}, color.RGBA{255, 127, 127, 255}
	near(t, img, 50, 150, red, "opaque")
	near(t, img, 150, 150, pink, "half transparent")
	near(t, img, 250, 150, white, "transparent")
	near(t, img, 50, 20, white, "the band above")
	near(t, img, 50, 280, white, "the band below")
	near(t, img, 50, 70, red, "the picture's first rows")
	if photo["size"] != float64(a.Size) || photo["updated_at"] != float64(a.UpdatedAt.Unix()) {
		t.Fatalf("info %v, stored %d bytes at %v", photo, a.Size, a.UpdatedAt)
	}
	e := lastAudit(t, b, "action=branding.bot_photo.set")
	if e["entity_id"] != store.AssetBotPhoto || asJSON(e["after"]) != `{"height":300,"size":`+asJSON(a.Size)+`,"width":300}` {
		t.Fatalf("audit: %v", e)
	}

	// The stored JPEG, for the preview: staff only.
	resp, err := b.c.Get(f.srv.URL + photo["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	h := resp.Header
	if resp.StatusCode != 200 || h.Get("Content-Type") != "image/jpeg" || !bytes.Equal(got, a.Data) ||
		h.Get("Cache-Control") != "private, max-age=86400" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("photo file: %d %v", resp.StatusCode, h)
	}
	if code, _ := f.browser().do(http.MethodGet, "/api/v1/branding/bot-photo", nil); code != 401 {
		t.Fatalf("read without a session: %d", code)
	}
	f.staffUser(7311, "support")
	sup := f.browser()
	sup.loginWithLink(7311)
	if code, _ := sup.do(http.MethodGet, "/api/v1/branding/bot-photo", nil); code != 403 {
		t.Fatalf("support read the photo: %d", code)
	}

	// A JPEG, a tall one this time.
	code, _ = put("image/jpeg", jpegOf(160, 400, color.RGBA{0, 0, 255, 255}))
	if code != 200 {
		t.Fatalf("jpeg: %d", code)
	}
	a2, img := storedBotPhoto(t, f)
	if b := img.Bounds(); b.Dx() != 400 || b.Dy() != 400 || !a2.UpdatedAt.After(a.UpdatedAt) {
		t.Fatalf("stored %v at %v", b, a2.UpdatedAt)
	}
	near(t, img, 200, 200, color.RGBA{0, 0, 255, 255}, "the picture")
	near(t, img, 40, 200, white, "the band on the left")
	near(t, img, 360, 200, white, "the band on the right")

	// Removed: the bot is told, the preview is gone.
	before = bot.refreshCount()
	if code, out := b.do(http.MethodDelete, "/api/v1/branding/bot-photo", nil); code != 200 || out["bot_photo"] != nil {
		t.Fatalf("remove: %d %v", code, out)
	}
	waitFor(t, "the bot told about the removal", func() bool { return bot.refreshCount() == before+1 })
	if code, _ := b.do(http.MethodGet, "/api/v1/branding/bot-photo", nil); code != 404 {
		t.Fatalf("removed photo still served: %d", code)
	}
	if e := lastAudit(t, b, "action=branding.bot_photo.removed"); e["entity_id"] != store.AssetBotPhoto {
		t.Fatalf("removal audit: %v", e)
	}
	if code, _ := b.do(http.MethodDelete, "/api/v1/branding/bot-photo", nil); code != 200 {
		t.Fatalf("remove again: %d", code)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/audit?action=branding.bot_photo.removed", nil); out["total"] != float64(1) {
		t.Fatalf("removing nothing was audited: %v", out)
	}
}

// The logo's changes tell the bot too (its brand stays fresh).
func TestLogoChangesTellTheBot(t *testing.T) {
	_, b, bot := setupFixture(t)
	before := bot.refreshCount()
	if code, _ := b.raw(http.MethodPut, "/api/v1/branding/logo", "image/png", pngOf(64, 64)); code != 200 {
		t.Fatalf("logo: %d", code)
	}
	waitFor(t, "a refresh after the logo upload", func() bool { return bot.refreshCount() == before+1 })
	if code, _ := b.do(http.MethodDelete, "/api/v1/branding/logo", nil); code != 200 {
		t.Fatalf("remove logo: %d", code)
	}
	waitFor(t, "a refresh after the logo removal", func() bool { return bot.refreshCount() == before+2 })
}

// One conversion at a time: an upload waits while another is converted.
func TestBotPhotoConversionsOneAtATime(t *testing.T) {
	f, b, _ := setupFixture(t)
	f.api.photoSlot <- struct{}{} // another conversion is running
	done := make(chan int, 1)
	go func() {
		code, _ := b.raw(http.MethodPut, "/api/v1/branding/bot-photo", "image/png", bandsPNG(300, 300))
		done <- code
	}()
	select {
	case code := <-done:
		t.Fatalf("converted beside another conversion: %d", code)
	case <-time.After(300 * time.Millisecond):
	}
	<-f.api.photoSlot
	select {
	case code := <-done:
		if code != 200 {
			t.Fatalf("after the other conversion: %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the upload never got its turn")
	}
	if n := len(f.api.photoSlot); n != 0 {
		t.Fatalf("the slot is still held: %d", n)
	}
}

// A picture whose JPEG would not fit the bot's gRPC message (1-bit noise
// compresses well as a PNG, not as a JPEG) is refused.
func TestBotPhotoTooDetailed(t *testing.T) {
	f, b, _ := setupFixture(t)
	noise := image.NewPaletted(image.Rect(0, 0, 2048, 1900), color.Palette{color.Black, color.White})
	_, _ = rand.Read(noise.Pix)
	for i := range noise.Pix {
		noise.Pix[i] &= 1 // black or white
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, noise); err != nil || buf.Len() > maxLogo {
		t.Fatalf("test picture: %d bytes, %v", buf.Len(), err)
	}
	code, out := b.raw(http.MethodPut, "/api/v1/branding/bot-photo", "image/png", buf.Bytes())
	if m, _ := out["message"].(string); code != 400 || !strings.Contains(m, "larger than 3 MB") {
		t.Fatalf("noise: %d %v", code, out)
	}
	if _, err := f.st.AssetInfo(context.Background(), f.st.Conn(), store.AssetBotPhoto); err == nil {
		t.Fatal("the refused picture was stored")
	}
}

// quadrantsJPEG is a w x h picture of four colours: red top left, green top
// right, blue bottom left, yellow bottom right.
func quadrantsJPEG(w, h int) []byte {
	colors := map[[2]bool]color.RGBA{
		{false, false}: {255, 0, 0, 255}, {true, false}: {0, 255, 0, 255},
		{false, true}: {0, 0, 255, 255}, {true, true}: {255, 255, 0, 255},
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, colors[[2]bool{x >= w/2, y >= h/2}])
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95})
	return buf.Bytes()
}

// tiffOrder is a TIFF byte order, to read and to write.
type tiffOrder interface {
	binary.ByteOrder
	binary.AppendByteOrder
}

// exifPayload is the data of a JPEG's EXIF segment: IFD0 has the camera
// make (so the orientation is not its first tag) and the orientation.
func exifPayload(order tiffOrder, orientation uint16) []byte {
	t := []byte("II")
	if order == binary.BigEndian {
		t = []byte("MM")
	}
	t = order.AppendUint16(t, 42)
	t = order.AppendUint32(t, 8) // IFD0 follows the header
	t = order.AppendUint16(t, 2) // two entries
	t = order.AppendUint16(t, 0x010f)
	t = order.AppendUint16(t, 2) // ASCII
	t = order.AppendUint32(t, 2)
	t = append(t, 'X', 0, 0, 0)
	t = order.AppendUint16(t, 0x0112)
	t = order.AppendUint16(t, 3) // SHORT
	t = order.AppendUint32(t, 1)
	t = order.AppendUint16(t, orientation)
	t = append(t, 0, 0)
	t = order.AppendUint32(t, 0) // no next directory
	return append([]byte("Exif\x00\x00"), t...)
}

// segment is a JPEG segment: its marker, its length (which counts itself)
// and its data.
func segment(marker byte, data []byte) []byte {
	return append(binary.BigEndian.AppendUint16([]byte{0xff, marker}, uint16(len(data)+2)), data...) //nolint:gosec // a test segment is short
}

// withSegments puts JPEG segments right after SOI.
func withSegments(jpegData []byte, segs ...[]byte) []byte {
	out := append([]byte{}, jpegData[:2]...)
	for _, seg := range segs {
		out = append(out, seg...)
	}
	return append(out, jpegData[2:]...)
}

// withOrientation is a JPEG that says in its EXIF data how to turn it upright.
func withOrientation(jpegData []byte, order tiffOrder, orientation uint16) []byte {
	return withSegments(jpegData, segment(0xe1, exifPayload(order, orientation)))
}

// The four colours of quadrantsJPEG, as the stored photo shows them.
var (
	red    = color.RGBA{255, 0, 0, 255}
	green  = color.RGBA{0, 255, 0, 255}
	blue   = color.RGBA{0, 0, 255, 255}
	yellow = color.RGBA{255, 255, 0, 255}
)

// A phone's picture is stored sideways; EXIF says how to turn it. The
// bot photo is turned upright (as the browser's preview is) before it is
// padded to a square: a 300x200 picture with orientation 5 to 8 is 200x300
// upright, so it is centred in a 300x300 square with white bands left and
// right.
func TestBotPhotoIsTurnedUpright(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}
	// The four colours at the picture's corners (top left, top right,
	// bottom left, bottom right) once upright, per orientation.
	cases := []struct {
		orientation uint16
		what        string
		corners     [4]color.RGBA
	}{
		{1, "upright", [4]color.RGBA{red, green, blue, yellow}},
		{2, "mirrored left to right", [4]color.RGBA{green, red, yellow, blue}},
		{3, "turned half way", [4]color.RGBA{yellow, blue, green, red}},
		{4, "mirrored top to bottom", [4]color.RGBA{blue, yellow, red, green}},
		{5, "transposed", [4]color.RGBA{red, blue, green, yellow}},
		{6, "turned a quarter clockwise", [4]color.RGBA{blue, red, yellow, green}},
		{7, "transposed along the other diagonal", [4]color.RGBA{yellow, green, blue, red}},
		{8, "turned a quarter counter-clockwise", [4]color.RGBA{green, yellow, red, blue}},
	}
	for _, order := range []tiffOrder{binary.LittleEndian, binary.BigEndian} {
		for _, c := range cases {
			name := strings.ToLower(order.String()) + "/" + c.what
			photo, side, err := botPhotoJPEG(withOrientation(quadrantsJPEG(300, 200), order, c.orientation))
			if err != nil || side != 300 {
				t.Fatalf("%s: side %d, %v", name, side, err)
			}
			img, _, err := image.Decode(bytes.NewReader(photo))
			if err != nil || img.Bounds().Dx() != 300 || img.Bounds().Dy() != 300 {
				t.Fatalf("%s: %v %v", name, img.Bounds(), err)
			}
			// Where the upright picture's corners are in the square.
			xs, ys, bands := [2]int{75, 225}, [2]int{100, 200}, [][2]int{{150, 20}, {150, 280}}
			if c.orientation >= 5 {
				xs, ys, bands = [2]int{100, 200}, [2]int{75, 225}, [][2]int{{20, 150}, {280, 150}}
			}
			for i, want := range c.corners {
				near(t, img, xs[i%2], ys[i/2], want, name+" corner "+strconv.Itoa(i))
			}
			for _, p := range bands {
				near(t, img, p[0], p[1], white, name+" white band")
			}
		}
	}
}

// Without EXIF orientation, or with a PNG, nothing is turned.
func TestBotPhotoWithoutOrientationIsAsStored(t *testing.T) {
	for name, data := range map[string][]byte{
		"no EXIF":          quadrantsJPEG(300, 200),
		"EXIF without one": withSegments(quadrantsJPEG(300, 200), segment(0xe1, []byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x00\x00\x00\x00\x00"))),
		"orientation 0":    withOrientation(quadrantsJPEG(300, 200), binary.BigEndian, 0),
		"orientation 9":    withOrientation(quadrantsJPEG(300, 200), binary.LittleEndian, 9),
	} {
		photo, _, err := botPhotoJPEG(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		img, _, _ := image.Decode(bytes.NewReader(photo))
		near(t, img, 75, 100, red, name+" top left")
		near(t, img, 225, 200, yellow, name+" bottom right")
	}
}

// Through the API: the stored photo is upright, as the browser's preview.
func TestBotPhotoUploadTurnedUpright(t *testing.T) {
	f, b, _ := setupFixture(t)
	// Orientation 6: stored sideways, to be turned a quarter clockwise.
	code, out := b.raw(http.MethodPut, "/api/v1/branding/bot-photo", "image/jpeg", withOrientation(quadrantsJPEG(300, 200), binary.BigEndian, 6))
	if code != 200 {
		t.Fatalf("upload: %d %v", code, out)
	}
	_, img := storedBotPhoto(t, f)
	near(t, img, 100, 75, blue, "top left")
	near(t, img, 200, 75, red, "top right")
	near(t, img, 100, 225, yellow, "bottom left")
	near(t, img, 200, 225, green, "bottom right")
	near(t, img, 20, 150, color.RGBA{255, 255, 255, 255}, "the band on the left")
}

// The EXIF data comes from an upload: whatever it holds, it is read within
// the bytes there are, and never makes the upload fail or panic.
func TestJPEGOrientationOfBadData(t *testing.T) {
	good := withSegments([]byte{0xff, 0xd8, 0xff, 0xd9}, segment(0xe1, exifPayload(binary.LittleEndian, 6)))
	if got := jpegOrientation(good); got != 6 {
		t.Fatalf("good: %d", got)
	}
	payload := func(order tiffOrder, mutate func([]byte)) []byte {
		p := exifPayload(order, 6)
		mutate(p)
		return withSegments([]byte{0xff, 0xd8, 0xff, 0xd9}, segment(0xe1, p))
	}
	le := binary.LittleEndian
	// Offsets in the payload: 6 "Exif\0\0", TIFF at 6: byte order 6, magic 8,
	// IFD0 offset 10, its entry count 14, first entry 16, second entry 28
	// (tag 28, type 30, count 32, value 36).
	for name, data := range map[string][]byte{
		"empty":                   nil,
		"SOI only":                {0xff, 0xd8},
		"not a JPEG":              []byte("GIF89a"),
		"marker without a code":   {0xff, 0xd8, 0xff},
		"length below 2":          {0xff, 0xd8, 0xff, 0xe1, 0x00, 0x01, 0xff, 0xd9},
		"length beyond the data":  {0xff, 0xd8, 0xff, 0xe1, 0xff, 0xff, 'E', 'x', 'i', 'f', 0, 0},
		"no length":               {0xff, 0xd8, 0xff, 0xe1},
		"Exif cut short":          {0xff, 0xd8, 0xff, 0xe1, 0x00, 0x0a, 'E', 'x', 'i', 'f', 0, 0, 'I', 'I'},
		"data before any marker":  {0xff, 0xd8, 0x00, 0xff, 0xe1, 0x00, 0x02},
		"starts at the picture":   {0xff, 0xd8, 0xff, 0xda, 0x00, 0x02},
		"another byte order":      payload(le, func(p []byte) { p[6], p[7] = 'X', 'X' }),
		"wrong magic":             payload(le, func(p []byte) { p[8] = 43 }),
		"IFD0 beyond the data":    payload(le, func(p []byte) { le.PutUint32(p[10:], 0xffffffff) }),
		"IFD0 at the very end":    payload(le, func(p []byte) { le.PutUint32(p[10:], uint32(len(p)-6)) }), //nolint:gosec // a test payload is short
		"entries cut short":       withSegments([]byte{0xff, 0xd8, 0xff, 0xd9}, segment(0xe1, exifPayload(le, 6)[:34])),
		"orientation of 0":        payload(le, func(p []byte) { le.PutUint16(p[36:], 0) }),
		"orientation of 9":        payload(le, func(p []byte) { le.PutUint16(p[36:], 9) }),
		"orientation of 0xffff":   payload(le, func(p []byte) { le.PutUint16(p[36:], 0xffff) }),
		"orientation not a SHORT": payload(le, func(p []byte) { le.PutUint16(p[30:], 4) }),
		"orientation counted 2":   payload(le, func(p []byte) { le.PutUint32(p[32:], 2) }),
	} {
		if got := jpegOrientation(data); got != 1 {
			t.Errorf("%s: orientation %d, want 1 (upright)", name, got)
		}
	}
	// Not the first segment, nor the first tag: found. Another APP1 (XMP)
	// before it is skipped, and a long run of fill bytes is walked through.
	xmp := segment(0xe1, []byte("http://ns.adobe.com/xap/1.0/\x00<x/>"))
	jfif := segment(0xe0, []byte("JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00"))
	exif := segment(0xe1, exifPayload(binary.BigEndian, 8))
	if got := jpegOrientation(withSegments([]byte{0xff, 0xd8, 0xff, 0xd9}, jfif, xmp, exif)); got != 8 {
		t.Errorf("after JFIF and XMP: %d", got)
	}
	fill := append(bytes.Repeat([]byte{0xff}, 5000), exif[1:]...) // 0xff fill bytes, then the marker's code
	if got := jpegOrientation(withSegments([]byte{0xff, 0xd8, 0xff, 0xd9}, fill)); got != 8 {
		t.Errorf("after fill bytes: %d", got)
	}
	// The look through the header is bounded: a flood of segments hides it.
	var many [][]byte
	for range 100 {
		many = append(many, segment(0xfe, []byte("x")))
	}
	if got := jpegOrientation(withSegments([]byte{0xff, 0xd8, 0xff, 0xd9}, append(many, exif)...)); got != 1 {
		t.Errorf("after 100 segments: %d", got)
	}
	// Every prefix, and every byte of the header set to other values, gives
	// an orientation of 1 to 8 (the test would die on a panic).
	for n := range len(good) + 1 {
		if got := jpegOrientation(good[:n]); got < 1 || got > 8 {
			t.Fatalf("prefix of %d bytes: %d", n, got)
		}
	}
	for i := range good {
		for _, v := range []byte{0x00, 0x01, 0x7f, 0x80, 0xff} {
			d := bytes.Clone(good)
			d[i] = v
			if got := jpegOrientation(d); got < 1 || got > 8 {
				t.Fatalf("byte %d set to %#x: %d", i, v, got)
			}
		}
	}
}

// The same picture again changes nothing: the JPEG that comes out is the
// one stored, so no new version (the bot would upload it to Telegram again,
// adding to its history of profile photos), no audit entry and no refresh
// of the bot. Another picture is a change as before.
func TestBotPhotoSamePictureAgain(t *testing.T) {
	f, b, bot := setupFixture(t)
	put := func(body []byte) (int, map[string]any) {
		t.Helper()
		return b.raw(http.MethodPut, "/api/v1/branding/bot-photo", "image/png", body)
	}
	audits := func() float64 {
		t.Helper()
		_, out := b.do(http.MethodGet, "/api/v1/audit?action=branding.bot_photo.set", nil)
		n, _ := out["total"].(float64)
		return n
	}

	before := bot.refreshCount()
	code, first := put(bandsPNG(300, 200))
	if code != 200 {
		t.Fatalf("first upload: %d %v", code, first)
	}
	waitFor(t, "the bot told about the photo", func() bool { return bot.refreshCount() == before+1 })
	a1, _ := storedBotPhoto(t, f)

	code, again := put(bandsPNG(300, 200))
	if code != 200 || asJSON(again["bot_photo"]) != asJSON(first["bot_photo"]) {
		t.Fatalf("the same picture again: %d %v, was %v", code, again["bot_photo"], first["bot_photo"])
	}
	a2, _ := storedBotPhoto(t, f)
	if !a2.UpdatedAt.Equal(a1.UpdatedAt) || !bytes.Equal(a2.Data, a1.Data) {
		t.Fatalf("stored again: %v then %v", a1.UpdatedAt, a2.UpdatedAt)
	}
	time.Sleep(150 * time.Millisecond) // the bot is told in the background
	if n := bot.refreshCount(); n != before+1 || audits() != 1 {
		t.Fatalf("the same picture again: %d refreshes (want %d), %v audit entries (want 1)", n, before+1, audits())
	}

	// Another picture: stored, audited, the bot told.
	code, _ = put(bandsPNG(320, 200))
	if code != 200 {
		t.Fatalf("another picture: %d", code)
	}
	waitFor(t, "the bot told about another photo", func() bool { return bot.refreshCount() == before+2 })
	a3, _ := storedBotPhoto(t, f)
	if !a3.UpdatedAt.After(a1.UpdatedAt) || bytes.Equal(a3.Data, a1.Data) || audits() != 2 {
		t.Fatalf("another picture: stored at %v, %v audit entries", a3.UpdatedAt, audits())
	}
	// And the first one again is a change too (the stored one is the other).
	if code, _ = put(bandsPNG(300, 200)); code != 200 {
		t.Fatalf("the first picture again: %d", code)
	}
	waitFor(t, "the bot told about the first photo again", func() bool { return bot.refreshCount() == before+3 })
	if audits() != 3 {
		t.Fatalf("audit entries: %v", audits())
	}
}
