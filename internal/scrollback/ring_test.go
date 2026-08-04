package scrollback

import (
	"bytes"
	"testing"
)

func TestRingWriteAndBytes(t *testing.T) {
	r := New(8)
	if _, err := r.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got := string(r.Bytes()); got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestRingOverwrite(t *testing.T) {
	r := New(4)
	if _, err := r.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	got := r.Bytes()
	if string(got) != "cdef" {
		t.Fatalf("got %q, want %q", got, "cdef")
	}
	if r.Len() != 4 {
		t.Fatalf("len=%d, want 4", r.Len())
	}
}

func TestRingExactCapacity(t *testing.T) {
	r := New(5)
	if _, err := r.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r.Bytes(), []byte("12345")) {
		t.Fatalf("got %q", r.Bytes())
	}
	if _, err := r.Write([]byte("67")); err != nil {
		t.Fatal(err)
	}
	if got := string(r.Bytes()); got != "34567" {
		t.Fatalf("got %q, want %q", got, "34567")
	}
}
