// Package crypto — pure unit tests for AES-256-GCM round-trip, tamper detection,
// wrong-key failure, and key derivation. No DB or network required.
package crypto

import (
	"bytes"
	"crypto/sha256"
	"os"
	"testing"
)

func keyFor(s string) [32]byte { return sha256.Sum256([]byte(s)) }

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := keyFor("a-test-key")
	cases := [][]byte{
		[]byte("hello world"),
		[]byte(""),                        // empty plaintext is valid
		[]byte("ghp_secrettoken_1234567"), // token-like
		bytes.Repeat([]byte{0x00}, 100),   // all-zero bytes
		[]byte("unicode: αβγ 日本語 🚀"),
	}
	for _, pt := range cases {
		ct, err := Encrypt(pt, key)
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", pt, err)
		}
		got, err := Decrypt(ct, key)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if !bytes.Equal(got, pt) {
			t.Errorf("round-trip mismatch: got %q want %q", got, pt)
		}
	}
}

func TestEncrypt_NonceMakesCiphertextUnique(t *testing.T) {
	key := keyFor("k")
	pt := []byte("same plaintext")
	a, err := Encrypt(pt, key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encrypt(pt, key)
	if err != nil {
		t.Fatal(err)
	}
	// A random nonce means two encryptions of the same plaintext differ.
	if bytes.Equal(a, b) {
		t.Error("two encryptions produced identical ciphertext (nonce not random?)")
	}
	// The first 12 bytes are the nonce; ciphertext is longer than the plaintext.
	if len(a) <= 12 {
		t.Errorf("ciphertext too short: %d bytes", len(a))
	}
}

func TestDecrypt_WrongKeyFails(t *testing.T) {
	ct, err := Encrypt([]byte("secret"), keyFor("right-key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(ct, keyFor("wrong-key")); err == nil {
		t.Error("Decrypt with wrong key should fail (GCM auth tag)")
	}
}

func TestDecrypt_TamperDetected(t *testing.T) {
	key := keyFor("k")
	ct, err := Encrypt([]byte("important data"), key)
	if err != nil {
		t.Fatal(err)
	}
	// Flip a bit in the GCM body (past the 12-byte nonce).
	tampered := append([]byte(nil), ct...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := Decrypt(tampered, key); err == nil {
		t.Error("tampered ciphertext should fail authentication")
	}

	// Tampering with the nonce also breaks authentication.
	nonceTampered := append([]byte(nil), ct...)
	nonceTampered[0] ^= 0xFF
	if _, err := Decrypt(nonceTampered, key); err == nil {
		t.Error("tampered nonce should fail authentication")
	}
}

func TestDecrypt_TooShort(t *testing.T) {
	key := keyFor("k")
	// Anything shorter than the 12-byte nonce is rejected before AEAD open.
	for _, n := range []int{0, 1, 11} {
		if _, err := Decrypt(make([]byte, n), key); err == nil {
			t.Errorf("Decrypt(%d bytes) should error (too short)", n)
		}
	}
}

func TestKeyFromEnv(t *testing.T) {
	t.Setenv("TOKEN_ENC_KEY", "")
	if _, err := KeyFromEnv(); err != ErrKeyNotSet {
		t.Errorf("empty env → err %v, want ErrKeyNotSet", err)
	}

	t.Setenv("TOKEN_ENC_KEY", "my-random-key-value")
	got, err := KeyFromEnv()
	if err != nil {
		t.Fatalf("KeyFromEnv: %v", err)
	}
	want := sha256.Sum256([]byte("my-random-key-value"))
	if got != want {
		t.Error("KeyFromEnv did not derive SHA-256 of env value")
	}
}

func TestKeyFromEnv_RoundTripUsable(t *testing.T) {
	t.Setenv("TOKEN_ENC_KEY", "env-derived-key")
	key, err := KeyFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	ct, err := Encrypt([]byte("payload"), key)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := Decrypt(ct, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(pt) != "payload" {
		t.Errorf("env-key round-trip = %q, want payload", pt)
	}
}

// Guard: an unset env var (not just empty string) also yields ErrKeyNotSet.
func TestKeyFromEnv_Unset(t *testing.T) {
	orig, had := os.LookupEnv("TOKEN_ENC_KEY")
	os.Unsetenv("TOKEN_ENC_KEY")
	defer func() {
		if had {
			os.Setenv("TOKEN_ENC_KEY", orig)
		}
	}()
	if _, err := KeyFromEnv(); err != ErrKeyNotSet {
		t.Errorf("unset env → err %v, want ErrKeyNotSet", err)
	}
}
