package sharelink

import (
	"errors"
	"reflect"
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
	tests := []struct {
		name       string
		caps       UploadCaps
		base       fileserve.Limits
		usedFiles  int
		usedBytes  int64
		wantLimits fileserve.Limits
	}{
		{
			name: "remainder below base", caps: UploadCaps{MaxFiles: 5, MaxBytes: 1000}, base: base,
			usedFiles: 3, usedBytes: 400,
			wantLimits: fileserve.Limits{MaxFiles: 2, MaxTotal: 600, MaxFileSize: 600, MaxFieldSize: 7},
		},
		{
			name: "caps above defaults keep them", caps: UploadCaps{MaxFiles: 1000, MaxBytes: 10240 << 20},
			wantLimits: fileserve.Limits{
				MaxFiles: fileserve.DefaultMaxFiles, MaxTotal: fileserve.DefaultMaxTotal, MaxFileSize: fileserve.DefaultMaxFileSize,
			},
		},
		{
			name: "no caps keep base", base: base, usedFiles: 999, usedBytes: 1 << 40,
			wantLimits: base,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.caps.Limits(tt.base, tt.usedFiles, tt.usedBytes)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.wantLimits) {
				t.Fatalf("limits = %+v, want %+v", got, tt.wantLimits)
			}
		})
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
