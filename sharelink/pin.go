package sharelink

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/arhuman/bizkit"
)

// PIN policy.
const (
	// MinPINLen and MaxPINLen bound a PIN, in characters.
	MinPINLen = 4
	MaxPINLen = 32
	// MaxPINFailures consecutive wrong PINs lock a link for PINLockDuration.
	MaxPINFailures  = 5
	PINLockDuration = 15 * time.Minute
	// MinPINKeyLen is the minimum length of a PINHasher key, in bytes.
	MinPINKeyLen = 32
)

const (
	pinHashPrefix = "hs256$"
	pinSaltLen    = 16
)

// ErrWrongPIN reports a PIN that does not match. It wraps bizkit.ErrForbidden.
var ErrWrongPIN = fmt.Errorf("sharelink: wrong PIN: %w", bizkit.ErrForbidden)

// PINHasher hashes PINs with HMAC-SHA256 under a host secret key and a random
// per-PIN salt. A PIN has too little entropy for any unkeyed hash, slow or not,
// to survive an offline search, so the protection rests on the key living
// outside the database (vault or environment), not on hashing cost.
// A PINHasher is safe for concurrent use.
type PINHasher struct {
	key []byte
}

// NewPINHasher returns a PINHasher keyed with key, which must hold at least
// MinPINKeyLen bytes. Changing the key invalidates every stored PIN hash.
func NewPINHasher(key []byte) (*PINHasher, error) {
	if len(key) < MinPINKeyLen {
		return nil, fmt.Errorf("sharelink: PIN key shorter than %d bytes", MinPINKeyLen)
	}
	return &PINHasher{key: append([]byte(nil), key...)}, nil
}

// Hash returns the encoded hash of pin, "hs256$<salt>$<mac>" in unpadded
// base64url. A PIN outside MinPINLen to MaxPINLen characters is an error
// wrapping bizkit.ErrForbidden.
func (h *PINHasher) Hash(pin string) (string, error) {
	if n := utf8.RuneCountInString(pin); n < MinPINLen || n > MaxPINLen {
		return "", fmt.Errorf("sharelink: PIN must have %d to %d characters: %w", MinPINLen, MaxPINLen, bizkit.ErrForbidden)
	}
	salt := make([]byte, pinSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("sharelink: PIN salt: %w", err)
	}
	enc := base64.RawURLEncoding
	return pinHashPrefix + enc.EncodeToString(salt) + "$" + enc.EncodeToString(h.mac(salt, pin)), nil
}

// Verify reports whether pin matches encoded, comparing MACs in constant
// time. A malformed encoded hash never matches.
func (h *PINHasher) Verify(pin, encoded string) bool {
	rest, ok := strings.CutPrefix(encoded, pinHashPrefix)
	if !ok {
		return false
	}
	saltB64, macB64, ok := strings.Cut(rest, "$")
	if !ok {
		return false
	}
	salt, err1 := base64.RawURLEncoding.DecodeString(saltB64)
	want, err2 := base64.RawURLEncoding.DecodeString(macB64)
	if err1 != nil || err2 != nil || len(salt) != pinSaltLen {
		return false
	}
	return hmac.Equal(h.mac(salt, pin), want)
}

// Check is CheckPIN with Verify(attempt, pinHash) as the verification.
func (h *PINHasher) Check(st PINState, now time.Time, pinHash, attempt string) (PINState, error) {
	return CheckPIN(st, now, func() bool { return h.Verify(attempt, pinHash) })
}

func (h *PINHasher) mac(salt []byte, pin string) []byte {
	m := hmac.New(sha256.New, h.key)
	m.Write(salt)
	m.Write([]byte(pin))
	return m.Sum(nil)
}

// PINState is the lockout state of one link, persisted by the host beside
// the PIN hash: the count of consecutive wrong PINs and the end of the
// current lock, zero when never locked.
type PINState struct {
	Failures    int
	LockedUntil time.Time
}

// Locked reports whether st is locked at now.
func (st PINState) Locked(now time.Time) bool {
	return now.Before(st.LockedUntil)
}

// CheckPIN applies the lockout policy to one PIN attempt and returns the
// state the host must save, whatever the error.
//
//   - While locked (now before LockedUntil) every attempt, the right PIN
//     included, fails with an error wrapping bizkit.ErrLocked; verify is not
//     called and the state is unchanged.
//   - A match resets the state to zero and returns nil.
//   - A mismatch counts one failure and returns ErrWrongPIN; the
//     MaxPINFailures-th consecutive one instead sets LockedUntil to
//     now+PINLockDuration, resets Failures to 0 and returns ErrLocked.
//
// Once the lock window has passed, attempts are judged again with a fresh
// count, so another MaxPINFailures wrong PINs are needed to lock again.
// Failures never decay with time otherwise. verify is the host's comparison
// of the attempt against the stored hash; it must be constant-time. The host
// must load and save the state atomically (a row lock), or concurrent
// attempts can each see fewer failures than were made.
func CheckPIN(st PINState, now time.Time, verify func() bool) (PINState, error) {
	if st.Locked(now) {
		return st, fmt.Errorf("sharelink: PIN locked until %s: %w", st.LockedUntil.UTC().Format(time.RFC3339), bizkit.ErrLocked)
	}
	if verify() {
		return PINState{}, nil
	}
	st.Failures++
	if st.Failures < MaxPINFailures {
		return st, ErrWrongPIN
	}
	locked := PINState{LockedUntil: now.Add(PINLockDuration)}
	return locked, fmt.Errorf("sharelink: %d wrong PINs: %w", MaxPINFailures, bizkit.ErrLocked)
}
