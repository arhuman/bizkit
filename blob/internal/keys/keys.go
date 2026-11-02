// Package keys holds the key normalization rule every blob backend applies,
// so a key maps to the same logical name on blob/local and blob/s3.
package keys

import (
	"path"
	"strings"
)

// Clean returns the logical key for key: backslashes become slashes, doubled
// slashes and dot segments are collapsed, and the leading slash is trimmed.
// A ".." never climbs above the root, so the result never starts with "..".
// The empty string means the root; callers decide whether that is a valid key.
func Clean(key string) string {
	return strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(key, "\\", "/")), "/")
}
