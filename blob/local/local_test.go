package local

import (
	"context"
	"errors"
	"fmt"
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

func TestPut_ReservedTempPrefixForbidden(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, key := range []string{tempPrefix + "x", "a/" + tempPrefix + "x", "a/" + tempPrefix + "d/b"} {
		if _, err := s.Put(ctx, key, strings.NewReader("x"), blob.PutOptions{}); !errors.Is(err, bizkit.ErrForbidden) {
			t.Errorf("Put(%q) err = %v, want ErrForbidden", key, err)
		}
		if _, err := s.Stat(ctx, key); !errors.Is(err, bizkit.ErrNotFound) {
			t.Errorf("Stat(%q) err = %v, want ErrNotFound", key, err)
		}
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
	if _, err := s.Put(ctx, "a/b", strings.NewReader("b"), blob.PutOptions{}); !errors.Is(err, bizkit.ErrForbidden) {
		t.Fatalf("Put under a file err = %v, want ErrForbidden", err)
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
	if _, err := s.Put(ctx, "dir", strings.NewReader("x"), blob.PutOptions{}); !errors.Is(err, bizkit.ErrForbidden) {
		t.Errorf("Put onto a prefix err = %v, want ErrForbidden", err)
	}
	if err := s.Delete(ctx, "dir"); err != nil {
		t.Errorf("Delete of a prefix err = %v, want nil", err)
	}
	requireOnlyObject(t, s, "dir", "dir/a.txt")
}

// requireOnlyObject fails unless key is the store's single object and dir
// its single root entry, which rules out a leftover temporary.
func requireOnlyObject(t *testing.T, s *Store, dir, key string) {
	t.Helper()
	entries, err := os.ReadDir(s.root)
	if err != nil || len(entries) != 1 || entries[0].Name() != dir {
		t.Fatalf("root entries = %v, %v; want only %s (no leftover temporary)", entries, err, dir)
	}
	if got, err := s.List(t.Context(), ""); err != nil || len(got) != 1 || got[0].Key != key {
		t.Fatalf("List = %v, %v; want only %s", got, err, key)
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

// TestListPage_TrimsWhileWalking pages with a limit small enough that the
// walk trims its candidates several times per page and skips directories
// before the cursor. The walk visits "w/dN/" before "w/dN-a", which sorts
// first, so a trim that truncates before sorting loses keys.
func TestListPage_TrimsWhileWalking(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	var keys []string
	for d := range 4 {
		keys = append(keys, fmt.Sprintf("w/d%d-a", d))
		for f := range 5 {
			keys = append(keys, fmt.Sprintf("w/d%d/f%d", d, f))
		}
	}
	for _, key := range keys {
		if _, err := s.Put(ctx, key, strings.NewReader(key), blob.PutOptions{}); err != nil {
			t.Fatalf("Put(%q): %v", key, err)
		}
	}
	all, err := s.List(ctx, "w/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	paged := pageAll(t, s, "w/", 2, len(all))
	if strings.Join(paged, ",") != strings.Join(infoKeys(all), ",") {
		t.Errorf("paged keys = %v, want %v", paged, infoKeys(all))
	}
}

// pageAll follows the ListPage cursor for at most maxPages calls and returns
// every key it saw.
func pageAll(t *testing.T, s *Store, prefix string, limit, maxPages int) []string {
	t.Helper()
	var keys []string
	after := ""
	for range maxPages {
		page, next, err := s.ListPage(context.Background(), prefix, after, limit)
		if err != nil {
			t.Fatalf("ListPage(after %q): %v", after, err)
		}
		keys = append(keys, infoKeys(page)...)
		if next == "" {
			break
		}
		after = next
	}
	return keys
}

func infoKeys(infos []blob.Info) []string {
	keys := make([]string, 0, len(infos))
	for _, info := range infos {
		keys = append(keys, info.Key)
	}
	return keys
}
