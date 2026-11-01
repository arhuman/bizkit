//go:build s3

// These tests need a live S3-compatible server; `make test-s3` starts a
// disposable MinIO from docker-compose.test.yml and runs them with -tags s3.

package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/arhuman/bizkit"
	"github.com/arhuman/bizkit/blob"
	"github.com/arhuman/bizkit/blob/blobtest"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// newStore returns a Store under a prefix unique to t, in a bucket created on
// demand. Objects left under the prefix are removed on cleanup.
func newStore(t *testing.T) *Store {
	t.Helper()
	cfg := Config{
		Endpoint:        env("BIZKIT_S3_ENDPOINT", "http://127.0.0.1:19000"),
		Region:          "us-east-1",
		Bucket:          env("BIZKIT_S3_BUCKET", "bizkit-test"),
		Prefix:          fmt.Sprintf("%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano()),
		AccessKeyID:     env("BIZKIT_S3_ACCESS_KEY", "bizkit"),
		SecretAccessKey: env("BIZKIT_S3_SECRET_KEY", "bizkit-test-secret"),
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	exists, err := s.client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		t.Fatalf("BucketExists(%s) at %s: %v", cfg.Bucket, cfg.Endpoint, err)
	}
	if !exists {
		err := s.client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region})
		if err != nil && minio.ToErrorResponse(err).Code != "BucketAlreadyOwnedByYou" {
			t.Fatalf("MakeBucket: %v", err)
		}
	}
	t.Cleanup(func() {
		for o := range s.client.ListObjects(ctx, cfg.Bucket, minio.ListObjectsOptions{Prefix: s.prefix, Recursive: true}) {
			if o.Err == nil {
				_ = s.client.RemoveObject(ctx, cfg.Bucket, o.Key, minio.RemoveObjectOptions{})
			}
		}
	})
	return s
}

func TestConformance_S3(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) blob.Backend { return newStore(t) })
}

func TestS3_ContentTypeRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, "a.pdf", strings.NewReader("%PDF"), blob.PutOptions{ContentType: "application/pdf"}); err != nil {
		t.Fatal(err)
	}
	info, err := s.Stat(ctx, "a.pdf")
	if err != nil || info.ContentType != "application/pdf" {
		t.Fatalf("Stat = %+v, %v; want ContentType application/pdf", info, err)
	}
}

func TestS3_ReaderErrorKeepsPreviousObject(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, "k", strings.NewReader("old"), blob.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	errRead := errors.New("read failed")
	if _, err := s.Put(ctx, "k", iotest.ErrReader(errRead), blob.PutOptions{}); !errors.Is(err, errRead) {
		t.Fatalf("Put err = %v, want %v", err, errRead)
	}
	if _, err := s.Put(ctx, "fresh", iotest.ErrReader(errRead), blob.PutOptions{}); !errors.Is(err, errRead) {
		t.Fatalf("Put err = %v, want %v", err, errRead)
	}
	if _, err := s.Stat(ctx, "fresh"); !errors.Is(err, bizkit.ErrNotFound) {
		t.Fatalf("Stat after failed Put err = %v, want ErrNotFound", err)
	}
	info, err := s.Stat(ctx, "k")
	if err != nil || info.Size != 3 {
		t.Fatalf("Stat after failed overwrite = %+v, %v; want the previous 3-byte object", info, err)
	}
}

// TestS3_MultipartOverrunIsAborted overruns MaxSize after a first part was
// uploaded: the upload must be aborted, leaving no object and no parts.
func TestS3_MultipartOverrunIsAborted(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	data := bytes.Repeat([]byte("x"), 2*partSize+1)
	_, err := s.Put(ctx, "big.bin", bytes.NewReader(data), blob.PutOptions{MaxSize: partSize + 1})
	if !errors.Is(err, bizkit.ErrTooLarge) {
		t.Fatalf("Put err = %v, want ErrTooLarge", err)
	}
	if _, err := s.Stat(ctx, "big.bin"); !errors.Is(err, bizkit.ErrNotFound) {
		t.Fatalf("Stat after rejected Put err = %v, want ErrNotFound", err)
	}
	for u := range s.client.ListIncompleteUploads(ctx, s.bucket, s.prefix, true) {
		t.Errorf("incomplete upload left behind: %s (err %v)", u.Key, u.Err)
	}
}

func TestS3_PrefixIsolatesStores(t *testing.T) {
	a, b := newStore(t), newStore(t)
	ctx := context.Background()
	if _, err := a.Put(ctx, "same.txt", strings.NewReader("a"), blob.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Stat(ctx, "same.txt"); !errors.Is(err, bizkit.ErrNotFound) {
		t.Errorf("other prefix Stat err = %v, want ErrNotFound", err)
	}
	if got, err := b.List(ctx, ""); err != nil || len(got) != 0 {
		t.Errorf("other prefix List = %v, %v; want empty", got, err)
	}
	got, err := a.List(ctx, "")
	if err != nil || len(got) != 1 || got[0].Key != "same.txt" {
		t.Errorf("List = %v, %v; want only same.txt without the store prefix", got, err)
	}
}
