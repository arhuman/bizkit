// Package s3 is the blob.Backend for S3 and S3-compatible object stores
// (MinIO, Garage, AWS S3). Objects live under an optional key prefix in one
// bucket; keys are normalized by the same rule as blob/local, so a key maps to the
// same logical name on both backends. The bucket must already exist.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/arhuman/bizkit"
	"github.com/arhuman/bizkit/blob"
	"github.com/arhuman/bizkit/blob/internal/keys"
)

// partSize bounds the memory one Put buffers: content of unknown length is
// streamed as multipart parts of this size, so objects are capped at 10000
// parts (about 80 GB).
const partSize = 8 << 20

// Config carries everything needed to reach one bucket. Credentials are
// secrets: callers must never log this struct.
type Config struct {
	// Endpoint is the host[:port] of the store, optionally prefixed with
	// "http://" (TLS off, the usual local MinIO) or "https://". A bare host
	// uses TLS. Empty means AWS S3 (s3.amazonaws.com).
	Endpoint string
	// Region is the bucket region. Empty lets the client discover it.
	Region string
	// Bucket is the existing bucket every object is stored in.
	Bucket string
	// Prefix is prepended to every key, so several hosts or tenants can share
	// a bucket. Leading and trailing slashes are ignored; empty means the
	// bucket root.
	Prefix string
	// AccessKeyID and SecretAccessKey are static credentials. When both are
	// empty the client uses the instance IAM role.
	AccessKeyID     string
	SecretAccessKey string
}

// Store stores objects in one S3 bucket under a key prefix. It is safe for
// concurrent use. A Put is atomic: readers see the previous object or the new
// one, never a partial upload.
type Store struct {
	client *minio.Client
	bucket string
	prefix string
}

var _ blob.Backend = (*Store)(nil)

// New returns a Store for cfg. It does no network I/O: an unreachable
// endpoint, bad credentials or a missing bucket surface on the first call.
func New(cfg Config) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("blob/s3: empty bucket")
	}
	host, secure := parseEndpoint(cfg.Endpoint)
	creds := credentials.NewIAM("")
	if cfg.AccessKeyID != "" || cfg.SecretAccessKey != "" {
		creds = credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, "")
	}
	client, err := minio.New(host, &minio.Options{Creds: creds, Secure: secure, Region: cfg.Region})
	if err != nil {
		return nil, fmt.Errorf("blob/s3: new client: %w", err)
	}
	prefix := strings.Trim(cfg.Prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	return &Store{client: client, bucket: cfg.Bucket, prefix: prefix}, nil
}

// parseEndpoint splits an optional scheme off endpoint and decides TLS.
func parseEndpoint(endpoint string) (host string, secure bool) {
	switch {
	case endpoint == "":
		return "s3.amazonaws.com", true
	case strings.HasPrefix(endpoint, "http://"):
		return strings.TrimPrefix(endpoint, "http://"), false
	case strings.HasPrefix(endpoint, "https://"):
		return strings.TrimPrefix(endpoint, "https://"), true
	default:
		return endpoint, true
	}
}

// objectKey returns the bucket key and logical key for key, rejecting a key
// that cleans to the empty string.
func (s *Store) objectKey(key string) (full, logical string, err error) {
	logical = keys.Clean(key)
	if logical == "" {
		return "", "", fmt.Errorf("blob/s3: empty key: %w", bizkit.ErrForbidden)
	}
	return s.prefix + logical, logical, nil
}

// Put streams r to the store as a multipart upload. A failed or oversized
// upload is aborted, so it never replaces an existing object nor leaves one.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
	full, logical, err := s.objectKey(key)
	if err != nil {
		return blob.Info{}, err
	}
	lr := &limitReader{r: r, left: opts.MaxSize}
	if opts.MaxSize <= 0 {
		lr.left = -1
	}
	_, err = s.client.PutObject(ctx, s.bucket, full, lr, -1, minio.PutObjectOptions{
		ContentType: opts.ContentType,
		PartSize:    partSize,
	})
	if lr.overrun {
		return blob.Info{}, fmt.Errorf("blob/s3: put %q: exceeds max size %d bytes: %w", key, opts.MaxSize, bizkit.ErrTooLarge)
	}
	if err != nil {
		return blob.Info{}, fmt.Errorf("blob/s3: put %q: %w", key, err)
	}
	return s.Stat(ctx, logical)
}

// limitReader passes r through until more than left bytes are read, then
// fails and records the overrun. A negative left means unbounded.
type limitReader struct {
	r       io.Reader
	left    int64
	overrun bool
}

var errOverrun = errors.New("blob/s3: max size exceeded")

