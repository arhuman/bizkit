package sharelink

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/arhuman/bizkit"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func newHasher(t *testing.T) *PINHasher {
	t.Helper()
	h, err := NewPINHasher([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestPIN_LocksAfterFiveFailures(t *testing.T) {
	h := newHasher(t)
	hash, err := h.Hash("1234")
	if err != nil {
		t.Fatal(err)
	}
	locked := PINState{LockedUntil: t0.Add(15 * time.Minute)}
	type step struct {
		name      string
		at        time.Duration
		pin       string
		wantErr   error
		wantState PINState
	}
	var steps []step
	for i := 1; i < 5; i++ {
		steps = append(steps, step{fmt.Sprintf("failure %d", i), 0, "0000", ErrWrongPIN, PINState{Failures: i}})
	}
	steps = append(steps,
		step{"5th failure locks", 0, "0000", bizkit.ErrLocked, locked},
		step{"right PIN during lock", 15*time.Minute - time.Second, "1234", bizkit.ErrLocked, locked},
		step{"right PIN after lock", 15 * time.Minute, "1234", nil, PINState{}},
	)
	// Steps share st, so a failed step stops the sequence.
	var st PINState
	for _, s := range steps {
		ok := t.Run(s.name, func(t *testing.T) {
			st, err = h.Check(st, t0.Add(s.at), hash, s.pin)
			if !errors.Is(err, s.wantErr) || (s.wantErr == ErrWrongPIN && errors.Is(err, bizkit.ErrLocked)) {
				t.Fatalf("err = %v, want %v only", err, s.wantErr)
			}
			if st != s.wantState {
				t.Fatalf("state = %+v, want %+v", st, s.wantState)
			}
		})
		if !ok {
			return
		}
	}
}

func TestPIN_SuccessResetsFailures(t *testing.T) {
	h := newHasher(t)
	hash, _ := h.Hash("secret-pin")
	st := PINState{Failures: 4}
	st, err := h.Check(st, t0, hash, "secret-pin")
	if err != nil || st.Failures != 0 {
		t.Fatalf("err = %v state = %+v", err, st)
	}
}

func TestPIN_LockExpiryGivesFreshCount(t *testing.T) {
	h := newHasher(t)
	hash, _ := h.Hash("1234")
	st := PINState{LockedUntil: t0}
	st, err := h.Check(st, t0, hash, "9999")
	if !errors.Is(err, ErrWrongPIN) || st.Failures != 1 {
		t.Fatalf("first failure after lock: err = %v state = %+v", err, st)
	}
	if !errors.Is(err, bizkit.ErrForbidden) {
		t.Fatalf("ErrWrongPIN must wrap ErrForbidden: %v", err)
	}
}

func TestPIN_LockedSkipsVerify(t *testing.T) {
	called := false
	_, err := CheckPIN(PINState{LockedUntil: t0.Add(time.Minute)}, t0, func() bool { called = true; return true })
	if !errors.Is(err, bizkit.ErrLocked) || called {
		t.Fatalf("err = %v, verify called = %v", err, called)
	}
}

func TestPIN_HashAndVerify(t *testing.T) {
	h := newHasher(t)
	a, err := h.Hash("1234")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := h.Hash("1234")
	if a == b {
		t.Fatal("two hashes of one PIN are equal: salt missing")
	}
	if strings.Contains(a, "1234") || !h.Verify("1234", a) || h.Verify("1235", a) {
		t.Fatalf("hash %q verifies wrongly", a)
	}
	other, _ := NewPINHasher([]byte(strings.Repeat("x", 32)))
	if other.Verify("1234", a) {
		t.Fatal("hash verified under another key")
	}
	for _, bad := range []string{"", "hs256$", "argon2$x$y", "hs256$!!$AAAA", "hs256$AAAA$AAAA", a + "x"} {
		if h.Verify("1234", bad) {
			t.Errorf("malformed hash %q verified", bad)
		}
	}
}

func TestPIN_HashRejectsLength(t *testing.T) {
	h := newHasher(t)
	for _, pin := range []string{"123", strings.Repeat("9", 33)} {
		if _, err := h.Hash(pin); !errors.Is(err, bizkit.ErrForbidden) {
			t.Errorf("Hash(%d chars) err = %v, want ErrForbidden", len(pin), err)
		}
	}
	if _, err := h.Hash("ééééé"); err != nil {
		t.Errorf("5-rune PIN refused: %v", err)
	}
}

func TestPIN_ShortKeyRefused(t *testing.T) {
	if _, err := NewPINHasher(make([]byte, 31)); err == nil {
		t.Fatal("short key accepted")
	}
}
