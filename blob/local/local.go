// Package local is the filesystem blob.Backend. Objects live as plain files
// under a root directory; keys are sanitized so no key can resolve outside it.
// It does not persist ContentType: Stat, Open and List report it empty.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/arhuman/bizkit"
	"github.com/arhuman/bizkit/blob"
)

const tempPrefix = ".bizkit-tmp-"

// Store stores objects under a root directory. It is safe for concurrent use;
// concurrent Puts to one key end with one complete object, never a mix.
type Store struct {
	root string
}

var _ blob.Backend = (*Store)(nil)

// New creates root if needed and returns a Store rooted at its absolute path.
func New(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("blob/local: create root %q: %w", root, err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("blob/local: resolve root: %w", err)
	}
	return &Store{root: abs}, nil
}

// resolve sanitizes key into an absolute path guaranteed to be under root, and
// returns the logical key (leading "/" trimmed, traversal collapsed).
func (s *Store) resolve(key string) (full, logical string, err error) {
	clean := path.Clean("/" + strings.ReplaceAll(key, "\\", "/"))
	full = filepath.Join(s.root, filepath.FromSlash(clean))
	if full != s.root && !strings.HasPrefix(full, s.root+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("blob/local: key %q escapes root: %w", key, bizkit.ErrForbidden)
	}
	return full, strings.TrimPrefix(clean, "/"), nil
}

// tempFile is the writable side of Put; tests swap createTemp to inject failures.
type tempFile interface {
	io.WriteCloser
	Name() string
}

var createTemp = func(dir string) (tempFile, error) {
	return os.CreateTemp(dir, tempPrefix+"*")
}

// Put writes r to a temporary file beside key and renames it into place, so a
// failed or oversized write never replaces or truncates an existing object.
// The empty key, and a key with any segment starting with ".bizkit-tmp-", fail
// with bizkit.ErrForbidden.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
	full, logical, err := s.resolve(key)
	if err != nil {
		return blob.Info{}, err
	}
	if err := checkPutKey(logical); err != nil {
		return blob.Info{}, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return blob.Info{}, fmt.Errorf("blob/local: mkdir for %q: %w", key, err)
	}
	f, err := createTemp(filepath.Dir(full))
	if err != nil {
		return blob.Info{}, fmt.Errorf("blob/local: create %q: %w", key, err)
	}
	tmp := f.Name()
	if err := writeLimited(f, r, opts.MaxSize); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return blob.Info{}, fmt.Errorf("blob/local: write %q: %w", key, err)
	}
	// A failed Close can mean unflushed bytes: the Put must not report success.
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return blob.Info{}, fmt.Errorf("blob/local: close %q: %w", key, err)
	}
	if err := os.Rename(tmp, full); err != nil {
		_ = os.Remove(tmp)
		return blob.Info{}, fmt.Errorf("blob/local: commit %q: %w", key, err)
	}
	info, err := s.Stat(ctx, logical)
	if err != nil {
		return blob.Info{}, err
	}
	info.ContentType = opts.ContentType
	return info, nil
}

// checkPutKey refuses the empty key and any key with a segment starting with
// tempPrefix: List skips those names as in-flight temporaries, so such an
// object would be stored yet never listed.
func checkPutKey(logical string) error {
	if logical == "" {
		return fmt.Errorf("blob/local: empty key: %w", bizkit.ErrForbidden)
	}
	for seg := range strings.SplitSeq(logical, "/") {
		if strings.HasPrefix(seg, tempPrefix) {
			return fmt.Errorf("blob/local: key %q uses the reserved prefix %q: %w", logical, tempPrefix, bizkit.ErrForbidden)
		}
	}
	return nil
}

func writeLimited(w io.Writer, r io.Reader, maxSize int64) error {
	if maxSize <= 0 {
		_, err := io.Copy(w, r)
		return err
	}
	n, err := io.Copy(w, io.LimitReader(r, maxSize+1))
	if err != nil {
		return err
	}
	if n > maxSize {
		return fmt.Errorf("exceeds max size %d bytes: %w", maxSize, bizkit.ErrTooLarge)
	}
	return nil
}

