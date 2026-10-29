// Package tonaddr reads and writes TON addresses (the Toncoin network). A receiving address is public data:
// this package never sees a key. It is shared by core (to check what the owner types) and payments (to
// compare what the chain reports), so both read an address the same way.
//
// An address has two forms. The raw form is "<workchain>:<64 hex digits>". The user-friendly form is 36 bytes in
// base64 (48 characters, the standard or the URL-safe alphabet): a flags byte (0x51 non-bounceable, 0x11
// bounceable; 0x80 added for the test network), the workchain, the 32-byte account hash and a CRC16 (XMODEM) of
// the first 34 bytes. Wallets show the friendly form: "UQ..." is non-bounceable and "EQ..." bounceable (on the
// test network "0Q..." and "kQ..."). A bounceable transfer to an account that is not deployed yet goes back to the
// sender, so the form BOBRES shows customers is the non-bounceable one.
package tonaddr

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

// Address is a TON account on the basechain.
type Address struct {
	Hash [32]byte
	// Bounceable and TestOnly are the flags the friendly form was written with; they are not part of which
	// account it is.
	Bounceable bool
	TestOnly   bool
}

// Errors Parse returns; the message says what to fix and never repeats the input.
var (
	ErrFormat    = errors.New("tonaddr: not a TON address: use the 48-character address your wallet shows (it starts with UQ or EQ)")
	ErrChecksum  = errors.New("tonaddr: the checksum of this address is wrong: a character was mistyped or lost")
	ErrWorkchain = errors.New("tonaddr: only addresses on the basechain (workchain 0) can receive payments here")
	ErrTestnet   = errors.New("tonaddr: this is a test network address; use a main network address")
)

const (
	tagBounceable    = 0x11
	tagNonBounceable = 0x51
	tagTestOnly      = 0x80
)

// crc16 is CRC16-CCITT (XMODEM): polynomial 0x1021, initial value 0, no reflection.
func crc16(b []byte) uint16 {
	var crc uint16
	for _, x := range b {
		crc ^= uint16(x) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// clean removes what a copy and paste may add around an address: spaces and invisible direction marks.
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '\u00a0', '\u200b', '\u200c', '\u200d', '\u200e', '\u200f', '\u202a', '\u202b', '\u202c',
			'\u202d', '\u202e', '\u2066', '\u2067', '\u2068', '\u2069', '\ufeff':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Parse reads an address in either form. The test network and any workchain but 0 are refused.
func Parse(s string) (Address, error) {
	s = clean(s)
	if s == "" {
		return Address{}, ErrFormat
	}
	if wc, h, ok := strings.Cut(s, ":"); ok {
		return parseRaw(wc, h)
	}
	return parseFriendly(s)
}

func parseRaw(wc, h string) (Address, error) {
	if len(h) != 64 {
		return Address{}, ErrFormat
	}
	n, err := strconv.Atoi(wc)
	if err != nil {
		return Address{}, ErrFormat
	}
	if n != 0 {
		return Address{}, ErrWorkchain
	}
	var a Address
	if _, err := hex.Decode(a.Hash[:], []byte(h)); err != nil {
		return Address{}, ErrFormat
	}
	return a, nil
}

func parseFriendly(s string) (Address, error) {
	if len(s) != 48 {
		return Address{}, ErrFormat
	}
	var raw []byte
	var err error
	if strings.ContainsAny(s, "-_") {
		raw, err = base64.URLEncoding.DecodeString(s)
	} else {
		raw, err = base64.StdEncoding.DecodeString(s)
		if err != nil { // letters only: the URL-safe alphabet is the same there
			raw, err = base64.URLEncoding.DecodeString(s)
		}
	}
	if err != nil || len(raw) != 36 {
		return Address{}, ErrFormat
	}
	if crc16(raw[:34]) != binary.BigEndian.Uint16(raw[34:]) {
		return Address{}, ErrChecksum
	}
	tag := raw[0]
	test := tag&tagTestOnly != 0
	switch tag &^ tagTestOnly {
	case tagBounceable, tagNonBounceable:
	default:
		return Address{}, ErrFormat
	}
	if raw[1] != 0 {
		return Address{}, ErrWorkchain
	}
	if test {
		return Address{}, ErrTestnet
	}
	a := Address{Bounceable: tag&^tagTestOnly == tagBounceable}
	copy(a.Hash[:], raw[2:34])
	return a, nil
}

// Raw is the raw form, "0:<hex>": the key two addresses of the same account share.
func (a Address) Raw() string { return "0:" + hex.EncodeToString(a.Hash[:]) }

// Friendly writes the user-friendly form in the URL-safe alphabet, bounceable or not, on the main network.
func (a Address) Friendly(bounceable bool) string {
	raw := make([]byte, 36)
	raw[0] = tagNonBounceable
	if bounceable {
		raw[0] = tagBounceable
	}
	copy(raw[2:34], a.Hash[:])
	c := crc16(raw[:34])
	binary.BigEndian.PutUint16(raw[34:], c)
	return base64.URLEncoding.EncodeToString(raw)
}

// Canonical is the form BOBRES stores and shows customers: non-bounceable, URL-safe, main network.
func (a Address) Canonical() string { return a.Friendly(false) }

// Same reports whether two addresses are the same account, whatever form each was written in.
func Same(a, b string) bool {
	x, err1 := Parse(a)
	y, err2 := Parse(b)
	return err1 == nil && err2 == nil && x.Hash == y.Hash
}

// Normalize reads an address in either form and returns the canonical form.
func Normalize(s string) (string, error) {
	a, err := Parse(s)
	if err != nil {
		return "", err
	}
	return a.Canonical(), nil
}
