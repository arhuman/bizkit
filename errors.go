// Package bizkit holds the sentinel errors shared by every bizkit package.
//
// Subpackages wrap these with fmt.Errorf("...: %w", err); hosts match them with
// errors.Is and translate them into their own typed errors. bizkit never
// returns a host's error type, and hosts must not match on error strings.
package bizkit

import "errors"

var (
	// ErrNotFound reports that the requested object, key or resource does not exist.
	ErrNotFound = errors.New("bizkit: not found")

	// ErrTooLarge reports that a size, count or total limit was exceeded.
	ErrTooLarge = errors.New("bizkit: too large")

	// ErrForbidden reports that a request falls outside what policy allows,
	// such as a path outside the scope or a content type that is refused.
	ErrForbidden = errors.New("bizkit: forbidden")

	// ErrExpired reports that a credential or link is past its expiry.
	ErrExpired = errors.New("bizkit: expired")

	// ErrLocked reports that a credential is temporarily locked after too many failures.
	ErrLocked = errors.New("bizkit: locked")
)
