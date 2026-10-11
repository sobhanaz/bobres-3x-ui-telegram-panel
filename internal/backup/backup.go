// Package backup reads and writes BOBRES backup files: an encrypted archive
// that holds the services' master keys and a database dump, so the file plus
// BOBRES_BACKUP_KEY is enough to restore.
//
// File format (version 1):
//
//	header (117 bytes, plaintext):
//	  magic      "BOBRESBK"           8
//	  version    1                    1
//	  salt       random               32
//	  prefix     random nonce prefix  8
//	  created    unix seconds, BE     8
//	  wrapNonce  random               12
//	  wrappedKey AES-GCM(file key)    48 (32 + tag)
//	chunks: AES-256-GCM under the file key, 64 KiB of plaintext each
//
// The file key is random per file. It is wrapped with a key derived from
// BOBRES_BACKUP_KEY by HKDF-SHA256 (salt from the header), with every header
// byte before wrapNonce as additional data, so any change to the header fails
// the unwrap. Each chunk's nonce is prefix || 4-byte big-endian counter, and
// its additional data is one flag byte: 1 on the last chunk, 0 otherwise.
// Reordered, dropped or cut chunks fail to open, and a file cut at a chunk
// boundary fails because its new last chunk was not sealed as last. Only the
// last chunk may be shorter than 64 KiB; it is empty only when the whole
// stream is.
//
// Inside the decrypted stream there are two members, back to back: a 4-byte
// big-endian length followed by that many bytes of keys JSON (a flat object
// of env var names to values), then the dump, which runs to the end of the
// stream. One length prefix is enough because the dump is always last.
//
// Both directions stream: memory use is a few chunks, whatever the dump size.
package backup

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

var (
	// ErrFormat marks a file that is not a backup, or is cut inside the header.
	ErrFormat = errors.New("backup: not a backup file")
	// ErrVersion marks a backup written by a newer, unknown format.
	ErrVersion = errors.New("backup: unsupported format version")
	// ErrKey marks a wrong backup key or a damaged header; the two look the same.
	ErrKey = errors.New("backup: wrong backup key or damaged header")
	// ErrCorrupt marks a damaged, cut or reordered body.
	ErrCorrupt = errors.New("backup: file is damaged or incomplete")
)

const (
	magic     = "BOBRESBK"
	version   = 1
	saltLen   = 32
	prefixLen = 8
	keyLen    = 32
	tagLen    = 16
	nonceLen  = 12
	// aadLen is the header part covered by the key wrap.
	aadLen    = len(magic) + 1 + saltLen + prefixLen + 8
	headerLen = aadLen + nonceLen + keyLen + tagLen

	chunkSize = 64 << 10
	// MinKeyLen is the shortest BOBRES_BACKUP_KEY accepted.
	MinKeyLen = 32
	// maxKeysLen caps the keys member so a bad length cannot ask for gigabytes.
	maxKeysLen = 1 << 20
	wrapInfo   = "bobres:backup:wrap:v1"
)

