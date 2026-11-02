// Package blob defines the byte storage contract shared by every bizkit
// backend. A Backend stores opaque objects under slash-separated logical keys;
// it knows nothing about workspaces, versions or tables, which stay with the
// host. Every backend must pass the blob/blobtest conformance suite.
package blob

import (
	"context"
	"io"
	"time"
)

// DefaultPageSize is the page size ListPage uses when limit is zero or
// negative. It is the S3 maximum for one listing request.
const DefaultPageSize = 1000

// Info describes a stored object without its content.
type Info struct {
	// Key is the logical key the backend stored the object under, after its
	// own key sanitization.
	Key string
	// Size is the object length in bytes.
	Size int64
	// ModTime is the last modification time as reported by the backend.
	ModTime time.Time
	// ContentType is the media type recorded at Put. Backends that do not
	// persist it leave it empty on Stat, Open and List.
	ContentType string
}

// PutOptions carries the metadata and limits of a single Put.
type PutOptions struct {
	// ContentType is the declared media type. It is recorded, never verified.
	ContentType string
	// MaxSize rejects content longer than this many bytes with an error
	// wrapping bizkit.ErrTooLarge. Zero means unbounded; exactly MaxSize
	// bytes is accepted.
	MaxSize int64
}

// Object is an open stored object. It is seekable so it can be handed to
// http.ServeContent for Range requests. The caller must Close it.
type Object interface {
	io.ReadSeekCloser
	// Info returns the metadata captured when the object was opened.
	Info() Info
}

// Backend is the byte storage contract.
//
// Errors for a key that does not exist wrap bizkit.ErrNotFound; errors for a
// MaxSize overrun wrap bizkit.ErrTooLarge. Callers match them with errors.Is.
// Implementations must be safe for concurrent use.
//
// A key may also be a directory prefix of other keys ("a" and "a/x"). A
// backend may store both, or refuse with an error wrapping bizkit.ErrForbidden
// the Put of "a" while "a/x" is stored, and of "a/x" while "a" is stored; each
// backend documents which. Either way the stored object is untouched.
type Backend interface {
	// Put stores the content of r under key, replacing any existing object.
	// A Put that fails, including a MaxSize overrun, leaves no partial object
	// under key.
	Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (Info, error)
	// Open returns the object at key. It checks existence before returning,
	// so a missing key fails here rather than on the first Read.
	Open(ctx context.Context, key string) (Object, error)
	// Stat returns the metadata of the object at key.
	Stat(ctx context.Context, key string) (Info, error)
	// Delete removes the object at key. Deleting a missing key, including a
	// key that is only a prefix of stored keys, is not an error and removes
	// nothing.
	Delete(ctx context.Context, key string) error
	// List returns every object whose key starts with prefix, sorted by key
	// in byte order. The match is on the key string, not on directory
	// boundaries: prefix "a" matches both "a/x" and "ab". An empty prefix
	// lists everything. It holds the whole result in memory, so it is meant
	// for prefixes known to be small; use ListPage otherwise.
	List(ctx context.Context, prefix string) ([]Info, error)
	// ListPage returns at most limit objects whose key starts with prefix and
	// is strictly greater than after, sorted by key in byte order
	// (strings.Compare). The prefix matches as in List; an empty after starts
	// at the first key, and limit <= 0 means DefaultPageSize.
	//
	// The second result is the cursor for the next call: the key of the last
	// returned object when more objects follow, "" when this page is the
	// last. Pages are stateless: between two calls an object put after the
	// cursor appears, one put before it is missed, and none is returned twice.
	ListPage(ctx context.Context, prefix, after string, limit int) ([]Info, string, error)
}
