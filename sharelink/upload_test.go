package sharelink

import (
	"errors"
	"testing"

	"github.com/arhuman/bizkit"
	"github.com/arhuman/bizkit/fileserve"
)

func TestUpload_Validate(t *testing.T) {
	for _, c := range []UploadCaps{{}, {MaxFiles: 1000, MaxBytes: 10240 << 20}} {
		if err := c.Validate(); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	for _, c := range []UploadCaps{{MaxFiles: -1}, {MaxFiles: 1001}, {MaxBytes: -1}, {MaxBytes: 10240<<20 + 1}} {
		if err := c.Validate(); !errors.Is(err, bizkit.ErrForbidden) {
			t.Errorf("%+v: err = %v, want ErrForbidden", c, err)
		}
	}
}

func TestUpload_LimitsNarrowsToRemainder(t *testing.T) {
	base := fileserve.Limits{MaxFiles: 10, MaxTotal: 1 << 20, MaxFieldSize: 7}
	got, err := UploadCaps{MaxFiles: 5, MaxBytes: 1000}.Limits(base, 3, 400)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxFiles != 2 || got.MaxTotal != 600 || got.MaxFileSize != 600 || got.MaxFieldSize != 7 {
		t.Fatalf("limits = %+v", got)
	}

	got, _ = UploadCaps{MaxFiles: 1000, MaxBytes: 10240 << 20}.Limits(fileserve.Limits{}, 0, 0)
	if got.MaxFiles != fileserve.DefaultMaxFiles || got.MaxTotal != fileserve.DefaultMaxTotal || got.MaxFileSize != fileserve.DefaultMaxFileSize {
		t.Fatalf("caps above defaults changed them: %+v", got)
	}

	got, _ = UploadCaps{}.Limits(base, 999, 1<<40)
	if got.MaxFiles != base.MaxFiles || got.MaxTotal != base.MaxTotal || got.MaxFileSize != base.MaxFileSize {
		t.Fatalf("no caps changed base: %+v", got)
	}
}

func TestUpload_LimitsCapReached(t *testing.T) {
	c := UploadCaps{MaxFiles: 2, MaxBytes: 100}
	if _, err := c.Limits(fileserve.Limits{}, 2, 0); !errors.Is(err, bizkit.ErrTooLarge) {
		t.Fatalf("file cap: err = %v", err)
	}
	if _, err := c.Limits(fileserve.Limits{}, 0, 100); !errors.Is(err, bizkit.ErrTooLarge) {
		t.Fatalf("size cap: err = %v", err)
	}
}
