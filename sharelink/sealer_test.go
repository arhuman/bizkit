package sharelink

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

var _ Sealer = (*AESGCM)(nil)

func TestSealer_RoundTripAndTamper(t *testing.T) {
	ctx := context.Background()
	s, err := NewAESGCM(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	pt, aad := []byte("token-value"), []byte("link-1")
	sealed, err := s.Seal(ctx, pt, aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, pt) {
		t.Fatal("sealed value contains the plaintext")
	}
	again, _ := s.Seal(ctx, pt, aad)
	if bytes.Equal(sealed, again) {
		t.Fatal("nonce reused")
	}
	got, err := s.Open(ctx, sealed, aad)
	if err != nil || !bytes.Equal(got, pt) {
		t.Fatalf("Open = %q, %v", got, err)
	}

	other, _ := NewAESGCM(bytes.Repeat([]byte{8}, 32))
	flipped := bytes.Clone(sealed)
	flipped[len(flipped)-1] ^= 1
	cases := map[string]func() ([]byte, error){
		"tampered":  func() ([]byte, error) { return s.Open(ctx, flipped, aad) },
		"other aad": func() ([]byte, error) { return s.Open(ctx, sealed, []byte("link-2")) },
		"other key": func() ([]byte, error) { return other.Open(ctx, sealed, aad) },
		"truncated": func() ([]byte, error) { return s.Open(ctx, sealed[:10], aad) },
	}
	for name, open := range cases {
		if _, err := open(); !errors.Is(err, ErrUnsealable) {
			t.Errorf("%s: err = %v, want ErrUnsealable", name, err)
		}
	}
}

func TestSealer_BadKey(t *testing.T) {
	if _, err := NewAESGCM(make([]byte, 10)); err == nil {
		t.Fatal("10-byte key accepted")
	}
}
