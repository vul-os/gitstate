package calendar

import (
	"encoding/json"
	"io"
	"net/url"
)

// urlPath returns just the path component of a URL for error messages (so query
// strings — which can contain identifiers — aren't echoed into logs).
func urlPath(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Path
	}
	return raw
}

// snippet truncates a response body for safe inclusion in an error message.
func snippet(b []byte) string {
	const max = 240
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}

// decodeJSON decodes r into v.
func decodeJSON(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}
