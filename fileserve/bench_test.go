package fileserve

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func BenchmarkServe(b *testing.B) {
	content := bytes.Repeat([]byte{0xAB}, 64<<10)
	meta := Meta{Name: "photo.png", ContentType: "image/png", ModTime: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	b.SetBytes(int64(len(content)))
	b.ReportAllocs()
	for b.Loop() {
		req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/f", nil)
		rec := httptest.NewRecorder()
		Serve(rec, req, meta, bytes.NewReader(content))
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d, want 200", rec.Code)
		}
	}
}
