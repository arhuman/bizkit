package s3

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/minio/minio-go/v7"

	"github.com/arhuman/bizkit"
	"github.com/arhuman/bizkit/blob"
)

func TestParseEndpoint(t *testing.T) {
	for _, tc := range []struct {
		in     string
		host   string
		secure bool
	}{
		{"", "s3.amazonaws.com", true},
		{"http://localhost:9000", "localhost:9000", false},
		{"https://s3.example.com", "s3.example.com", true},
		{"s3.example.com:9000", "s3.example.com:9000", true},
	} {
		host, secure := parseEndpoint(tc.in)
		if host != tc.host || secure != tc.secure {
			t.Errorf("parseEndpoint(%q) = %q, %v; want %q, %v", tc.in, host, secure, tc.host, tc.secure)
		}
	}
}

func TestNew(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("New without bucket: want error")
	}
	if _, err := New(Config{Endpoint: "http://bad host", Bucket: "b"}); err == nil {
		t.Error("New with an invalid endpoint: want error")
	}
	for in, want := range map[string]string{"": "", "/tenant/": "tenant/", "a/b": "a/b/"} {
		s, err := New(Config{Endpoint: "http://127.0.0.1:1", Bucket: "b", Prefix: in, AccessKeyID: "k", SecretAccessKey: "s"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if s.prefix != want {
			t.Errorf("prefix for %q = %q, want %q", in, s.prefix, want)
		}
	}
}

func TestLimitReader(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		left    int64
		want    string
		overrun bool
	}{
		{"unbounded", "0123456789", -1, "0123456789", false},
		{"exact", "0123456789", 10, "0123456789", false},
		{"under", "0123", 10, "0123", false},
		{"over", "0123456789x", 10, "0123456789", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lr := &limitReader{r: iotest.OneByteReader(strings.NewReader(tc.content)), left: tc.left}
			got, err := io.ReadAll(lr)
			if string(got) != tc.want {
				t.Errorf("read %q, want %q", got, tc.want)
			}
			if lr.overrun != tc.overrun || errors.Is(err, errOverrun) != tc.overrun {
				t.Errorf("overrun = %v, err = %v; want overrun %v", lr.overrun, err, tc.overrun)
			}
		})
	}
}

func TestLimitReader_PropagatesReadError(t *testing.T) {
	errRead := errors.New("read failed")
	for _, left := range []int64{-1, 0, 5} {
		lr := &limitReader{r: iotest.ErrReader(errRead), left: left}
		if _, err := lr.Read(make([]byte, 8)); !errors.Is(err, errRead) {
			t.Errorf("left %d: err = %v, want %v", left, err, errRead)
		}
		if lr.overrun {
			t.Errorf("left %d: a read error is not an overrun", left)
		}
	}
}

func TestWrapErr(t *testing.T) {
	missing := minio.ErrorResponse{Code: minio.NoSuchKey, StatusCode: 404}
	if err := wrapErr("stat", "k", missing); !errors.Is(err, bizkit.ErrNotFound) {
		t.Errorf("NoSuchKey err = %v, want ErrNotFound", err)
	}
	noBucket := minio.ErrorResponse{Code: minio.NoSuchBucket, StatusCode: 404}
	if err := wrapErr("stat", "k", noBucket); errors.Is(err, bizkit.ErrNotFound) {
		t.Errorf("NoSuchBucket err = %v, must not be ErrNotFound", err)
	}
}

// TestUnreachable drives every method into a transport failure, which must
// surface as a plain error: never ErrNotFound, never a success.
func TestUnreachable(t *testing.T) {
	s, err := New(Config{Endpoint: "http://127.0.0.1:1", Region: "us-east-1", Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	notFound := func(err error) bool { return err == nil || errors.Is(err, bizkit.ErrNotFound) }

	if _, err := s.Put(ctx, "k", strings.NewReader("x"), blob.PutOptions{MaxSize: 10}); notFound(err) {
		t.Errorf("Put err = %v, want a transport error", err)
	}
	if _, err := s.Open(ctx, "k"); notFound(err) {
		t.Errorf("Open err = %v, want a transport error", err)
	}
	if _, err := s.Stat(ctx, "k"); notFound(err) {
		t.Errorf("Stat err = %v, want a transport error", err)
	}
	if err := s.Delete(ctx, "k"); notFound(err) {
		t.Errorf("Delete err = %v, want a transport error", err)
	}
	if _, err := s.List(ctx, "dir/"); notFound(err) {
		t.Errorf("List err = %v, want a transport error", err)
	}
}

func TestEmptyKeyForbidden(t *testing.T) {
	s, err := New(Config{Endpoint: "http://127.0.0.1:1", Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if _, err := s.Put(ctx, "/", strings.NewReader("x"), blob.PutOptions{}); !errors.Is(err, bizkit.ErrForbidden) {
		t.Errorf("Put err = %v, want ErrForbidden", err)
	}
	if _, err := s.Open(ctx, ""); !errors.Is(err, bizkit.ErrForbidden) {
		t.Errorf("Open err = %v, want ErrForbidden", err)
	}
	if _, err := s.Stat(ctx, ".."); !errors.Is(err, bizkit.ErrForbidden) {
		t.Errorf("Stat err = %v, want ErrForbidden", err)
	}
	if err := s.Delete(ctx, ""); !errors.Is(err, bizkit.ErrForbidden) {
		t.Errorf("Delete err = %v, want ErrForbidden", err)
	}
}