type object struct {
	*os.File
	info blob.Info
}

func (o *object) Info() blob.Info { return o.info }

// Open opens the object at key and stats it, so a missing key fails here.
func (s *Store) Open(_ context.Context, key string) (blob.Object, error) {
	full, logical, err := s.resolve(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full) // #nosec G304 -- full is sanitized by resolve, guaranteed under root
	if err != nil {
		return nil, notFoundOr(key, "open", err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("blob/local: stat %q: %w", key, err)
	}
	if st.IsDir() {
		_ = f.Close()
		return nil, fmt.Errorf("blob/local: %q is a prefix, not an object: %w", key, bizkit.ErrNotFound)
	}
	return &object{File: f, info: infoOf(logical, st)}, nil
}

// Stat returns the metadata of the object at key.
func (s *Store) Stat(_ context.Context, key string) (blob.Info, error) {
	full, logical, err := s.resolve(key)
	if err != nil {
		return blob.Info{}, err
	}
	st, err := os.Stat(full)
	if err != nil {
		return blob.Info{}, notFoundOr(key, "stat", err)
	}
	if st.IsDir() {
		return blob.Info{}, fmt.Errorf("blob/local: %q is a prefix, not an object: %w", key, bizkit.ErrNotFound)
	}
	return infoOf(logical, st), nil
}

// Delete removes the object at key; a missing key is not an error. Empty
// parent directories are left in place.
func (s *Store) Delete(_ context.Context, key string) error {
	full, logical, err := s.resolve(key)
	if err != nil {
		return err
	}
	if logical == "" {
		return fmt.Errorf("blob/local: empty key: %w", bizkit.ErrForbidden)
	}
	if err := os.Remove(full); err != nil && !missing(err) {
		return fmt.Errorf("blob/local: delete %q: %w", key, err)
	}
	return nil
}

// List returns every object whose logical key starts with prefix, sorted by
// key. In-flight Put temporaries are never listed.
func (s *Store) List(_ context.Context, prefix string) ([]blob.Info, error) {
	_, logical, err := s.resolve(prefix)
	if err != nil {
		return nil, err
	}
	dir := path.Dir(logical)
	if strings.HasSuffix(prefix, "/") && logical != "" {
		logical += "/"
		dir = strings.TrimSuffix(logical, "/")
	}
	l := lister{root: s.root, match: logical}
	if err := filepath.WalkDir(filepath.Join(s.root, filepath.FromSlash(dir)), l.visit); err != nil {
		return nil, fmt.Errorf("blob/local: list %q: %w", prefix, err)
	}
	slices.SortFunc(l.out, func(a, b blob.Info) int { return strings.Compare(a.Key, b.Key) })
	return l.out, nil
}

// lister collects, for List, the objects under root whose logical key starts
// with match.
type lister struct {
	root  string
	match string
	out   []blob.Info
}

func (l *lister) visit(p string, d fs.DirEntry, err error) error {
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if d.IsDir() || strings.HasPrefix(d.Name(), tempPrefix) {
		return nil
	}
	rel, err := filepath.Rel(l.root, p)
	if err != nil {
		return err
	}
	key := filepath.ToSlash(rel)
	if !strings.HasPrefix(key, l.match) {
		return nil
	}
	st, err := d.Info()
	if err != nil {
		return err
	}
	l.out = append(l.out, infoOf(key, st))
	return nil
}

func infoOf(key string, st fs.FileInfo) blob.Info {
	return blob.Info{Key: key, Size: st.Size(), ModTime: st.ModTime()}
}

// missing reports whether err means no object exists at the path, including a
// key nested under an existing object (ENOTDIR), which is absent, not broken.
func missing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

func notFoundOr(key, op string, err error) error {
	if missing(err) {
		return fmt.Errorf("blob/local: %s %q: %w", op, key, bizkit.ErrNotFound)
	}
	return fmt.Errorf("blob/local: %s %q: %w", op, key, err)
}
