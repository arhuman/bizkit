package fileserve

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/arhuman/bizkit"
)

// formPart is one part of a test body: a file when file is non-empty.
type formPart struct {
	name, file, ctype, body string
}

func uploadRequest(t *testing.T, parts ...formPart) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		h := textproto.MIMEHeader{}
		disp := `form-data; name="` + p.name + `"`
		if p.file != "" {
			disp += `; filename="` + p.file + `"`
		}
		h.Set("Content-Disposition", disp)
		if p.ctype != "" {
			h.Set("Content-Type", p.ctype)
		}
		w, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, p.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func newReader(t *testing.T, lim Limits, parts ...formPart) *Reader {
	t.Helper()
	u, err := NewReader(uploadRequest(t, parts...), lim)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// readAllParts reads every file part fully and returns the bodies, stopping
// at the first error.
func readAllParts(u *Reader) ([]string, error) {
	var got []string
	for {
		p, err := u.Next()
		if errors.Is(err, io.EOF) {
			return got, nil
		}
		if err != nil {
			return got, err
		}
		b, err := io.ReadAll(p)
		if err != nil {
			return got, err
		}
		got = append(got, string(b))
	}
}

func TestUpload_StreamsPartsAndFields(t *testing.T) {
	u := newReader(t, Limits{},
		formPart{name: "note", body: "hello"},
		formPart{name: "file", file: "dir/a.PDF", ctype: "Application/PDF; x=y", body: "%PDF-1"},
		formPart{name: "empty", file: "", body: ""},
		formPart{name: "file", file: "b.bin", body: "bb"},
	)
	p, err := u.Next()
	if err != nil {
		t.Fatal(err)
	}
	if p.FormName != "file" || p.FileName != "a.PDF" || p.ContentType != "application/pdf" {
		t.Errorf("part = %q %q %q", p.FormName, p.FileName, p.ContentType)
	}
	if got := u.Fields().Get("note"); got != "hello" {
		t.Errorf("note = %q, want hello", got)
	}
	p, err = u.Next()
	if err != nil {
		t.Fatal(err)
	}
	if p.FileName != "b.bin" || p.ContentType != defaultContentType {
		t.Errorf("second part = %q %q", p.FileName, p.ContentType)
	}
	if _, ok := u.Fields()["empty"]; !ok {
		t.Error("empty-filename part not recorded as a field")
	}
	if _, err := u.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestUpload_PerFileLimit(t *testing.T) {
	u := newReader(t, Limits{MaxFileSize: 10},
		formPart{name: "f", file: "ok.txt", body: "0123456789"},
		formPart{name: "f", file: "big.txt", body: "0123456789X"},
	)
	got, err := readAllParts(u)
	if !errors.Is(err, bizkit.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if len(got) != 1 || got[0] != "0123456789" {
		t.Fatalf("parts = %q, want only the 10-byte file", got)
	}
}

func TestUpload_PerFileLimitAppliesToUnreadPart(t *testing.T) {
	u := newReader(t, Limits{MaxFileSize: 4},
		formPart{name: "f", file: "big.txt", body: "0123456789"},
		formPart{name: "f", file: "next.txt", body: "x"},
	)
	if _, err := u.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Next(); !errors.Is(err, bizkit.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestUpload_CountLimit(t *testing.T) {
	u := newReader(t, Limits{MaxFiles: 2},
		formPart{name: "f", file: "1.txt", body: "1"},
		formPart{name: "f", file: "2.txt", body: "2"},
		formPart{name: "f", file: "3.txt", body: "3"},
	)
	got, err := readAllParts(u)
	if !errors.Is(err, bizkit.ErrTooLarge) || len(got) != 2 {
		t.Fatalf("parts = %q, err = %v, want 2 parts then ErrTooLarge", got, err)
	}
}

func TestUpload_TotalLimit(t *testing.T) {
	u := newReader(t, Limits{MaxFileSize: 10, MaxTotal: 15},
		formPart{name: "note", body: "abc"},
		formPart{name: "f", file: "1.txt", body: "12345678"},
		formPart{name: "f", file: "2.txt", body: "12345"},
	)
	got, err := readAllParts(u)
	if !errors.Is(err, bizkit.ErrTooLarge) || len(got) != 1 {
		t.Fatalf("parts = %q, err = %v, want 1 part then ErrTooLarge", got, err)
	}
}

func TestUpload_FieldLimits(t *testing.T) {
	u := newReader(t, Limits{MaxFieldSize: 3}, formPart{name: "note", body: "abcd"})
	if _, err := u.Next(); !errors.Is(err, bizkit.ErrTooLarge) {
		t.Fatalf("size err = %v, want ErrTooLarge", err)
	}
	u = newReader(t, Limits{MaxFields: 1}, formPart{name: "a", body: "1"}, formPart{name: "b", body: "2"})
	if _, err := u.Next(); !errors.Is(err, bizkit.ErrTooLarge) {
		t.Fatalf("count err = %v, want ErrTooLarge", err)
	}
}

func TestUpload_BlockedExtension(t *testing.T) {
	blocked := []string{"shell.php", "SHELL.PhP", "x.php.jpg", "x.jpg.php", "run.exe.", "a.tar.SH ", ".htaccess"}
	for _, name := range blocked {
		t.Run(name, func(t *testing.T) {
			u := newReader(t, Limits{}, formPart{name: "f", file: name, body: "x"})
			if _, err := u.Next(); !errors.Is(err, bizkit.ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
		})
	}
	allowed := []string{"photo.jpg", "report.final.pdf", "phpinfo.txt", "noext"}
	for _, name := range allowed {
		t.Run(name, func(t *testing.T) {
			u := newReader(t, Limits{}, formPart{name: "f", file: name, body: "x"})
			if _, err := u.Next(); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}

func TestUpload_CustomBlocklist(t *testing.T) {
	u := newReader(t, Limits{BlockedExts: []string{".PDF"}},
		formPart{name: "f", file: "a.php", body: "x"},
		formPart{name: "f", file: "b.pdf", body: "x"},
	)
	if _, err := u.Next(); err != nil {
		t.Fatalf("custom list must replace the default: %v", err)
	}
	if _, err := u.Next(); !errors.Is(err, bizkit.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestUpload_RejectsNonMultipart(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/upload", strings.NewReader("a=b"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := NewReader(req, Limits{}); !errors.Is(err, http.ErrNotMultipart) {
		t.Fatalf("err = %v, want ErrNotMultipart", err)
	}
}

func TestUpload_MalformedBody(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/upload", strings.NewReader("--xyz\r\nno colon here\r\n\r\nbody\r\n--xyz--\r\n"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")
	u, err := NewReader(req, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Next(); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want a multipart error", err)
	}
}

// TestUpload_StreamsWithoutBuffering proves Next hands out a part before its
// body is complete: the writer holds the rest of the part until the test has
// read the first chunk, which would deadlock if anything buffered it whole.
func TestUpload_StreamsWithoutBuffering(t *testing.T) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	proceed := make(chan struct{})
	chunk := bytes.Repeat([]byte("a"), 4096)
	go func() {
		w, err := mw.CreateFormFile("file", "big.bin")
		if err == nil {
			_, err = w.Write(chunk)
		}
		<-proceed
		for i := 0; err == nil && i < 256; i++ {
			_, err = w.Write(chunk)
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/upload", pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	u, err := NewReader(req, Limits{})
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		p   *Part
		err error
	}
	done := make(chan result, 1)
	go func() {
		p, err := u.Next()
		if err == nil {
			_, err = io.ReadFull(p, make([]byte, len(chunk)))
		}
		done <- result{p, err}
	}()
	var res result
	select {
	case res = <-done:
	case <-time.After(5 * time.Second):
		close(proceed)
		t.Fatal("first chunk not delivered before the part completed")
	}
	close(proceed)
	if res.err != nil {
		t.Fatal(res.err)
	}
	n, err := io.Copy(io.Discard, res.p)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(256 * len(chunk)); n != want {
		t.Fatalf("rest = %d bytes, want %d", n, want)
	}
}
