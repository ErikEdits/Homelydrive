package vaultcrypto

import (
	"bytes"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTripAndSeek(t *testing.T) {
	key := NewDataKey()
	for _, size := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 3*chunkSize + 17} {
		data := make([]byte, size)
		rand.Read(data)
		p := filepath.Join(t.TempDir(), "f.hdv")
		f, _ := os.Create(p)
		w, err := NewWriter(f, key)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data[:size/2])
		w.Write(data[size/2:])
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		f.Close()
		r, err := OpenFile(p, key)
		if err != nil {
			t.Fatal(err)
		}
		if r.Size() != int64(size) {
			t.Fatalf("size %d: got %d", size, r.Size())
		}
		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("size %d: roundtrip failed: %v", size, err)
		}
		if size > 10 {
			r.Seek(int64(size-10), io.SeekStart)
			tail, _ := io.ReadAll(r)
			if !bytes.Equal(tail, data[size-10:]) {
				t.Fatalf("seek failed")
			}
		}
		r.Close()
	}
}

func TestTamperAndWrongKey(t *testing.T) {
	key := NewDataKey()
	p := filepath.Join(t.TempDir(), "f.hdv")
	f, _ := os.Create(p)
	w, _ := NewWriter(f, key)
	w.Write(bytes.Repeat([]byte("x"), 2*chunkSize))
	w.Close()
	f.Close()
	if r, err := OpenFile(p, NewDataKey()); err == nil {
		_, err := io.ReadAll(r)
		r.Close()
		if err == nil {
			t.Fatal("wrong key accepted")
		}
	}
	// Truncating the final chunk must be detected.
	raw, _ := os.ReadFile(p)
	os.WriteFile(p, raw[:len(raw)-(chunkSize+tagSize)], 0o600)
	r, err := OpenFile(p, key)
	if err == nil {
		_, err := io.ReadAll(r)
		r.Close()
		if err == nil {
			t.Fatal("truncation not detected")
		}
	}
}

func TestWrapKey(t *testing.T) {
	salt := NewSalt()
	k := NewDataKey()
	sealed, _ := Seal(DeriveKEK("pw", salt), k)
	got, err := Open(DeriveKEK("pw", salt), sealed)
	if err != nil || !bytes.Equal(got, k) {
		t.Fatal("unwrap failed")
	}
	if _, err := Open(DeriveKEK("wrong", salt), sealed); err == nil {
		t.Fatal("wrong password accepted")
	}
}
