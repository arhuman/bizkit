package local

import (
	"bytes"
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
