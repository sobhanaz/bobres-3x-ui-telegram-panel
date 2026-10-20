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
// the whole picture is the one that pays for a bigger one.
const (
	maxSide   = 16384
	maxPixels = 100_000_000
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
	// Width and Height are in pixels; 0 for a WebP, whose size is not read.
	Width, Height int
}

// Inspect says whether data is a receipt: not empty, at most MaxBytes, and a
// JPEG, PNG or WebP image by its first bytes. A JPEG or PNG must also have a
// readable header with a size within limits. A WebP's picture size is not
// read: nothing here decodes a receipt, a WebP is only ever passed on to
// Telegram as a file, and its bytes are bounded by MaxBytes.
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
		if err != nil || cfg.Width < 1 || cfg.Height < 1 {
			return Info{}, ErrType
		}
		if cfg.Width > maxSide || cfg.Height > maxSide || cfg.Width*cfg.Height > maxPixels {
			return Info{}, ErrTooLarge
		}
		ext := "jpg"
		if ct == "image/png" {
			ext = "png"
		}
		return Info{ContentType: ct, Ext: ext, Width: cfg.Width, Height: cfg.Height}, nil
	case "image/webp":
		if !webp(data) {
			return Info{}, ErrType
		}
		return Info{ContentType: ct, Ext: "webp"}, nil
	}
	return Info{}, ErrType
}

// webp checks a RIFF container beyond the 12 bytes DetectContentType reads:
// a known first chunk (lossy, lossless or extended) and a size that the file
// actually has, so a truncated file is not taken for an image.
func webp(data []byte) bool {
	if len(data) < 20 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return false
	}
	switch string(data[12:16]) {
	case "VP8 ", "VP8L", "VP8X":
	default:
		return false
	}
	return uint64(binary.LittleEndian.Uint32(data[4:8]))+8 <= uint64(len(data))
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
