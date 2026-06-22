package accounting

import "errors"

// ErrUnauthorized is returned by token/API calls on an HTTP 401 — the access
// token is missing, revoked or expired. Callers that hold a refresh token should
// refresh and retry once; the API handler maps an unrecoverable 401 to a 4xx so
// the user is prompted to reconnect.
var ErrUnauthorized = errors.New("accounting: unauthorized (401) — reconnect required")
