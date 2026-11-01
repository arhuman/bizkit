// Package sharelink holds the stateless policy of public share links: token
// generation and hashing, PIN hashing and lockout, expiry policy, upload caps
// and the Sealer contract.
//
// sharelink owns no persistence. The host stores, in its own share_links
// table, the token hash (never the token), the PIN hash, the PINState and the
// expiry, loads them for each request and saves what the functions here
// return. Every time-dependent function takes now as a parameter.
package sharelink

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// TokenBytes is the entropy of a token, 256 bits, before encoding.
const TokenBytes = 32

// NewToken returns a fresh token: TokenBytes from crypto/rand in unpadded
// base64url, 43 characters safe in a URL path. Show it to the creator once and
// persist only HashToken of it (or a sealed copy, see Sealer).
func NewToken() (string, error) {
	b := make([]byte, TokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("sharelink: token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ValidToken reports whether token has the shape NewToken produces. Check it
// before hashing so a malformed token never reaches the store.
func ValidToken(token string) bool {
	b, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(b) == TokenBytes
}

// HashToken returns the lower-case hex SHA-256 of token, the lookup key the
// host stores. A plain hash suffices: the token carries 256 bits of entropy.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// MatchToken reports whether token hashes to storedHash, comparing in
// constant time. Use it when a candidate row is compared in memory rather
// than found by an indexed hash lookup.
func MatchToken(token, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(storedHash)) == 1
}
