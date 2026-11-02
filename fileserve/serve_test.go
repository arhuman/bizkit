package fileserve

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arhuman/bizkit/blob"
)

const body = "0123456789abcdef"

func serve(t *testing.T, method string, m Meta, header http.Header) *http.Response {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, "/f", nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	Serve(rec, req, m, strings.NewReader(body))
	return rec.Result()
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func dispositionType(res *http.Response) string {
	d := res.Header.Get("Content-Disposition")
	kind, _, _ := strings.Cut(d, ";")
	return kind
}

func TestServe_SVGAlwaysAttachment(t *testing.T) {
	for _, ct := range []string{
		"image/svg+xml",
		"IMAGE/SVG+XML",
		"image/svg+xml; charset=utf-8",
		"Image/Svg+Xml ; charset=\"utf-8\"",
	} {
		t.Run(ct, func(t *testing.T) {
			res := serve(t, http.MethodGet, Meta{Name: "logo.svg", ContentType: ct}, nil)
			defer res.Body.Close()
			if got := dispositionType(res); got != "attachment" {
				t.Fatalf("disposition = %q, want attachment", got)
			}
			if got := res.Header.Get("Content-Security-Policy"); got != sandboxCSP {
				t.Fatalf("CSP = %q, want %q", got, sandboxCSP)
			}
			if Inline(ct) {
				t.Fatalf("Inline(%q) = true", ct)
			}
		})
	}
}

func TestServe_InlineAllowlist(t *testing.T) {
	tests := []struct {
		ct      string
		wantCT  string
		wantCSP string
	}{
		{"image/png", "image/png", sandboxCSP},
		{"image/jpeg", "image/jpeg", sandboxCSP},
		{"IMAGE/GIF", "image/gif", sandboxCSP},
		{"image/webp; q=1", "image/webp", sandboxCSP},
		{"application/pdf", "application/pdf", ""},
	}
	for _, tt := range tests {
		t.Run(tt.ct, func(t *testing.T) {
			res := serve(t, http.MethodGet, Meta{Name: "f", ContentType: tt.ct}, nil)
			defer res.Body.Close()
			if got := dispositionType(res); got != "inline" {
				t.Fatalf("disposition = %q, want inline", got)
			}
			if got := res.Header.Get("Content-Type"); got != tt.wantCT {
				t.Fatalf("Content-Type = %q, want %q", got, tt.wantCT)
			}
			if got := res.Header.Get("Content-Security-Policy"); got != tt.wantCSP {
				t.Fatalf("CSP = %q, want %q", got, tt.wantCSP)
			}
			if got := readBody(t, res); got != body {
				t.Fatalf("body = %q", got)
			}
		})
	}
}

func TestServe_AttachmentDefault(t *testing.T) {
	tests := []struct {
		ct     string
		wantCT string
	}{
		{"", defaultContentType},
		{"not a type", defaultContentType},
		{"text/html", "text/html"},
		{"application/xhtml+xml", "application/xhtml+xml"},
		{"text/plain; charset=utf-8", "text/plain"},
		{"image/bmp", "image/bmp"},
		{"application/zip", "application/zip"},
		{"image/png\r\nSet-Cookie: x=1", defaultContentType},
	}
	for _, tt := range tests {
		t.Run(tt.ct, func(t *testing.T) {
			res := serve(t, http.MethodGet, Meta{Name: "f", ContentType: tt.ct}, nil)
			defer res.Body.Close()
			if got := dispositionType(res); got != "attachment" {
				t.Fatalf("disposition = %q, want attachment", got)
			}
			if got := res.Header.Get("Content-Type"); got != tt.wantCT {
				t.Fatalf("Content-Type = %q, want %q", got, tt.wantCT)
			}
			if got := res.Header.Get("Content-Security-Policy"); got != sandboxCSP {
				t.Fatalf("CSP = %q, want %q", got, sandboxCSP)
			}
			if res.Header.Get("Set-Cookie") != "" {
				t.Fatal("header injected")
			}
		})
	}
}

func TestServe_Nosniff(t *testing.T) {
	for _, ct := range []string{"image/png", "application/pdf", "text/html", ""} {
		t.Run(ct, func(t *testing.T) {
			res := serve(t, http.MethodGet, Meta{Name: "f", ContentType: ct}, nil)
			defer res.Body.Close()
			if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q", got)
			}
		})
	}
}

