package davfs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"time"

	"github.com/arhuman/bizkit"
)

// readFile is a regular file opened for reading. x/net/webdav opens every
// listed file during PROPFIND to read only its Stat, so the Tree reader is
// opened on the first Read or Seek. It keeps only the io.ReadSeekCloser
// method set, so a Tree reader's WriteTo, if any, never bypasses a
// destination's ReadFrom during COPY.
type readFile struct {
	ctx   context.Context
	fs    *fileSystem
	name  string
	entry Entry
	rc    io.ReadSeekCloser
}

func (f *readFile) reader() (io.ReadSeekCloser, error) {
	if f.rc == nil {
		rc, err := f.fs.tree.Open(f.ctx, f.name)
		if err != nil {
			return nil, f.fs.fail(f.ctx, "open", f.name, err)
		}
		f.rc = rc
	}
	return f.rc, nil
}

func (f *readFile) Read(p []byte) (int, error) {
	rc, err := f.reader()
	if err != nil {
		return 0, err
	}
	return rc.Read(p)
}

func (f *readFile) Seek(offset int64, whence int) (int64, error) {
	rc, err := f.reader()
	if err != nil {
		return 0, err
	}
	return rc.Seek(offset, whence)
}

func (f *readFile) Close() error {
	if f.rc == nil {
		return nil
	}
	return f.rc.Close()
}

func (f *readFile) Stat() (fs.FileInfo, error) { return fileInfo{f.entry}, nil }

func (f *readFile) Readdir(int) ([]fs.FileInfo, error) {
	return nil, fmt.Errorf("davfs: %q: not a directory", f.entry.Name)
}

func (f *readFile) Write([]byte) (int, error) {
	return 0, fmt.Errorf("davfs: %q: opened read-only", f.entry.Name)
}

// dirFile is a directory whose children are listed once, on the first
// Readdir, since PROPFIND opens every listed directory only to Stat it.
type dirFile struct {
	ctx     context.Context
	fs      *fileSystem
	name    string
	entry   Entry
	entries []Entry
	listed  bool
	offset  int
}

func (f *dirFile) Stat() (fs.FileInfo, error) { return fileInfo{f.entry}, nil }
func (f *dirFile) Close() error               { return nil }

func (f *dirFile) Read([]byte) (int, error) {
	return 0, fmt.Errorf("davfs: %q: is a directory", f.entry.Name)
}

func (f *dirFile) Seek(int64, int) (int64, error) {
	return 0, fmt.Errorf("davfs: %q: is a directory", f.entry.Name)
}

func (f *dirFile) Write([]byte) (int, error) {
	return 0, fmt.Errorf("davfs: %q: is a directory", f.entry.Name)
}

func (f *dirFile) list() error {
	if f.listed {
		return nil
	}
	entries, err := f.fs.tree.List(f.ctx, f.name)
	if err != nil {
		return f.fs.fail(f.ctx, "open", f.name, err)
	}
	if out := outcomeOf(f.ctx); out != nil {
		for _, e := range entries {
			out.remember(path.Join(f.name, e.Name), e)
		}
	}
	f.entries, f.listed = entries, true
	return nil
}

// Readdir returns up to count remaining children, or all of them when
// count <= 0, with io.EOF once exhausted for a positive count (http.File).
func (f *dirFile) Readdir(count int) ([]fs.FileInfo, error) {
	if err := f.list(); err != nil {
		return nil, err
	}
	if count > 0 && f.offset >= len(f.entries) {
		return nil, io.EOF
	}
	end := len(f.entries)
	if count > 0 && f.offset+count < end {
		end = f.offset + count
	}
	infos := make([]fs.FileInfo, 0, end-f.offset)
	for _, e := range f.entries[f.offset:end] {
		infos = append(infos, fileInfo{e})
	}
	f.offset = end
	return infos, nil
}

// writeFile is a file opened for writing (PUT, or a COPY destination). Write
// feeds an io.Pipe that Tree.Put reads on its own goroutine, so the body
// streams through instead of being buffered.
//
// A failed body read or a size overrun is kept in abortErr; Close then fails
// the pipe rather than closing it, so Put's reader errors instead of reaching
// io.EOF and the Tree discards the upload. x/net/webdav calls Close whether or
// not its copy failed, which is why the decision lives there.
type writeFile struct {
	ctx      context.Context
	name     string
	maxSize  int64
	pw       *io.PipeWriter
	written  int64
	putDone  chan error
	abortErr error
}

// newWriteFile starts Tree.Put on a goroutine that always ends: either Close
// closes the pipe, or Put returns early and closes the read side with its
// error, which unblocks any pending Write. It sends exactly one value on the
// buffered putDone, which Close receives.
func newWriteFile(ctx context.Context, tree Tree, name string, maxSize int64) *writeFile {
	pr, pw := io.Pipe()
	f := &writeFile{ctx: ctx, name: name, maxSize: maxSize, pw: pw, putDone: make(chan error, 1)}
	go func() {
		err := tree.Put(ctx, name, pr)
		_ = pr.CloseWithError(err) // CloseWithError never fails
		f.putDone <- err
	}()
	return f
}

func (f *writeFile) Write(p []byte) (int, error) {
	if f.maxSize > 0 && f.written+int64(len(p)) > f.maxSize {
		return 0, f.abort(fmt.Errorf("davfs: put %q: over %d bytes: %w", f.name, f.maxSize, bizkit.ErrTooLarge))
	}
	n, err := f.pw.Write(p)
	f.written += int64(n)
	if err != nil {
		return n, f.abort(fmt.Errorf("davfs: put %q: %w", f.name, err))
	}
	return n, nil
}

// ReadFrom is what io.Copy calls with the request body, which makes it the
// only place a failed body read is visible to the file.
func (f *writeFile) ReadFrom(r io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			w, werr := f.Write(buf[:n])
			total += int64(w)
			if werr != nil {
				return total, werr
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, f.abort(fmt.Errorf("davfs: put %q: read body: %w", f.name, rerr))
		}
	}
}

func (f *writeFile) abort(err error) error {
	if f.abortErr == nil {
		f.abortErr = err
	}
	return err
}

func (f *writeFile) Read([]byte) (int, error) {
	return 0, fmt.Errorf("davfs: %q: opened write-only", f.name)
}

func (f *writeFile) Seek(int64, int) (int64, error) {
	return 0, fmt.Errorf("davfs: %q: opened write-only", f.name)
}

func (f *writeFile) Readdir(int) ([]fs.FileInfo, error) {
	return nil, fmt.Errorf("davfs: %q: not a directory", f.name)
}

// Stat reports the bytes written so far; x/net/webdav calls it after the copy
// and before Close to compute the ETag.
func (f *writeFile) Stat() (fs.FileInfo, error) {
	return fileInfo{Entry{Name: path.Base(f.name), Size: f.written, ModTime: time.Now()}}, nil
}

// Close commits the upload by ending the pipe and waiting for Put, or fails
// the pipe after an aborted copy so nothing is committed.
func (f *writeFile) Close() error {
	if f.abortErr != nil {
		f.pw.CloseWithError(f.abortErr) //nolint:errcheck // CloseWithError never fails
		<-f.putDone
		record(f.ctx, f.abortErr)
		return fmt.Errorf("davfs: put %q aborted: %w", f.name, f.abortErr)
	}
	_ = f.pw.Close() // Close never fails
	if err := <-f.putDone; err != nil {
		record(f.ctx, err)
		return fmt.Errorf("davfs: put %q: %w", f.name, err)
	}
	return nil
}
