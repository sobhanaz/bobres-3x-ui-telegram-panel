//go:build integration

package adapter

// A minimal Shadowsocks client (AEAD, chacha20-ietf-poly1305), enough for the
// real-panel tests to push traffic through a Marzban user's own account and
// see whether the panel's Xray core lets it pass: one connection, one request,
// the answer read to its end.

import (
	"bufio"
	"crypto/cipher"
	"crypto/md5" //nolint:gosec // Shadowsocks derives the master key from the password with MD5 (EVP_BytesToKey)
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // Shadowsocks derives the session key with HKDF-SHA1
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// ssKey is the master key of a password: MD5 blocks chained until there are 32 bytes.
func ssKey(password string) []byte {
	var key, prev []byte
	for len(key) < chacha20poly1305.KeySize {
		h := md5.New() //nolint:gosec // see the import
		h.Write(prev)
		h.Write([]byte(password))
		prev = h.Sum(nil)
		key = append(key, prev...)
	}
	return key[:chacha20poly1305.KeySize]
}

// ssAEAD is the cipher of one direction: the session key is HKDF-SHA1 of the
// master key and the stream's salt.
func ssAEAD(master, salt []byte) (cipher.AEAD, error) {
	sub := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(hkdf.New(sha1.New, master, salt, []byte("ss-subkey")), sub); err != nil { //nolint:gosec // see the import
		return nil, err
	}
	return chacha20poly1305.New(sub)
}

// nonce counts chunks, little endian.
type nonce [chacha20poly1305.NonceSize]byte

func (n *nonce) next() []byte {
	cur := append([]byte(nil), n[:]...)
	for i := range n {
		n[i]++
		if n[i] != 0 {
			break
		}
	}
	return cur
}

// ssAddress is the SOCKS-style address header that starts the stream.
func ssAddress(target string) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("port %q", portStr)
	}
	var hdr []byte
	if ip := net.ParseIP(host).To4(); ip != nil {
		hdr = append([]byte{1}, ip...)
	} else if len(host) <= 255 {
		hdr = append([]byte{3, byte(len(host))}, host...) //nolint:gosec // length checked above
	} else {
		return nil, fmt.Errorf("host name of %d bytes", len(host))
	}
	return binary.BigEndian.AppendUint16(hdr, uint16(port)), nil //nolint:gosec // range checked above
}

// ssRequest sends request to target through the Shadowsocks server at addr as
// the user with this password, and returns everything it answers. A server
// that does not know the user closes the connection: the error says so.
func ssRequest(addr, password string, request []byte, target string, timeout time.Duration) ([]byte, error) {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(timeout))

	master := ssKey(password)
	salt := make([]byte, chacha20poly1305.KeySize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	enc, err := ssAEAD(master, salt)
	if err != nil {
		return nil, err
	}
	hdr, err := ssAddress(target)
	if err != nil {
		return nil, err
	}
	payload := append(hdr, request...)
	if len(payload) > 0x3fff {
		return nil, errors.New("request too long for one chunk")
	}
	var n nonce
	out := append([]byte(nil), salt...)
	out = enc.Seal(out, n.next(), binary.BigEndian.AppendUint16(nil, uint16(len(payload))), nil) //nolint:gosec // bounded above
	out = enc.Seal(out, n.next(), payload, nil)
	if _, err := c.Write(out); err != nil {
		return nil, err
	}

	// The answer: the server's own salt, then chunks of length and payload.
	br := bufio.NewReader(c)
	srvSalt := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(br, srvSalt); err != nil {
		return nil, fmt.Errorf("no answer from the server (it does not know this user?): %w", err)
	}
	dec, err := ssAEAD(master, srvSalt)
	if err != nil {
		return nil, err
	}
	var dn nonce
	var got []byte
	for {
		lenBuf := make([]byte, 2+dec.Overhead())
		if _, err := io.ReadFull(br, lenBuf); err != nil {
			if (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) && len(got) > 0 {
				return got, nil
			}
			return got, err
		}
		l, err := dec.Open(nil, dn.next(), lenBuf, nil)
		if err != nil {
			return got, fmt.Errorf("answer length: %w", err)
		}
		chunk := make([]byte, int(binary.BigEndian.Uint16(l))+dec.Overhead())
		if _, err := io.ReadFull(br, chunk); err != nil {
			return got, err
		}
		p, err := dec.Open(nil, dn.next(), chunk, nil)
		if err != nil {
			return got, fmt.Errorf("answer chunk: %w", err)
		}
		got = append(got, p...)
	}
}