func TestServe_Range(t *testing.T) {
	tests := []struct {
		rng       string
		wantCode  int
		wantRange string
		wantBody  string
	}{
		{"bytes=0-3", http.StatusPartialContent, "bytes 0-3/16", "0123"},
		{"bytes=10-", http.StatusPartialContent, "bytes 10-15/16", "abcdef"},
		{"bytes=-2", http.StatusPartialContent, "bytes 14-15/16", "ef"},
		{"bytes=99-", http.StatusRequestedRangeNotSatisfiable, "bytes */16", ""},
	}
	for _, tt := range tests {
		t.Run(tt.rng, func(t *testing.T) {
			res := serve(t, http.MethodGet, Meta{Name: "doc.pdf", ContentType: "application/pdf"},
				http.Header{"Range": {tt.rng}})
			defer res.Body.Close()
			if res.StatusCode != tt.wantCode {
				t.Fatalf("status = %d, want %d", res.StatusCode, tt.wantCode)
			}
			if got := res.Header.Get("Content-Range"); got != tt.wantRange {
				t.Fatalf("Content-Range = %q, want %q", got, tt.wantRange)
			}
			if tt.wantCode == http.StatusPartialContent {
				if got := readBody(t, res); got != tt.wantBody {
					t.Fatalf("body = %q, want %q", got, tt.wantBody)
				}
			}
		})
	}
}

func TestServe_FilenameEncoding(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"report.pdf", `attachment; filename="report.pdf"; filename*=UTF-8''report.pdf`},
		{"", `attachment; filename="download"; filename*=UTF-8''download`},
		{"dir/sub\\été.txt", `attachment; filename="_t_.txt"; filename*=UTF-8''%C3%A9t%C3%A9.txt`},
		{`a"b.txt`, `attachment; filename="a_b.txt"; filename*=UTF-8''a%22b.txt`},
		{"a\r\nSet-Cookie: x=1", `attachment; filename="a__Set-Cookie: x=1"; filename*=UTF-8''a%0D%0ASet-Cookie%3A%20x%3D1`},
		{"日本.zip", `attachment; filename="__.zip"; filename*=UTF-8''%E6%97%A5%E6%9C%AC.zip`},
		{"bad\xffname", `attachment; filename="bad_name"; filename*=UTF-8''bad_name`},
		{"a\\b\x7f", `attachment; filename="b_"; filename*=UTF-8''b%7F`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := serve(t, http.MethodGet, Meta{Name: tt.name, ContentType: "application/zip"}, nil)
			defer res.Body.Close()
			got := res.Header.Get("Content-Disposition")
			if got != tt.want {
				t.Fatalf("Content-Disposition = %q, want %q", got, tt.want)
			}
			if strings.ContainsAny(got, "\r\n") || res.Header.Get("Set-Cookie") != "" {
				t.Fatal("header injected")
			}
		})
	}
}

func TestServe_Head(t *testing.T) {
	res := serve(t, http.MethodHead, Meta{Name: "a.png", ContentType: "image/png"}, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if got := res.Header.Get("Content-Length"); got != "16" {
		t.Fatalf("Content-Length = %q", got)
	}
	if got := readBody(t, res); got != "" {
		t.Fatalf("HEAD body = %q", got)
	}
	if dispositionType(res) != "inline" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers = %v", res.Header)
	}
}

func TestServe_ConditionalModTime(t *testing.T) {
	mod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	res := serve(t, http.MethodGet, Meta{Name: "a.png", ContentType: "image/png", ModTime: mod},
		http.Header{"If-Modified-Since": {mod.Format(http.TimeFormat)}})
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", res.StatusCode)
	}
}

type fakeObject struct {
	*bytes.Reader
	info blob.Info
}

func (fakeObject) Close() error      { return nil }
func (o fakeObject) Info() blob.Info { return o.info }

func TestServe_Object(t *testing.T) {
	mod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	obj := fakeObject{
		Reader: bytes.NewReader([]byte(body)),
		info:   blob.Info{Key: "ws/1/photo.webp", Size: 16, ModTime: mod, ContentType: "image/webp"},
	}
	tests := []struct {
		name string
		want string
	}{
		{"", `inline; filename="photo.webp"; filename*=UTF-8''photo.webp`},
		{"holiday.webp", `inline; filename="holiday.webp"; filename*=UTF-8''holiday.webp`},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			_, _ = obj.Seek(0, io.SeekStart)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/f", nil)
			req.Header.Set("Range", "bytes=4-5")
			rec := httptest.NewRecorder()
			ServeObject(rec, req, tt.name, obj)
			res := rec.Result()
			if got := res.Header.Get("Content-Disposition"); got != tt.want {
				t.Fatalf("Content-Disposition = %q, want %q", got, tt.want)
			}
			if res.StatusCode != http.StatusPartialContent || readBody(t, res) != "45" {
				t.Fatalf("status = %d", res.StatusCode)
			}
			if got := res.Header.Get("Last-Modified"); got != mod.Format(http.TimeFormat) {
				t.Fatalf("Last-Modified = %q", got)
			}
		})
	}
}