// Write encrypts keys and then the whole of dump into w.
func Write(w io.Writer, key []byte, keys map[string]string, dump io.Reader) error {
	if len(key) < MinKeyLen {
		return fmt.Errorf("backup: key must be at least %d bytes", MinKeyLen)
	}
	keysJSON, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("backup: encode keys: %w", err)
	}
	if len(keysJSON) > maxKeysLen {
		return fmt.Errorf("backup: keys too large")
	}

	hdr := make([]byte, headerLen)
	copy(hdr, magic)
	hdr[len(magic)] = version
	salt := hdr[len(magic)+1 : len(magic)+1+saltLen]
	prefix := hdr[len(magic)+1+saltLen : len(magic)+1+saltLen+prefixLen]
	binary.BigEndian.PutUint64(hdr[aadLen-8:aadLen], uint64(time.Now().Unix())) // #nosec G115 -- current time is positive
	wrapNonce := hdr[aadLen : aadLen+nonceLen]
	fileKey := make([]byte, keyLen)
	for _, b := range [][]byte{salt, prefix, wrapNonce, fileKey} {
		if _, err := crand.Read(b); err != nil {
			return fmt.Errorf("backup: random: %w", err)
		}
	}
	wrap, err := wrapAEAD(key, salt)
	if err != nil {
		return err
	}
	wrap.Seal(hdr[:aadLen+nonceLen], wrapNonce, fileKey, hdr[:aadLen])
	if _, err := w.Write(hdr); err != nil {
		return fmt.Errorf("backup: write header: %w", err)
	}

	gcm, err := newGCM(fileKey)
	if err != nil {
		return err
	}
	cw := &chunkWriter{w: w, gcm: gcm, buf: make([]byte, 0, chunkSize), out: make([]byte, 0, chunkSize+tagLen)}
	copy(cw.nonce[:], prefix)

	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(keysJSON))) // #nosec G115 -- capped at maxKeysLen above
	if _, err := cw.Write(n[:]); err != nil {
		return err
	}
	if _, err := cw.Write(keysJSON); err != nil {
		return err
	}
	if _, err := io.Copy(cw, dump); err != nil {
		return fmt.Errorf("backup: copy dump: %w", err)
	}
	return cw.flush(true)
}

// chunkWriter seals 64 KiB chunks. It holds a full chunk back until more data
// arrives, so the final flush always knows which chunk is last.
type chunkWriter struct {
	w       io.Writer
	gcm     cipher.AEAD
	nonce   [nonceLen]byte
	counter uint64
	buf     []byte
	out     []byte
}

func (c *chunkWriter) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		if len(c.buf) == chunkSize {
			if err := c.flush(false); err != nil {
				return n, err
			}
		}
		k := copy(c.buf[len(c.buf):chunkSize], p)
		c.buf = c.buf[:len(c.buf)+k]
		p = p[k:]
		n += k
	}
	return n, nil
}

func (c *chunkWriter) flush(last bool) error {
	if c.counter > math.MaxUint32 {
		return fmt.Errorf("backup: stream too long")
	}
	binary.BigEndian.PutUint32(c.nonce[prefixLen:], uint32(c.counter))
	c.out = c.gcm.Seal(c.out[:0], c.nonce[:], c.buf, flag(last))
	if _, err := c.w.Write(c.out); err != nil {
		return fmt.Errorf("backup: write chunk: %w", err)
	}
	c.counter++
	c.buf = c.buf[:0]
	return nil
}

// Reader gives the keys and the dump of an opened backup.
type Reader struct {
	created time.Time
	keys    map[string]string
	body    *chunkReader
}

// Open reads the header and the keys member. The dump is read later through
// Dump; nothing past the keys is read until then.
func Open(r io.Reader, key []byte) (*Reader, error) {
	if len(key) < MinKeyLen {
		return nil, fmt.Errorf("backup: key must be at least %d bytes", MinKeyLen)
	}
	src := bufio.NewReaderSize(r, chunkSize+tagLen)
	hdr := make([]byte, headerLen)
	if _, err := io.ReadFull(src, hdr); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrFormat
		}
		return nil, fmt.Errorf("backup: read header: %w", err)
	}
	if string(hdr[:len(magic)]) != magic {
		return nil, ErrFormat
	}
	if hdr[len(magic)] != version {
		return nil, ErrVersion
	}
	salt := hdr[len(magic)+1 : len(magic)+1+saltLen]
	prefix := hdr[len(magic)+1+saltLen : len(magic)+1+saltLen+prefixLen]
	created := int64(binary.BigEndian.Uint64(hdr[aadLen-8 : aadLen])) // #nosec G115 -- authenticated by the wrap
	wrap, err := wrapAEAD(key, salt)
	if err != nil {
		return nil, err
	}
	fileKey, err := wrap.Open(nil, hdr[aadLen:aadLen+nonceLen], hdr[aadLen+nonceLen:], hdr[:aadLen])
	if err != nil {
		return nil, ErrKey
	}
	gcm, err := newGCM(fileKey)
	if err != nil {
		return nil, err
	}
	body := &chunkReader{src: src, gcm: gcm, ct: make([]byte, chunkSize+tagLen), plain: make([]byte, 0, chunkSize)}
	copy(body.nonce[:], prefix)

	var n [4]byte
	if _, err := io.ReadFull(body, n[:]); err != nil {
		return nil, bodyErr(err)
	}
	size := binary.BigEndian.Uint32(n[:])
	if size > maxKeysLen {
		return nil, ErrCorrupt
	}
	keysJSON := make([]byte, size)
	if _, err := io.ReadFull(body, keysJSON); err != nil {
		return nil, bodyErr(err)
	}
	var keys map[string]string
	if err := json.Unmarshal(keysJSON, &keys); err != nil {
		return nil, ErrCorrupt
	}
	return &Reader{created: time.Unix(created, 0).UTC(), keys: keys, body: body}, nil
}

