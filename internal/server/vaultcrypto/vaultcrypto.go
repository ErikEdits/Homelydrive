// Package vaultcrypto implements the encryption used by the vault (Tresor):
// a random data key wrapped with a password derived key (Argon2id), and a
// chunked AES-256-GCM file format that supports seeking (for video playback).
package vaultcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"os"

	"golang.org/x/crypto/argon2"
)

const (
	chunkSize  = 64 << 10
	tagSize    = 16
	headerSize = 12 // "HDV1" + 8 byte nonce prefix
	magic      = "HDV1"
)

var ErrWrongPassword = errors.New("wrong vault password")
var ErrCorrupt = errors.New("vault file is corrupt")

// NewSalt returns a random salt for DeriveKEK.
func NewSalt() []byte {
	b := make([]byte, 16)
	rand.Read(b)
	return b
}

// DeriveKEK derives the key-encryption key from the vault password.
func DeriveKEK(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
}

// NewDataKey returns a random 256 bit data key.
func NewDataKey() []byte {
	b := make([]byte, 32)
	rand.Read(b)
	return b
}

// Seal encrypts small data (names, wrapped keys) with a random nonce.
func Seal(key, plaintext []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	rand.Read(nonce)
	return g.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts data produced by Seal.
func Open(key, sealed []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < g.NonceSize() {
		return nil, ErrCorrupt
	}
	out, err := g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], nil)
	if err != nil {
		return nil, ErrWrongPassword
	}
	return out, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func nonceFor(prefix []byte, idx uint32) []byte {
	n := make([]byte, 12)
	copy(n, prefix)
	binary.BigEndian.PutUint32(n[8:], idx)
	return n
}

// Writer encrypts a stream into the chunked format.
type Writer struct {
	w      io.Writer
	g      cipher.AEAD
	prefix []byte
	buf    []byte
	idx    uint32
	closed bool
}

// NewWriter writes the header and returns a Writer. Close must be called.
func NewWriter(w io.Writer, key []byte) (*Writer, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	prefix := make([]byte, 8)
	rand.Read(prefix)
	if _, err := w.Write(append([]byte(magic), prefix...)); err != nil {
		return nil, err
	}
	return &Writer{w: w, g: g, prefix: prefix, buf: make([]byte, 0, chunkSize)}, nil
}

func (w *Writer) flush(final bool) error {
	ad := []byte{0}
	if final {
		ad[0] = 1
	}
	ct := w.g.Seal(nil, nonceFor(w.prefix, w.idx), w.buf, ad)
	w.idx++
	w.buf = w.buf[:0]
	_, err := w.w.Write(ct)
	return err
}

// Write encrypts p.
func (w *Writer) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		if len(w.buf) == chunkSize {
			// Only now do we know this full chunk is not the last one.
			if err := w.flush(false); err != nil {
				return n, err
			}
		}
		k := copy(w.buf[len(w.buf):chunkSize], p)
		w.buf = w.buf[:len(w.buf)+k]
		p = p[k:]
		n += k
	}
	return n, nil
}

// Close writes the final chunk.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.flush(true)
}

// Reader decrypts a chunked file with random access.
type Reader struct {
	f       *os.File
	g       cipher.AEAD
	prefix  []byte
	size    int64 // plaintext size
	nChunks int64
	pos     int64
	cached  int64
	plain   []byte
}

// OpenFile opens an encrypted file for reading.
func OpenFile(path string, key []byte) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	hdr := make([]byte, headerSize)
	if _, err := io.ReadFull(f, hdr); err != nil || string(hdr[:4]) != magic {
		f.Close()
		return nil, ErrCorrupt
	}
	g, err := gcm(key)
	if err != nil {
		f.Close()
		return nil, err
	}
	n := fi.Size() - headerSize
	if n < tagSize {
		f.Close()
		return nil, ErrCorrupt
	}
	full := int64(chunkSize + tagSize)
	nChunks := (n + full - 1) / full
	last := n - (nChunks-1)*full
	size := (nChunks-1)*chunkSize + last - tagSize
	return &Reader{f: f, g: g, prefix: hdr[4:], size: size, nChunks: nChunks, cached: -1}, nil
}

// Size returns the plaintext size.
func (r *Reader) Size() int64 { return r.size }

func (r *Reader) load(idx int64) error {
	if idx == r.cached {
		return nil
	}
	full := int64(chunkSize + tagSize)
	off := headerSize + idx*full
	length := full
	if idx == r.nChunks-1 {
		length = (r.size - idx*chunkSize) + tagSize
	}
	ct := make([]byte, length)
	if _, err := r.f.ReadAt(ct, off); err != nil && !(errors.Is(err, io.EOF) && int64(len(ct)) == length) {
		return ErrCorrupt
	}
	ad := []byte{0}
	if idx == r.nChunks-1 {
		ad[0] = 1
	}
	pt, err := r.g.Open(r.plain[:0], nonceFor(r.prefix, uint32(idx)), ct, ad)
	if err != nil {
		return ErrCorrupt
	}
	r.plain, r.cached = pt, idx
	return nil
}

// Read implements io.Reader.
func (r *Reader) Read(p []byte) (int, error) {
	if r.pos >= r.size {
		return 0, io.EOF
	}
	idx := r.pos / chunkSize
	if err := r.load(idx); err != nil {
		return 0, err
	}
	off := r.pos - idx*chunkSize
	n := copy(p, r.plain[off:])
	r.pos += int64(n)
	return n, nil
}

// Seek implements io.Seeker.
func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.pos + offset
	case io.SeekEnd:
		abs = r.size + offset
	}
	if abs < 0 {
		return 0, errors.New("negative position")
	}
	r.pos = abs
	return abs, nil
}

// Close closes the file.
func (r *Reader) Close() error { return r.f.Close() }
