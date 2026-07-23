package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/exo/gitstate/internal/auth"
)

// TestHashVerifyRoundTrip covers the happy path and confirms the encoding is
// PHC-formatted argon2id with a per-call random salt.
func TestHashVerifyRoundTrip(t *testing.T) {
	const pw = "correct horse battery staple"
	enc, err := auth.HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(enc, "$argon2id$") {
		t.Errorf("encoded hash missing argon2id prefix: %q", enc)
	}
	if err := auth.VerifyPassword(pw, enc); err != nil {
		t.Errorf("VerifyPassword on correct password: %v", err)
	}

	// A second hash of the same password must differ (random salt).
	enc2, err := auth.HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword(2): %v", err)
	}
	if enc == enc2 {
		t.Error("two hashes of the same password are identical — salt not random")
	}
	if err := auth.VerifyPassword(pw, enc2); err != nil {
		t.Errorf("VerifyPassword on second hash: %v", err)
	}
}

// TestVerifyWrongPassword covers the security-relevant failure: a wrong password
// returns the ErrPasswordMismatch sentinel (not a generic error).
func TestVerifyWrongPassword(t *testing.T) {
	enc, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	err = auth.VerifyPassword("hunter3", enc)
	if !errors.Is(err, auth.ErrPasswordMismatch) {
		t.Errorf("expected ErrPasswordMismatch, got %v", err)
	}
}

// TestVerifyMalformedHash covers corrupt/garbage stored hashes — these must
// error (and must NOT be treated as a match).
func TestVerifyMalformedHash(t *testing.T) {
	cases := []string{
		"",
		"plaintext",
		"$argon2id$v=19$bad",                   // too few parts
		"$bcrypt$v=19$m=1,t=1,p=1$AAAA$AAAA",   // wrong algorithm
		"$argon2id$v=99$m=1,t=1,p=1$AAAA$AAAA", // wrong version
		"$argon2id$v=19$nope$AAAA$AAAA",        // unparseable params
		"$argon2id$v=19$m=1,t=1,p=1$!!!$AAAA",  // bad salt base64
		"$argon2id$v=19$m=1,t=1,p=1$AAAA$!!!",  // bad hash base64
	}
	for _, enc := range cases {
		if err := auth.VerifyPassword("anything", enc); err == nil {
			t.Errorf("expected error for malformed hash %q, got nil (treated as match!)", enc)
		}
	}
}
