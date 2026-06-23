// Package githubapp mints GitHub App credentials: a short-lived App JWT (used to
// authenticate AS the App) and per-installation access tokens (used to fetch repo
// data with the installation's own, org-owned, repo-scoped rate budget).
//
// This is the production-grade alternative to the OAuth-app data path: tokens are
// owned by the org's installation (not a connector's user account), scoped to the
// repos the org installed the App on, carry a per-org rate-limit budget, and
// survive the connector leaving. OAuth stays only for "Sign in with GitHub".
//
// The App's RSA private key is a SERVER secret — it lives in config (from the
// GITHUB_APP_PRIVATE_KEY env var), is never stored in the DB, and is never logged.
package githubapp

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	gogithub "github.com/google/go-github/v66/github"
)

// AppJWT signs a short-lived RS256 JSON Web Token authenticating as the GitHub App.
//
// Claims follow GitHub's requirements:
//   - iss = appID
//   - iat = now-60s (clock-skew tolerance; GitHub rejects tokens issued "in the future")
//   - exp = now+9m  (GitHub's hard maximum is 10 minutes)
//
// pemKey is the App's RSA private key in PEM form. The returned token is a bearer
// credential — treat it like the private key and never log it.
func AppJWT(appID, pemKey string) (string, error) {
	if appID == "" {
		return "", fmt.Errorf("githubapp: app id is empty")
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pemKey))
	if err != nil {
		return "", fmt.Errorf("githubapp: parse private key: %w", err)
	}

	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    appID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("githubapp: sign jwt: %w", err)
	}
	return signed, nil
}

// InstallationToken mints a fresh installation access token for installationID by
// authenticating as the App (App JWT) and calling CreateInstallationToken.
// Installation tokens last ~1h; the returned expiresAt lets callers cache + reuse
// the token until it is near expiry. The token is never logged.
func InstallationToken(ctx context.Context, appID, pemKey, installationID string) (token string, expiresAt time.Time, err error) {
	instID, err := parseInstallationID(installationID)
	if err != nil {
		return "", time.Time{}, err
	}
	jwtStr, err := AppJWT(appID, pemKey)
	if err != nil {
		return "", time.Time{}, err
	}

	client := gogithub.NewClient(nil).WithAuthToken(jwtStr)
	it, _, err := client.Apps.CreateInstallationToken(ctx, instID, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("githubapp: create installation token: %w", err)
	}
	return it.GetToken(), it.GetExpiresAt().Time, nil
}

// InstallationLogin returns the account login the App is installed on (the org or
// user that owns the installation) — used to label the stored connection.
func InstallationLogin(ctx context.Context, appID, pemKey, installationID string) (string, error) {
	instID, err := parseInstallationID(installationID)
	if err != nil {
		return "", err
	}
	jwtStr, err := AppJWT(appID, pemKey)
	if err != nil {
		return "", err
	}
	client := gogithub.NewClient(nil).WithAuthToken(jwtStr)
	inst, _, err := client.Apps.GetInstallation(ctx, instID)
	if err != nil {
		return "", fmt.Errorf("githubapp: get installation: %w", err)
	}
	return inst.GetAccount().GetLogin(), nil
}

// parseInstallationID parses the numeric installation id GitHub supplies as a string.
func parseInstallationID(installationID string) (int64, error) {
	var id int64
	if _, err := fmt.Sscan(installationID, &id); err != nil || id <= 0 {
		return 0, fmt.Errorf("githubapp: invalid installation id %q", installationID)
	}
	return id, nil
}
