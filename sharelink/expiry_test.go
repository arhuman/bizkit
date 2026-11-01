package sharelink

import (
	"errors"
	"testing"
	"time"

	"github.com/arhuman/bizkit"
)

func TestExpiry_Policy(t *testing.T) {
	tests := []struct {
		name    string
		req     ExpiryRequest
		want    time.Time
		wantErr error
	}{
		{"default 30 days", ExpiryRequest{}, t0.AddDate(0, 0, 30), nil},
		{"one day", ExpiryRequest{Days: 1}, t0.AddDate(0, 0, 1), nil},
		{"maximum", ExpiryRequest{Days: 3650}, t0.AddDate(0, 0, 3650), nil},
		{"over maximum", ExpiryRequest{Days: 3651, Admin: true}, time.Time{}, bizkit.ErrForbidden},
		{"negative", ExpiryRequest{Days: -1}, time.Time{}, bizkit.ErrForbidden},
		{"never by admin", ExpiryRequest{Never: true, Admin: true}, time.Time{}, nil},
		{"never by member", ExpiryRequest{Never: true}, time.Time{}, bizkit.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Expiry(tt.req, t0)
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if !got.Equal(tt.want) {
				t.Fatalf("expiry = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExpiry_Check(t *testing.T) {
	exp := t0.Add(time.Hour)
	if err := CheckExpiry(exp, exp.Add(-time.Nanosecond)); err != nil {
		t.Fatalf("before expiry: %v", err)
	}
	if err := CheckExpiry(exp, exp); !errors.Is(err, bizkit.ErrExpired) {
		t.Fatalf("at expiry: err = %v, want ErrExpired", err)
	}
	if err := CheckExpiry(time.Time{}, t0.AddDate(100, 0, 0)); err != nil {
		t.Fatalf("never-expiring link: %v", err)
	}
}
