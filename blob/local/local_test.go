package local

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/arhuman/bizkit"
	"github.com/arhuman/bizkit/blob"
	"github.com/arhuman/bizkit/blob/blobtest"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestConformance_Local(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) blob.Backend { return newStore(t) })
}

func TestNew_RootIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(file); err == nil {
		t.Fatal("New on a regular file: want error")
	}
}

func TestPut_TraversalStaysUnderRoot(t *testing.T) {
	s := newStore(t)
	info, err := s.Put(context.Background(), "../../etc/passwd", strings.NewReader("x"), blob.PutOptions{})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if info.Key != "etc/passwd" {
		t.Errorf("key = %q, want %q", info.Key, "etc/passwd")
	}
	if _, err := os.Stat(filepath.Join(s.root, "etc", "passwd")); err != nil {
		t.Errorf("object not under root: %v", err)
	}
}

func TestEmptyKeyForbidden(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, "/", strings.NewReader("x"), blob.PutOptions{}); !errors.Is(err, bizkit.ErrForbidden) {
		t.Errorf("Put empty key err = %v, want ErrForbidden", err)
	}
	if err := s.Delete(ctx, ""); !errors.Is(err, bizkit.ErrForbidden) {
		t.Errorf("Delete empty key err = %v, want ErrForbidden", err)
	}
}

func TestPrefixIsNotAnObject(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, "dir/a.txt", strings.NewReader("a"), blob.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, "dir"); !errors.Is(err, bizkit.ErrNotFound) {
		t.Errorf("Open prefix err = %v, want ErrNotFound", err)
	}
	if _, err := s.Stat(ctx, "dir"); !errors.Is(err, bizkit.ErrNotFound) {
		t.Errorf("Stat prefix err = %v, want ErrNotFound", err)
	}
}

func TestPut_ParentIsAFile(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, "a", strings.NewReader("a"), blob.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "a/b", strings.NewReader("b"), blob.PutOptions{}); err == nil {
		t.Fatal("Put under a file: want error")
	}
}

var errCloseFailed = errors.New("flush on close failed")

type failingClose struct{ tempFile }

func (f failingClose) Close() error {
	_ = f.tempFile.Close()
	return errCloseFailed
}

func TestPut_CloseFailureLeavesNoObject(t *testing.T) {
	orig := createTemp
	t.Cleanup(func() { createTemp = orig })
	createTemp = func(dir string) (tempFile, error) {
		f, err := orig(dir)
		if err != nil {
			return nil, err
		}
		return failingClose{f}, nil
	}

	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, "ws1/doc.txt", strings.NewReader("hello"), blob.PutOptions{}); !errors.Is(err, errCloseFailed) {
		t.Fatalf("Put err = %v, want %v", err, errCloseFailed)
	}
	if _, err := s.Stat(ctx, "ws1/doc.txt"); !errors.Is(err, bizkit.ErrNotFound) {
		t.Fatalf("Stat after failed Put err = %v, want ErrNotFound", err)
	}
	if got, err := s.List(ctx, ""); err != nil || len(got) != 0 {
		t.Fatalf("List after failed Put = %v, %v; want no objects and no temporaries", got, err)
	}
}

func TestPut_OversizeKeepsPreviousObject(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, "k", strings.NewReader("old"), blob.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "k", strings.NewReader("too long"), blob.PutOptions{MaxSize: 3}); !errors.Is(err, bizkit.ErrTooLarge) {
		t.Fatalf("Put err = %v, want ErrTooLarge", err)
	}
	info, err := s.Stat(ctx, "k")
	if err != nil || info.Size != 3 {
		t.Fatalf("Stat after rejected overwrite = %+v, %v; want the previous 3-byte object", info, err)
	}
}

func TestKeyUnderAnObjectIsMissing(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, "a", strings.NewReader("a"), blob.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stat(ctx, "a/b"); !errors.Is(err, bizkit.ErrNotFound) {
		t.Errorf("Stat err = %v, want ErrNotFound", err)
	}
	if _, err := s.Open(ctx, "a/b"); !errors.Is(err, bizkit.ErrNotFound) {
		t.Errorf("Open err = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, "a/b"); err != nil {
		t.Errorf("Delete err = %v, want nil", err)
	}
	if info, err := s.Stat(ctx, "a"); err != nil || info.Size != 1 {
		t.Errorf("Stat(a) = %+v, %v; want the 1-byte object untouched", info, err)
	}
}

var errInjected = errors.New("injected")

func TestPut_CreateTempFailure(t *testing.T) {
	orig := createTemp
	t.Cleanup(func() { createTemp = orig })
	createTemp = func(string) (tempFile, error) { return nil, errInjected }

	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, "k", strings.NewReader("x"), blob.PutOptions{}); !errors.Is(err, errInjected) {
		t.Fatalf("Put err = %v, want %v", err, errInjected)
	}
	if _, err := s.Stat(ctx, "k"); !errors.Is(err, bizkit.ErrNotFound) {
		t.Fatalf("Stat after failed Put err = %v, want ErrNotFound", err)
	}
}

func TestPut_ReaderErrorLeavesNoObject(t *testing.T) {
	for _, maxSize := range []int64{0, 100} {
		s := newStore(t)
		ctx := context.Background()
		r := iotest.ErrReader(errInjected)
		if _, err := s.Put(ctx, "k", r, blob.PutOptions{MaxSize: maxSize}); !errors.Is(err, errInjected) {
			t.Fatalf("MaxSize %d: Put err = %v, want %v", maxSize, err, errInjected)
		}
		entries, err := os.ReadDir(s.root)
		if err != nil || len(entries) != 0 {
			t.Fatalf("MaxSize %d: root entries = %v, %v; want none (no object, no temporary)", maxSize, entries, err)
		}
	}
}

func TestPrefixCannotBeReplacedOrDeleted(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, "dir/a.txt", strings.NewReader("a"), blob.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "dir", strings.NewReader("x"), blob.PutOptions{}); err == nil {
		t.Error("Put onto a prefix: want error")
	}
	if err := s.Delete(ctx, "dir"); err == nil || errors.Is(err, bizkit.ErrNotFound) {
		t.Errorf("Delete of a prefix err = %v, want a non-NotFound error", err)
	}
	entries, err := os.ReadDir(s.root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "dir" {
		t.Fatalf("root entries = %v, %v; want only dir (no leftover temporary)", entries, err)
	}
	if got, err := s.List(ctx, ""); err != nil || len(got) != 1 || got[0].Key != "dir/a.txt" {
		t.Fatalf("List = %v, %v; want only dir/a.txt", got, err)
	}
}

func TestUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, "locked/x", strings.NewReader("x"), blob.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(s.root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o750) })

	if _, err := s.Stat(ctx, "locked/x"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Stat err = %v, want ErrPermission", err)
	}
	if _, err := s.Open(ctx, "locked/x"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Open err = %v, want ErrPermission", err)
	}
	if _, err := s.List(ctx, ""); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("List err = %v, want ErrPermission", err)
	}
}
