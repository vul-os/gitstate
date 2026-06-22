package accounting

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// tokenResponse is the standard OAuth2 token endpoint response shape shared by
// Xero and QuickBooks.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // seconds
	TokenType    string `json:"token_type"`
}

func (t tokenResponse) tokens() Tokens {
	out := Tokens{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken}
	if t.ExpiresIn > 0 {
		out.Expiry = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
	return out
}

// basicAuthHeader builds the "Basic base64(id:secret)" value used by both
// providers' token endpoints (client credentials are sent in the Authorization
// header, never the body — keeps the secret out of any logged form body).
func basicAuthHeader(clientID, clientSecret string) string {
	raw := clientID + ":" + clientSecret
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
}

// postForm posts an application/x-www-form-urlencoded body to a token endpoint
// with the given Authorization header, decoding the JSON token response. The
// caller's ctx bounds the request timeout. Tokens are never logged; on a non-2xx
// status the (bounded) body is returned in the error for diagnostics — token
// endpoints do not echo secrets on error.
func postForm(ctx context.Context, endpoint, authHeader string, form url.Values) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("accounting: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("accounting: token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode == http.StatusUnauthorized {
		return tokenResponse{}, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenResponse{}, fmt.Errorf("accounting: token status %d: %s", resp.StatusCode, snippet(body))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return tokenResponse{}, fmt.Errorf("accounting: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return tokenResponse{}, fmt.Errorf("accounting: token response missing access_token")
	}
	return tr, nil
}

// doJSON performs an authenticated JSON API request (Bearer token) and decodes
// the response into out. A 401 maps to ErrUnauthorized so callers can refresh +
// retry. extraHeaders lets a provider add e.g. Xero-tenant-id.
func doJSON(ctx context.Context, method, endpoint, accessToken string, reqBody any, out any, extraHeaders map[string]string) error {
	var bodyReader io.Reader
	if reqBody != nil {
		raw, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("accounting: marshal request: %w", err)
		}
		bodyReader = strings.NewReader(string(raw))
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return fmt.Errorf("accounting: build api request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("accounting: api request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("accounting: api status %d: %s", resp.StatusCode, snippet(respBody))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("accounting: decode api response: %w", err)
		}
	}
	return nil
}

// snippet returns a short, single-line excerpt of a response body for error
// messages (never used on success paths; token endpoints don't echo secrets on
// error). Bounded so it cannot dump a large body into logs.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
