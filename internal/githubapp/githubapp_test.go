package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// testPEM generates a fresh RSA key and returns its PKCS#1 PEM + the public key.
func testPEM(t *testing.T) (string, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	return string(pemBytes), &key.PublicKey
}

func TestAppJWT_SignsAndParses(t *testing.T) {
	pemKey, pub := testPEM(t)

	tokenStr, err := AppJWT("123456", pemKey)
	if err != nil {
		t.Fatalf("AppJWT: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("AppJWT returned empty token")
	}

	// Parse back with the public key and verify the claims.
	claims := jwt.RegisteredClaims{}
	parsed, err := jwt.ParseWithClaims(tokenStr, &claims, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodRSA); !ok {
			t.Fatalf("unexpected signing method: %v", tok.Header["alg"])
		}
		return pub, nil
	})
	if err != nil {
		t.Fatalf("parse with public key: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("parsed token is not valid")
	}
	if claims.Issuer != "123456" {
		t.Fatalf("iss = %q, want 123456", claims.Issuer)
	}
	if claims.ExpiresAt == nil || claims.IssuedAt == nil {
		t.Fatal("exp/iat claims missing")
	}
	// exp must be within ~10 minutes of now (GitHub's hard maximum).
	if d := time.Until(claims.ExpiresAt.Time); d <= 0 || d > 10*time.Minute {
		t.Fatalf("exp out of range: %v from now", d)
	}
	// iat must be in the past (clock-skew tolerance).
	if !claims.IssuedAt.Time.Before(time.Now()) {
		t.Fatal("iat is not in the past")
	}
}

func TestAppJWT_BadKey(t *testing.T) {
	if _, err := AppJWT("123456", "not-a-pem"); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
	if _, err := AppJWT("", "whatever"); err == nil {
		t.Fatal("expected error for empty app id")
	}
}

func TestListInstallations_BadKeyErrors(t *testing.T) {
	// Can't reach GitHub; a bad PEM must fail when shaping the app-JWT client, before
	// any network call, with a clear error.
	if _, err := ListInstallations(context.Background(), "123456", "not-a-pem"); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
	if _, err := ListInstallations(context.Background(), "", "whatever"); err == nil {
		t.Fatal("expected error for empty app id")
	}
}

func TestAppClient_BuildsAuthedClient(t *testing.T) {
	pemKey, _ := testPEM(t)
	// A valid key + app id builds a client (authenticated as the App via App JWT).
	c, err := appClient("123456", pemKey)
	if err != nil {
		t.Fatalf("appClient: %v", err)
	}
	if c == nil {
		t.Fatal("appClient returned nil client")
	}
	if c.Apps == nil {
		t.Fatal("client has no Apps service")
	}
	// A bad key fails before producing a client.
	if _, err := appClient("123456", "nope"); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestTokenForOwner_BadKeyErrors(t *testing.T) {
	// The owner lookup must enumerate installations first; a bad key fails up front
	// (no network) with a clear error rather than silently returning an empty token.
	if _, err := TokenForOwner(context.Background(), "123456", "not-a-pem", "acme"); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
	if _, err := TokenForOwner(context.Background(), "", "whatever", "acme"); err == nil {
		t.Fatal("expected error for empty app id")
	}
}

// TestMatchInstallationOwner exercises the case-insensitive owner→installation match
// that TokenForOwner relies on (extracted so it can be tested without GitHub).
func TestMatchInstallationOwner(t *testing.T) {
	insts := []Installation{
		{ID: 11, Login: "cognizance-processing", Type: "Organization"},
		{ID: 22, Login: "nu-bi", Type: "Organization"},
		{ID: 33, Login: "alice", Type: "User"},
	}
	cases := []struct {
		owner  string
		wantID int64
		wantOK bool
	}{
		{"nu-bi", 22, true},
		{"NU-BI", 22, true},                       // case-insensitive
		{"Cognizance-Processing", 11, true},       // case-insensitive
		{"alice", 33, true},
		{"not-installed", 0, false},               // App not installed on this org
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := matchInstallationOwner(insts, tc.owner)
		if ok != tc.wantOK {
			t.Fatalf("owner %q: ok = %v, want %v", tc.owner, ok, tc.wantOK)
		}
		if ok && got.ID != tc.wantID {
			t.Fatalf("owner %q: id = %d, want %d", tc.owner, got.ID, tc.wantID)
		}
	}
}

func TestInstallationToken_BadKeyErrors(t *testing.T) {
	// Can't reach GitHub; a bad PEM must fail before any network call with a clear error.
	_, _, err := InstallationToken(context.Background(), "123456", "not-a-pem", "999")
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
	// A bad installation id is also rejected up front.
	pemKey, _ := testPEM(t)
	if _, _, err := InstallationToken(context.Background(), "123456", pemKey, "not-a-number"); err == nil {
		t.Fatal("expected error for invalid installation id")
	}
}
