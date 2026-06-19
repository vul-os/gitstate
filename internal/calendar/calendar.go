// Package calendar is a provider-abstracted client for two-way leave/availability
// sync against Google Calendar and Microsoft Graph.
//
//   - PushLeave creates/updates an all-day out-of-office event on the member's
//     calendar (Google events.insert; Graph POST /me/events with showAs:oof).
//   - DeleteLeaveEvent removes it (leave rejection/cancellation).
//   - PullBusy reads OOO/busy windows so availability can reflect real calendars
//     (Google freeBusy; Graph getSchedule).
//
// The client uses the stored AES-GCM-encrypted access token, transparently
// refreshing it with the stored refresh token when expired (RefreshedToken).
// All HTTP calls are stdlib net/http with timeouts. Tokens are never logged.
package calendar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

// httpTimeout bounds every provider HTTP call.
const httpTimeout = 20 * time.Second

// Leave is the minimal leave-entry shape the client needs to push an OOO event.
type Leave struct {
	ID        string
	Kind      string    // pto | sick | holiday
	StartDate time.Time // inclusive day
	EndDate   time.Time // inclusive day
	Note      string
	Name      string // member display name, for the event title
}

// title renders the OOO event summary, e.g. "PTO — Ada Lovelace".
func (l Leave) title() string {
	label := "PTO"
	switch l.Kind {
	case "sick":
		label = "Sick leave"
	case "holiday":
		label = "Holiday"
	}
	if l.Name != "" {
		return label + " — " + l.Name
	}
	return label
}

// BusyBlock is a busy/OOO window pulled from a calendar.
type BusyBlock struct {
	Start  time.Time
	End    time.Time
	Status string // busy | oof | tentative (best-effort)
}

// Conn is the decrypted connection context a Client needs. The caller decrypts
// the stored tokens and supplies the provider's oauth2.Config so the client can
// refresh expired access tokens. CalendarID defaults to the provider's primary
// calendar when empty.
type Conn struct {
	Provider     string // google | microsoft
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	CalendarID   string
	OAuth        *oauth2.Config
}

// Client performs calendar operations for a single connection.
type Client struct {
	conn Conn
	http *http.Client
	tok  *oauth2.Token // current token (may be refreshed in-flight)
}

// New builds a Client for a decrypted connection.
func New(conn Conn) *Client {
	return &Client{
		conn: conn,
		http: &http.Client{Timeout: httpTimeout},
		tok: &oauth2.Token{
			AccessToken:  conn.AccessToken,
			RefreshToken: conn.RefreshToken,
			Expiry:       conn.Expiry,
		},
	}
}

// RefreshedToken returns the (possibly newly refreshed) token. After a sequence
// of operations the caller can persist this if AccessToken/Expiry changed, so
// the refreshed token is reused next time. Returns the zero token if unchanged.
func (c *Client) RefreshedToken() *oauth2.Token { return c.tok }

// accessToken returns a valid access token, refreshing via the refresh token if
// the current one is expired and an oauth2.Config is available.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	if c.tok.Valid() {
		return c.tok.AccessToken, nil
	}
	if c.conn.OAuth == nil || c.tok.RefreshToken == "" {
		// Can't refresh — fall back to the (possibly still-working) access token.
		if c.tok.AccessToken != "" {
			return c.tok.AccessToken, nil
		}
		return "", fmt.Errorf("calendar: access token expired and no refresh token available")
	}
	src := c.conn.OAuth.TokenSource(ctx, c.tok)
	fresh, err := src.Token()
	if err != nil {
		return "", fmt.Errorf("calendar: refresh token: %w", err)
	}
	// Preserve the refresh token if the provider didn't return a new one.
	if fresh.RefreshToken == "" {
		fresh.RefreshToken = c.tok.RefreshToken
	}
	c.tok = fresh
	return c.tok.AccessToken, nil
}

// do executes an authenticated JSON request, decoding the response into out (if
// non-nil). It returns the decoded body and status for callers needing the
// raw status (e.g. delete tolerating 404/410).
func (c *Client) do(ctx context.Context, method, url string, body any, out any) (int, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return 0, err
	}

	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("calendar: marshal body: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return 0, fmt.Errorf("calendar: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("calendar: request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("calendar: %s %s: status %d: %s", method, urlPath(url), resp.StatusCode, snippet(raw))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("calendar: decode response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// PushLeave creates or updates an all-day OOO event for a leave entry on the
// member's calendar and returns the provider event id. When existingEventID is
// non-empty the event is updated in place (so date/title edits sync).
func (c *Client) PushLeave(ctx context.Context, leave Leave, existingEventID string) (string, error) {
	switch c.conn.Provider {
	case "google":
		return c.googlePushLeave(ctx, leave, existingEventID)
	case "microsoft":
		return c.microsoftPushLeave(ctx, leave, existingEventID)
	default:
		return "", fmt.Errorf("calendar: unsupported provider %q", c.conn.Provider)
	}
}

// DeleteLeaveEvent removes a previously created OOO event. A missing event
// (404/410) is treated as success (idempotent).
func (c *Client) DeleteLeaveEvent(ctx context.Context, eventID string) error {
	if eventID == "" {
		return nil
	}
	switch c.conn.Provider {
	case "google":
		return c.googleDeleteEvent(ctx, eventID)
	case "microsoft":
		return c.microsoftDeleteEvent(ctx, eventID)
	default:
		return fmt.Errorf("calendar: unsupported provider %q", c.conn.Provider)
	}
}

// PullBusy reads busy/OOO windows in [from, to) from the member's calendar.
func (c *Client) PullBusy(ctx context.Context, from, to time.Time) ([]BusyBlock, error) {
	switch c.conn.Provider {
	case "google":
		return c.googlePullBusy(ctx, from, to)
	case "microsoft":
		return c.microsoftPullBusy(ctx, from, to)
	default:
		return nil, fmt.Errorf("calendar: unsupported provider %q", c.conn.Provider)
	}
}

func (c *Client) calendarID() string {
	if c.conn.CalendarID != "" {
		return c.conn.CalendarID
	}
	return "primary"
}
