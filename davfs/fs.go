package davfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"time"

	"golang.org/x/net/webdav"

	"github.com/arhuman/bizkit"
)

// errRoot refuses an operation on the mount root itself.
var errRoot = fmt.Errorf("davfs: the mount root cannot be changed: %w", bizkit.ErrForbidden)

// errCollection refuses a PUT onto an existing directory.
var errCollection = errors.New("davfs: a collection cannot be replaced by a file")

// fileSystem adapts a Tree to webdav.FileSystem.
//
// x/net/webdav tests failures with os.IsNotExist, which does not follow
// fmt.Errorf wrapping, so a missing name is returned as an *os.PathError.
type fileSystem struct {
	tree    Tree
	maxSize int64
}

func (f *fileSystem) fail(ctx context.Context, op, name string, err error) error {
	record(ctx, err)
	if errors.Is(err, bizkit.ErrNotFound) {
		return &os.PathError{Op: op, Path: name, Err: os.ErrNotExist}
	}
	return fmt.Errorf("davfs: %s %q: %w", op, name, err)
}

func (f *fileSystem) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	name = path.Clean("/" + name)
	if name == "/" {
		return fmt.Errorf("davfs: mkdir %q: %w", name, os.ErrExist)
	}
	f.changed(ctx)
	if err := f.tree.Mkdir(ctx, name); err != nil {
		return f.fail(ctx, "mkdir", name, err)
	}
	return nil
}

func (f *fileSystem) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	name = path.Clean("/" + name)
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0 {
		return f.openWrite(ctx, name)
	}
	entry := rootEntry
	if name != "/" {
		e, err := f.stat(ctx, name)
		if err != nil {
			return nil, f.fail(ctx, "open", name, err)
		}
		entry = e
	}
	if entry.IsDir {
		return &dirFile{ctx: ctx, fs: f, name: name, entry: entry}, nil
	}
	return &readFile{ctx: ctx, fs: f, name: name, entry: entry}, nil
}

// openWrite checks the parent is an existing directory, which WebDAV answers
// 409 otherwise, and that name is not a collection, which RFC 4918 section
// 9.7.2 answers 405, then starts the streaming Put.
func (f *fileSystem) openWrite(ctx context.Context, name string) (webdav.File, error) {
	if name == "/" {
		return nil, f.fail(ctx, "put", name, errRoot)
	}
	f.changed(ctx)
	e, err := f.tree.Stat(ctx, name)
	if err != nil && !errors.Is(err, bizkit.ErrNotFound) {
		return nil, f.fail(ctx, "put", name, err)
	}
	if err == nil && e.IsDir {
		if out := outcomeOf(ctx); out != nil {
			out.set(http.StatusMethodNotAllowed)
		}
		return nil, fmt.Errorf("davfs: put %q: %w", name, errCollection)
	}
	if parent := path.Dir(name); parent != "/" {
		e, err := f.tree.Stat(ctx, parent)
		if err != nil {
			return nil, f.fail(ctx, "put", name, err)
		}
		if !e.IsDir {
			return nil, &os.PathError{Op: "put", Path: name, Err: os.ErrNotExist}
		}
	}
	return newWriteFile(ctx, f.tree, name, f.maxSize), nil
}

func (f *fileSystem) RemoveAll(ctx context.Context, name string) error {
	name = path.Clean("/" + name)
	if name == "/" {
		return f.fail(ctx, "remove", name, errRoot)
	}
	f.changed(ctx)
	if err := f.tree.Remove(ctx, name); err != nil {
		return f.fail(ctx, "remove", name, err)
	}
	return nil
}

func (f *fileSystem) Rename(ctx context.Context, oldName, newName string) error {
	oldName, newName = path.Clean("/"+oldName), path.Clean("/"+newName)
	if oldName == "/" || newName == "/" {
		return f.fail(ctx, "rename", oldName, errRoot)
	}
	f.changed(ctx)
	if err := f.tree.Rename(ctx, oldName, newName); err != nil {
		return f.fail(ctx, "rename", oldName, err)
	}
	return nil
}

func (f *fileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	name = path.Clean("/" + name)
	if name == "/" {
		return fileInfo{rootEntry}, nil
	}
	e, err := f.stat(ctx, name)
	if err != nil {
		return nil, f.fail(ctx, "stat", name, err)
	}
	return fileInfo{e}, nil
}

// stat serves name from the request's cache, filled by earlier Stat and List
// calls, before asking the Tree. Errors are never cached.
func (f *fileSystem) stat(ctx context.Context, name string) (Entry, error) {
	out := outcomeOf(ctx)
	if out != nil {
		if e, ok := out.entry(name); ok {
			return e, nil
		}
	}
	e, err := f.tree.Stat(ctx, name)
	if err == nil && out != nil {
		out.remember(name, e)
	}
	return e, err
}

// changed drops the request's cache before the Tree is modified.
func (f *fileSystem) changed(ctx context.Context) {
	if out := outcomeOf(ctx); out != nil {
		out.forget()
	}
}

var rootEntry = Entry{Name: "/", IsDir: true}

// fileInfo adapts an Entry to fs.FileInfo and webdav.ContentTyper.
type fileInfo struct {
	entry Entry
}

func (fi fileInfo) Name() string       { return fi.entry.Name }
func (fi fileInfo) Size() int64        { return fi.entry.Size }
func (fi fileInfo) ModTime() time.Time { return fi.entry.ModTime }
func (fi fileInfo) IsDir() bool        { return fi.entry.IsDir }
func (fi fileInfo) Sys() any           { return nil }

func (fi fileInfo) Mode() fs.FileMode {
	if fi.entry.IsDir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}

// ContentType returns the media type of the name's extension, else
// application/octet-stream. It never sniffs content: x/net/webdav would open
// the file to do so, a Tree.Open per extensionless file in a listing.
func (fi fileInfo) ContentType(context.Context) (string, error) {
	if t := mime.TypeByExtension(path.Ext(fi.entry.Name)); t != "" {
		return t, nil
	}
	return "application/octet-stream", nil
}