// Created is when the backup was written.
func (r *Reader) Created() time.Time { return r.created }

// Keys returns the master keys stored in the backup.
func (r *Reader) Keys() map[string]string { return r.keys }

// Dump streams the dump. It returns io.EOF only after the last chunk has been
// authenticated; any damage or truncation is an ErrCorrupt error instead.
func (r *Reader) Dump() io.Reader { return r.body }

// Summary describes a verified backup. It holds key names, never values.
type Summary struct {
	Created  time.Time
	KeyNames []string
	DumpSize int64
}

// Verify reads a whole backup and checks every chunk.
func Verify(r io.Reader, key []byte) (Summary, error) {
	br, err := Open(r, key)
	if err != nil {
		return Summary{}, err
	}
	n, err := io.Copy(io.Discard, br.Dump())
	if err != nil {
		return Summary{}, err
	}
	s := Summary{Created: br.created, DumpSize: n}
	for k := range br.keys {
		s.KeyNames = append(s.KeyNames, k)
	}
	return s, nil
}

// chunkReader opens chunks one at a time. Errors are sticky.
type chunkReader struct {
	src     *bufio.Reader
	gcm     cipher.AEAD
	nonce   [nonceLen]byte
	counter uint64
	ct      []byte
	plain   []byte
	pending []byte
	done    bool
	err     error
}

func (c *chunkReader) Read(p []byte) (int, error) {
	for len(c.pending) == 0 {
		if c.err != nil {
			return 0, c.err
		}
		if c.done {
			return 0, io.EOF
		}
		if err := c.next(); err != nil {
			c.err = err
			return 0, err
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *chunkReader) next() error {
	if c.counter > math.MaxUint32 {
		return ErrCorrupt
	}
	n, err := io.ReadFull(c.src, c.ct)
	last := false
	switch {
	case err == nil:
		// A full chunk is the last one only if the file ends right after it.
		if _, perr := c.src.Peek(1); errors.Is(perr, io.EOF) {
			last = true
		} else if perr != nil {
			return fmt.Errorf("backup: read: %w", perr)
		}
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		last = true
	default:
		return fmt.Errorf("backup: read: %w", err)
	}
	if n < tagLen {
		return ErrCorrupt
	}
	binary.BigEndian.PutUint32(c.nonce[prefixLen:], uint32(c.counter))
	plain, err := c.gcm.Open(c.plain[:0], c.nonce[:], c.ct[:n], flag(last))
	if err != nil {
		return ErrCorrupt
	}
	if last && len(plain) == 0 && c.counter != 0 {
		return ErrCorrupt
	}
	c.counter++
	c.pending = plain
	c.done = last
	return nil
}

// bodyErr maps an early end of the decrypted stream to ErrCorrupt.
func bodyErr(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrCorrupt
	}
	return err
}

func flag(last bool) []byte {
	if last {
		return []byte{1}
	}
	return []byte{0}
}

func wrapAEAD(key, salt []byte) (cipher.AEAD, error) {
	k, err := hkdf.Key(sha256.New, key, salt, wrapInfo, keyLen)
	if err != nil {
		return nil, fmt.Errorf("backup: derive key: %w", err)
	}
	return newGCM(k)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("backup: gcm: %w", err)
	}
	return gcm, nil
}
