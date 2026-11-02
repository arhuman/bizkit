package davfs_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arhuman/bizkit/davfs"
)

func BenchmarkPropfindDepth1(b *testing.B) {
	files := map[string]string{"/sub/": ""}
	for i := range 100 {
		files[fmt.Sprintf("/f%03d.txt", i)] = "content"
	}
	h := davfs.New(newMemTree(files), davfs.Options{Prefix: "/dav"})
	b.ReportAllocs()
	for b.Loop() {
		req := httptest.NewRequestWithContext(b.Context(), "PROPFIND", "/dav/", nil)
		req.Header.Set("Depth", "1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMultiStatus {
			b.Fatalf("status = %d, want 207", rec.Code)
		}
	}
}
