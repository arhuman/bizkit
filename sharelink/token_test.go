package sharelink

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestToken_ShapeAndUniqueness(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if a == b {
		t.Fatal("two tokens are equal")
	}
	if len(a) != 43 || !ValidToken(a) {
		t.Fatalf("token %q: len %d, valid %v", a, len(a), ValidToken(a))
	}
	for _, bad := range []string{"", "short", a + "A", a[:42] + "+", a[:42] + "="} {
		if ValidToken(bad) {
			t.Errorf("ValidToken(%q) = true", bad)
		}
	}
}

func TestToken_HashAndMatch(t *testing.T) {
	tok, _ := NewToken()
	sum := sha256.Sum256([]byte(tok))
	want := hex.EncodeToString(sum[:])
	if got := HashToken(tok); got != want {
		t.Fatalf("HashToken = %q, want %q", got, want)
	}
	if !MatchToken(tok, want) {
		t.Fatal("MatchToken rejects its own hash")
	}
	other, _ := NewToken()
	if MatchToken(other, want) || MatchToken(tok, want[:63]) {
		t.Fatal("MatchToken accepts a wrong token or a truncated hash")
	}
}
