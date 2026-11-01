package sharelink

import (
	"fmt"
	"time"

	"github.com/arhuman/bizkit"
)

// Expiry policy, in days.
const (
	// DefaultDays is the lifetime of a link created without a chosen one.
	DefaultDays = 30
	// MaxDays bounds a chosen lifetime; past it only "never" remains, which
	// is admin-only.
	MaxDays = 3650
)

// ExpiryRequest is what a creator asks for: Days of lifetime (DefaultDays
// when 0) or, with Never, a link that never expires. Admin reports whether
// the creator is an administrator, the only role allowed to ask for Never.
type ExpiryRequest struct {
	Days  int
	Never bool
	Admin bool
}

// Expiry returns the expiry of req counted from now, or the zero time for a
// link that never expires. Never by a non-admin, and Days below 0 or above
// MaxDays, are errors wrapping bizkit.ErrForbidden. Days count calendar days
// in now's location, so a lifetime spans DST changes without drifting.
func Expiry(req ExpiryRequest, now time.Time) (time.Time, error) {
	if req.Never {
		if !req.Admin {
			return time.Time{}, fmt.Errorf("sharelink: only an admin may create a link that never expires: %w", bizkit.ErrForbidden)
		}
		return time.Time{}, nil
	}
	days := req.Days
	if days == 0 {
		days = DefaultDays
	}
	if days < 1 || days > MaxDays {
		return time.Time{}, fmt.Errorf("sharelink: lifetime must be 1 to %d days, got %d: %w", MaxDays, days, bizkit.ErrForbidden)
	}
	return now.AddDate(0, 0, days), nil
}

// CheckExpiry returns an error wrapping bizkit.ErrExpired when a link with
// expiresAt is expired at now, that is when now is at or past expiresAt. The
// zero expiresAt never expires.
func CheckExpiry(expiresAt, now time.Time) error {
	if expiresAt.IsZero() || now.Before(expiresAt) {
		return nil
	}
	return fmt.Errorf("sharelink: expired at %s: %w", expiresAt.UTC().Format(time.RFC3339), bizkit.ErrExpired)
}
