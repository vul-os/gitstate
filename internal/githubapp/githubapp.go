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
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	gogithub "github.com/google/go-github/v66/github"
)

// Installation is one account/org the App is installed on. The App's private key
// can enumerate every installation — so a single App connection spans ALL the orgs
// it was installed on, not just the most recently stored installation_id.
type Installation struct {
	ID    int64  // numeric installation id (used to mint that install's token)
	Login string // the account/org login the App is installed on
	Type  string // "Organization" or "User" (GitHub's target_type)
}

// appClient builds a go-github client authenticated AS the App (App JWT). Shared by
// every "act as the App" call (ListInstallations, etc.) so they all sign the JWT the
// same way. The returned client is a bearer credential — never log it.
func appClient(appID, pemKey string) (*gogithub.Client, error) {
	jwtStr, err := AppJWT(appID, pemKey)
	if err != nil {
		return nil, err
	}
	return gogithub.NewClient(nil).WithAuthToken(jwtStr), nil
}

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
	client, err := appClient(appID, pemKey)
	if err != nil {
		return "", time.Time{}, err
	}
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
	client, err := appClient(appID, pemKey)
	if err != nil {
		return "", err
	}
	inst, _, err := client.Apps.GetInstallation(ctx, instID)
	if err != nil {
		return "", fmt.Errorf("githubapp: get installation: %w", err)
	}
	return inst.GetAccount().GetLogin(), nil
}

// ListInstallations enumerates EVERY account/org the App is installed on by
// authenticating as the App (App JWT) and paging GET /app/installations. A GitHub App
// can be installed on many accounts (each a separate installation id); this returns
// all of them so listing/sync can span every org — no per-installation storage needed.
func ListInstallations(ctx context.Context, appID, pemKey string) ([]Installation, error) {
	client, err := appClient(appID, pemKey)
	if err != nil {
		return nil, err
	}

	var out []Installation
	opts := &gogithub.ListOptions{PerPage: 100}
	for {
		insts, resp, err := client.Apps.ListInstallations(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("githubapp: list installations: %w", err)
		}
		for _, in := range insts {
			if in == nil {
				continue
			}
			out = append(out, Installation{
				ID:    in.GetID(),
				Login: in.GetAccount().GetLogin(),
				Type:  in.GetTargetType(),
			})
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// TokenForOwner mints an installation token for the installation whose account login
// matches owner (case-insensitive). Used to fetch a repo owned by a specific org with
// the RIGHT credential: a cognizance-owned repo needs cognizance's installation token,
// a nu-bi-owned repo needs nu-bi's. Errors clearly when the App is not installed on
// owner (the user must install the App on that org first).
func TokenForOwner(ctx context.Context, appID, pemKey, owner string) (string, error) {
	insts, err := ListInstallations(ctx, appID, pemKey)
	if err != nil {
		return "", err
	}
	inst, ok := matchInstallationOwner(insts, owner)
	if !ok {
		return "", fmt.Errorf("githubapp: no installation matches owner %q (install the App on that org)", owner)
	}
	tok, _, err := InstallationToken(ctx, appID, pemKey, fmt.Sprint(inst.ID))
	return tok, err
}

// matchInstallationOwner returns the installation whose account login matches owner
// (case-insensitive). ok=false when owner is empty or the App is not installed on it.
func matchInstallationOwner(insts []Installation, owner string) (Installation, bool) {
	if owner == "" {
		return Installation{}, false
	}
	for _, in := range insts {
		if strings.EqualFold(in.Login, owner) {
			return in, true
		}
	}
	return Installation{}, false
}

// AllInstallationTokens mints a token for every installation the App has, keyed by the
// installation's account login (lower-cased). Best-effort: an installation whose token
// fails to mint is skipped (with its error captured in errs) so the others still work.
func AllInstallationTokens(ctx context.Context, appID, pemKey string) (tokens map[string]string, errs map[string]error) {
	insts, err := ListInstallations(ctx, appID, pemKey)
	if err != nil {
		return nil, map[string]error{"*": err}
	}
	tokens = make(map[string]string, len(insts))
	errs = map[string]error{}
	for _, in := range insts {
		tok, _, err := InstallationToken(ctx, appID, pemKey, fmt.Sprint(in.ID))
		if err != nil {
			errs[strings.ToLower(in.Login)] = err
			continue
		}
		tokens[strings.ToLower(in.Login)] = tok
	}
	return tokens, errs
}

// parseInstallationID parses the numeric installation id GitHub supplies as a string.
func parseInstallationID(installationID string) (int64, error) {
	var id int64
	if _, err := fmt.Sscan(installationID, &id); err != nil || id <= 0 {
		return 0, fmt.Errorf("githubapp: invalid installation id %q", installationID)
	}
	return id, nil
}
