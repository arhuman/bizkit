package sharelink

import (
	"fmt"

	"github.com/arhuman/bizkit"
	"github.com/arhuman/bizkit/fileserve"
)

// Bounds of a link's upload caps, so a typo cannot make a cap meaningless.
const (
	MaxUploadFiles = 1000
	MaxUploadBytes = 10240 << 20
)

// UploadCaps caps what guests may upload through one upload-enabled link
// over its whole life. A zero field is no cap of the link's own; the host's
// fileserve.Limits still apply per request.
type UploadCaps struct {
	MaxFiles int
	MaxBytes int64
}

// Validate returns an error wrapping bizkit.ErrForbidden when a cap is
// negative or above MaxUploadFiles or MaxUploadBytes.
func (c UploadCaps) Validate() error {
	if c.MaxFiles < 0 || c.MaxFiles > MaxUploadFiles {
		return fmt.Errorf("sharelink: file cap must be 0 to %d, got %d: %w", MaxUploadFiles, c.MaxFiles, bizkit.ErrForbidden)
	}
	if c.MaxBytes < 0 || c.MaxBytes > MaxUploadBytes {
		return fmt.Errorf("sharelink: size cap must be 0 to %d bytes, got %d: %w", int64(MaxUploadBytes), c.MaxBytes, bizkit.ErrForbidden)
	}
	return nil
}

// Limits narrows base, the host's per-request limits, to what the caps leave
// after usedFiles files and usedBytes bytes already uploaded through the
// link, so fileserve.Reader enforces the remainder while streaming. A cap
// already reached is an error wrapping bizkit.ErrTooLarge. Base fields left
// zero take their fileserve defaults before narrowing.
func (c UploadCaps) Limits(base fileserve.Limits, usedFiles int, usedBytes int64) (fileserve.Limits, error) {
	if c.MaxFiles > 0 {
		left := c.MaxFiles - usedFiles
		if left <= 0 {
			return fileserve.Limits{}, fmt.Errorf("sharelink: link file cap %d reached: %w", c.MaxFiles, bizkit.ErrTooLarge)
		}
		base.MaxFiles = min(base.WithDefaults().MaxFiles, left)
	}
	if c.MaxBytes > 0 {
		left := c.MaxBytes - usedBytes
		if left <= 0 {
			return fileserve.Limits{}, fmt.Errorf("sharelink: link size cap %d reached: %w", c.MaxBytes, bizkit.ErrTooLarge)
		}
		def := base.WithDefaults()
		base.MaxTotal = min(def.MaxTotal, left)
		base.MaxFileSize = min(def.MaxFileSize, left)
	}
	return base, nil
}
