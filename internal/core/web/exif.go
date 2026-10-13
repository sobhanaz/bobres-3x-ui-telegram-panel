package web

import (
	"encoding/binary"
	"image"
	"image/color"
)

// A phone saves a photo as the sensor saw it and notes in the EXIF data
// (Orientation, tag 0x0112) how to turn it upright. Browsers apply the tag
// and Go's decoder does not, so the picture is turned here before it is
// padded to a square.

// maxExifScan bounds how many JPEG header segments are looked through for
// the EXIF data (it follows SOI and, at most, the JFIF segment).
const maxExifScan = 32

// jpegOrientation returns the EXIF orientation (1 to 8) of a JPEG: 1, the
// upright picture, when it has none, and for anything malformed. It reads
// only the header segments, within data, and never panics.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	i := 2
	for range maxExifScan {
		// A marker: 0xff, any number of 0xff fill bytes, then its code.
		if i+1 >= len(data) || data[i] != 0xff {
			return 1
		}
		for i+1 < len(data) && data[i+1] == 0xff {
			i++
		}
		if i+1 >= len(data) {
			return 1
		}
		code := data[i+1]
		i += 2
		switch {
		case code == 0xd8 || code == 0x01 || code >= 0xd0 && code <= 0xd7:
			continue // no length
		case code == 0xd9 || code == 0xda:
			return 1 // the end, or the picture's data: no EXIF before it
		}
		if i+2 > len(data) {
			return 1
		}
		n := int(binary.BigEndian.Uint16(data[i:]))
		if n < 2 || i+n > len(data) {
			return 1
		}
		seg := data[i+2 : i+n]
		i += n
		if code == 0xe1 && len(seg) >= 6 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
	}
	return 1
}

// tiffOrientation reads the Orientation tag from the first directory (IFD0)
// of the TIFF structure inside an EXIF segment: 1 when it is missing or
// does not hold a value of 1 to 8.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(t[2:]) != 42 {
		return 1
	}
	ifd := uint64(bo.Uint32(t[4:]))
	if ifd+2 > uint64(len(t)) {
		return 1
	}
	entries := uint64(bo.Uint16(t[ifd:]))
	for k := range entries {
		at := ifd + 2 + 12*k // tag 2, type 2, count 4, value 4 bytes
		if at+12 > uint64(len(t)) {
			return 1
		}
		if bo.Uint16(t[at:]) != 0x0112 {
			continue
		}
		const short = 3
		if bo.Uint16(t[at+2:]) != short || bo.Uint32(t[at+4:]) != 1 {
			return 1
		}
		if o := int(bo.Uint16(t[at+8:])); o >= 1 && o <= 8 {
			return o
		}
		return 1
	}
	return 1
}

// oriented is src turned upright as EXIF orientation o (1 to 8) says,
// without copying it: pixel (x, y) of the result is a pixel of src.
type oriented struct {
	src  image.Image
	o    int
	w, h int // size of src
}

// orient returns img turned upright; img itself for orientation 1.
func orient(img image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return img
	}
	b := img.Bounds()
	return &oriented{src: img, o: o, w: b.Dx(), h: b.Dy()}
}

func (r *oriented) ColorModel() color.Model { return r.src.ColorModel() }

// Bounds: orientations 5 to 8 turn the picture a quarter.
func (r *oriented) Bounds() image.Rectangle {
	if r.o >= 5 {
		return image.Rect(0, 0, r.h, r.w)
	}
	return image.Rect(0, 0, r.w, r.h)
}

// At maps the result's pixel back to the source's: 2 mirrors left to right,
// 3 turns it half way, 4 mirrors top to bottom, 5 transposes it, 6 turns it
// a quarter clockwise, 7 transposes it along the other diagonal, 8 turns it
// a quarter counter-clockwise.
func (r *oriented) At(x, y int) color.Color {
	var sx, sy int
	switch r.o {
	case 2:
		sx, sy = r.w-1-x, y
	case 3:
		sx, sy = r.w-1-x, r.h-1-y
	case 4:
		sx, sy = x, r.h-1-y
	case 5:
		sx, sy = y, x
	case 6:
		sx, sy = y, r.h-1-x
	case 7:
		sx, sy = r.w-1-y, r.h-1-x
	default: // 8
		sx, sy = r.w-1-y, x
	}
	m := r.src.Bounds().Min
	return r.src.At(m.X+sx, m.Y+sy)
}
