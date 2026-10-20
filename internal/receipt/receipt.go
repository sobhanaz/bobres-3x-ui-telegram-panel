// Package receipt decides what a customer may send as the receipt of a manual
// payment from the Telegram Mini App: one JPEG, PNG or WebP image of at most
// MaxBytes. Core checks an upload with it before passing it to the bot, and
// the bot checks it again before it sends the image to Telegram, so the two
// agree on what is accepted. The decision rests on the bytes alone: a file
// name or a Content-Type the sender gave is never looked at.
package receipt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	_ "image/jpeg" // registers the decoder image.DecodeConfig reads a JPEG's size with
	_ "image/png"  // the same for a PNG
	"net/http"
)

// MaxBytes is the largest receipt, in bytes.
const MaxBytes = 8 << 20

// The largest picture a receipt may be: a side in pixels, and all of them. A
// size read from a header costs nothing to read, and a viewer that decodes
// the whole picture is the one that pays for a bigger one: the dashboard's
// review list shows every receipt in an <img>, and a picture of 40 megapixels
// is already 160 MB once it is decoded. A phone's camera at full size is over
// 8 MB as a JPEG, and the app makes a big picture smaller before it sends it.
const (
	maxSide   = 16384
	maxPixels = 40_000_000
)

// Telegram's limits for a picture sent as a photo (sendPhoto): the width and
// the height may not add up to more than 10000 pixels, and the longer side
// may be at most 20 times the shorter. Anything else goes as a document.
const (
	photoMaxSum   = 10000
	photoMaxRatio = 20
)

// Why a file is not a receipt.
var (
	ErrEmpty    = errors.New("receipt: the file is empty")
	ErrTooLarge = errors.New("receipt: the file is too large")
	ErrType     = errors.New("receipt: not a JPEG, PNG or WebP image")
)

// Info is what Inspect found out about an accepted receipt.
type Info struct {
	// ContentType is image/jpeg, image/png or image/webp.
	ContentType string
	// Ext is the file name extension for ContentType, without the dot.
	Ext string
	// Width and Height are in pixels, read from the file's header (a WebP's
	// canvas).
	Width, Height int
}

// Inspect says whether data is a receipt: not empty, at most MaxBytes, and a
// JPEG, PNG or WebP image by its first bytes, with a readable header that
// gives a picture size within limits. Nothing here decodes the picture.
func Inspect(data []byte) (Info, error) {
	switch {
	case len(data) == 0:
		return Info{}, ErrEmpty
	case len(data) > MaxBytes:
		return Info{}, ErrTooLarge
	}
	switch ct := http.DetectContentType(data); ct {
	case "image/jpeg", "image/png":
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return Info{}, ErrType
		}
		ext := "jpg"
		if ct == "image/png" {
			ext = "png"
		}
		return sized(Info{ContentType: ct, Ext: ext, Width: cfg.Width, Height: cfg.Height})
	case "image/webp":
		w, h, ok := webpSize(data)
		if !ok {
			return Info{}, ErrType
		}
		return sized(Info{ContentType: ct, Ext: "webp", Width: w, Height: h})
	}
	return Info{}, ErrType
}

// sized refuses a picture with no size or a gigantic one.
func sized(i Info) (Info, error) {
	switch {
	case i.Width < 1 || i.Height < 1:
		return Info{}, ErrType
	case i.Width > maxSide || i.Height > maxSide || i.Width*i.Height > maxPixels:
		return Info{}, ErrTooLarge
	}
	return i, nil
}

// webpSize checks a RIFF container beyond the 12 bytes DetectContentType
// reads: a known first chunk (lossy, lossless or extended) whose header gives
// the picture's size, and a file as long as its RIFF size says, so a
// truncated file is not taken for an image. ok is false for anything else.
func webpSize(data []byte) (w, h int, ok bool) {
	if len(data) < 20 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, false
	}
	if uint64(binary.LittleEndian.Uint32(data[4:8]))+8 > uint64(len(data)) {
		return 0, 0, false
	}
	// The first chunk: its name at 12, its size at 16, its payload from 20.
	switch string(data[12:16]) {
	case "VP8 ": // lossy: a 3-byte frame tag, the start code 9d 01 2a, then 14-bit sizes (and 2 bits of scale)
		if len(data) < 30 || data[23] != 0x9d || data[24] != 0x01 || data[25] != 0x2a {
			return 0, 0, false
		}
		return int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff), true
	case "VP8L": // lossless: the signature byte 2f, then 14 bits of width-1 and 14 bits of height-1
		if len(data) < 25 || data[20] != 0x2f {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(data[21:25])
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, true
	case "VP8X": // extended: flags and 3 reserved bytes, then 24 bits of canvas width-1 and 24 bits of height-1
		if len(data) < 30 {
			return 0, 0, false
		}
		le24 := func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }
		return le24(data[24:27]) + 1, le24(data[27:30]) + 1, true
	}
	return 0, 0, false
}

// PhotoSize reports whether Telegram takes the image as a photo. Only a JPEG
// or PNG whose size is known and within Telegram's limits does; a WebP, or a
// picture that is too wide, too tall or too narrow, is sent as a document.
func (i Info) PhotoSize() bool {
	if i.ContentType != "image/jpeg" && i.ContentType != "image/png" {
		return false
	}
	long, short := max(i.Width, i.Height), min(i.Width, i.Height)
	return short > 0 && i.Width+i.Height <= photoMaxSum && long <= photoMaxRatio*short
}