func (l *limitReader) Read(p []byte) (int, error) {
	if l.left < 0 {
		return l.r.Read(p)
	}
	if l.left == 0 {
		var probe [1]byte
		n, err := l.r.Read(probe[:])
		if n > 0 {
			l.overrun = true
			return 0, errOverrun
		}
		return 0, err
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	return n, err
}

type object struct {
	*minio.Object
	info blob.Info
}

func (o *object) Info() blob.Info { return o.info }

// Open stats the object at key, so a missing key fails here, then opens it
// pinned to the stat's ETag: an overwrite between the two calls makes reads
// fail instead of mixing two versions.
func (s *Store) Open(ctx context.Context, key string) (blob.Object, error) {
	full, logical, err := s.objectKey(key)
	if err != nil {
		return nil, err
	}
	st, err := s.client.StatObject(ctx, s.bucket, full, minio.StatObjectOptions{})
	if err != nil {
		return nil, wrapErr("open", key, err)
	}
	opts := minio.GetObjectOptions{}
	if err := opts.SetMatchETag(st.ETag); err != nil {
		return nil, fmt.Errorf("blob/s3: open %q: %w", key, err)
	}
	obj, err := s.client.GetObject(ctx, s.bucket, full, opts)
	if err != nil {
		return nil, wrapErr("open", key, err)
	}
	return &object{Object: obj, info: infoOf(logical, st)}, nil
}

// Stat returns the metadata of the object at key.
func (s *Store) Stat(ctx context.Context, key string) (blob.Info, error) {
	full, logical, err := s.objectKey(key)
	if err != nil {
		return blob.Info{}, err
	}
	st, err := s.client.StatObject(ctx, s.bucket, full, minio.StatObjectOptions{})
	if err != nil {
		return blob.Info{}, wrapErr("stat", key, err)
	}
	return infoOf(logical, st), nil
}

// Delete removes the object at key; a missing key is not an error.
func (s *Store) Delete(ctx context.Context, key string) error {
	full, _, err := s.objectKey(key)
	if err != nil {
		return err
	}
	if err := s.client.RemoveObject(ctx, s.bucket, full, minio.RemoveObjectOptions{}); err != nil {
		return wrapErr("delete", key, err)
	}
	return nil
}

// List returns every object whose logical key starts with prefix, sorted by
// key. Listings do not carry ContentType, so it is empty on every result.
func (s *Store) List(ctx context.Context, prefix string) ([]blob.Info, error) {
	var out []blob.Info
	for o := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: s.listPrefix(prefix), Recursive: true}) {
		if o.Err != nil {
			return nil, wrapErr("list", prefix, o.Err)
		}
		out = append(out, infoOf(strings.TrimPrefix(o.Key, s.prefix), o))
	}
	slices.SortFunc(out, func(a, b blob.Info) int { return strings.Compare(a.Key, b.Key) })
	return out, nil
}

// ListPage lists from the cursor with StartAfter and stops reading after
// limit+1 objects, relying on the store's byte order of keys.
func (s *Store) ListPage(ctx context.Context, prefix, after string, limit int) ([]blob.Info, string, error) {
	if limit <= 0 {
		limit = blob.DefaultPageSize
	}
	opts := minio.ListObjectsOptions{Prefix: s.listPrefix(prefix), Recursive: true, MaxKeys: min(limit+1, blob.DefaultPageSize)}
	if after != "" {
		opts.StartAfter = s.prefix + after
	}
	// Cancelling stops the listing goroutine, which blocks on its next send
	// once this loop stops reading.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := make([]blob.Info, 0, min(limit+1, blob.DefaultPageSize))
	for o := range s.client.ListObjects(ctx, s.bucket, opts) {
		if o.Err != nil {
			return nil, "", wrapErr("list", prefix, o.Err)
		}
		if len(out) == limit {
			return out, out[limit-1].Key, nil
		}
		out = append(out, infoOf(strings.TrimPrefix(o.Key, s.prefix), o))
	}
	return out, "", nil
}

// listPrefix returns the bucket prefix matching the logical prefix, keeping a
// trailing slash that keys.Clean would drop.
func (s *Store) listPrefix(prefix string) string {
	logical := keys.Clean(prefix)
	if strings.HasSuffix(prefix, "/") && logical != "" {
		logical += "/"
	}
	return s.prefix + logical
}

func infoOf(logical string, o minio.ObjectInfo) blob.Info {
	return blob.Info{Key: logical, Size: o.Size, ModTime: o.LastModified, ContentType: o.ContentType}
}

// wrapErr maps a NoSuchKey response to bizkit.ErrNotFound; every other
// failure, including a NoSuchBucket response, stays a plain error.
func wrapErr(op, key string, err error) error {
	if minio.ToErrorResponse(err).Code == minio.NoSuchKey {
		return fmt.Errorf("blob/s3: %s %q: %w", op, key, bizkit.ErrNotFound)
	}
	return fmt.Errorf("blob/s3: %s %q: %w", op, key, err)
}
