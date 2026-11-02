package davfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"time"

	"golang.org/x/net/webdav"

	"github.com/arhuman/bizkit"
)

// errRoot refuses an operation on the mount root itself.
var errRoot = fmt.Errorf("davfs: the mount root cannot be changed: %w", bizkit.ErrForbidden)

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
		e, err := f.tree.Stat(ctx, name)
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
// 409 otherwise, then starts the streaming Put.
func (f *fileSystem) openWrite(ctx context.Context, name string) (webdav.File, error) {
	if name == "/" {
		return nil, f.fail(ctx, "put", name, errRoot)
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
	e, err := f.tree.Stat(ctx, name)
	if err != nil {
		return nil, f.fail(ctx, "stat", name, err)
	}
	return fileInfo{e}, nil
}

var rootEntry = Entry{Name: "/", IsDir: true}

// fileInfo adapts an Entry to fs.FileInfo.
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
