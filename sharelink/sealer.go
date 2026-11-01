package sharelink

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// ErrUnsealable reports a sealed value that cannot be opened: tampered,
// truncated, sealed under another key or bound to other associated data.
var ErrUnsealable = errors.New("sharelink: cannot open sealed value")

// Sealer encrypts and authenticates small secrets the host must recover
// later, such as a link token kept so the link can be copied again. The host
// supplies the implementation, typically backed by its vault. aad binds the
// sealed value to its context (a link or workspace id): Open must fail when
// it differs from the aad given to Seal. Open failures on bad input wrap
// ErrUnsealable.
type Sealer interface {
	Seal(ctx context.Context, plaintext, aad []byte) ([]byte, error)
	Open(ctx context.Context, sealed, aad []byte) ([]byte, error)
}

// AESGCM is a Sealer using AES-GCM with a random 96-bit nonce prepended to
// each sealed value. Keep the number of values sealed under one key well
// below 2^32. It is safe for concurrent use.
type AESGCM struct {
	aead cipher.AEAD
}

// NewAESGCM returns an AESGCM keyed with key, which must be 16, 24 or 32
// bytes (AES-128, AES-192, AES-256).
func NewAESGCM(key []byte) (*AESGCM, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("sharelink: sealer key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("sharelink: sealer: %w", err)
	}
	return &AESGCM{aead: aead}, nil
}

// Seal returns nonce || ciphertext || tag for plaintext bound to aad.
func (s *AESGCM) Seal(_ context.Context, plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize(), s.aead.NonceSize()+len(plaintext)+s.aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("sharelink: sealer nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, plaintext, aad), nil
}

// Open authenticates and decrypts sealed under aad.
func (s *AESGCM) Open(_ context.Context, sealed, aad []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n+s.aead.Overhead() {
		return nil, ErrUnsealable
	}
	pt, err := s.aead.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, ErrUnsealable
	}
	return pt, nil
}
