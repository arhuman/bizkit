package local

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/arhuman/bizkit/blob"
)

func BenchmarkLocalPutOpen(b *testing.B) {
	store, err := New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	content := bytes.Repeat([]byte{0xAB}, 64<<10)
	b.SetBytes(int64(len(content)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := store.Put(b.Context(), "dir/object.bin", bytes.NewReader(content), blob.PutOptions{}); err != nil {
			b.Fatal(err)
		}
		obj, err := store.Open(b.Context(), "dir/object.bin")
		if err != nil {
			b.Fatal(err)
		}
		n, err := io.Copy(io.Discard, obj)
		_ = obj.Close()
		if err != nil || n != int64(len(content)) {
			b.Fatalf("read %d bytes, err %v, want %d", n, err, len(content))
		}
	}
}

// BenchmarkLocalListPage pages through 1000 objects spread over 10
// directories, 100 per page: ADR-0001 accepts one subtree walk per page.
func BenchmarkLocalListPage(b *testing.B) {
	store, err := New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	const dirs, perDir, pageSize = 10, 100, 100
	for d := range dirs {
		for i := range perDir {
			key := fmt.Sprintf("tree/d%02d/object-%03d", d, i)
			if _, err := store.Put(b.Context(), key, bytes.NewReader([]byte("x")), blob.PutOptions{}); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		if total := listAllPages(b, store, "tree/", pageSize); total != dirs*perDir {
			b.Fatalf("listed %d objects, want %d", total, dirs*perDir)
		}
	}
}

func listAllPages(b *testing.B, store *Store, prefix string, limit int) int {
	b.Helper()
	total, after := 0, ""
	for {
		page, next, err := store.ListPage(b.Context(), prefix, after, limit)
		if err != nil {
			b.Fatal(err)
		}
		total += len(page)
		if next == "" {
			return total
		}
		after = next
	}
}
