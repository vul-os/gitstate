package calendar

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Google Calendar API v3 base.
const googleCalAPI = "https://www.googleapis.com/calendar/v3"

// dateOnly formats a day as Google's all-day "date" value (YYYY-MM-DD).
func dateOnly(t time.Time) string { return t.UTC().Format("2006-01-02") }

// googleEvent is the subset of the Google Calendar event resource we use.
type googleEvent struct {
	ID           string          `json:"id,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	Description  string          `json:"description,omitempty"`
	Start        googleEventDate `json:"start,omitempty"`
	End          googleEventDate `json:"end,omitempty"`
	Transparency string          `json:"transparency,omitempty"` // opaque = busy
	// EventType "outOfOffice" marks an OOO block; falls back gracefully if the
	// account/API rejects it (we still set transparency=opaque).
	EventType string `json:"eventType,omitempty"`
}

type googleEventDate struct {
	Date string `json:"date,omitempty"` // all-day
}

func (c *Client) googleEventBody(leave Leave) googleEvent {
	// Google all-day end date is exclusive — add a day to EndDate (inclusive).
	endExclusive := leave.EndDate.AddDate(0, 0, 1)
	return googleEvent{
		Summary:      leave.title(),
		Description:  leave.Note,
		Start:        googleEventDate{Date: dateOnly(leave.StartDate)},
		End:          googleEventDate{Date: dateOnly(endExclusive)},
		Transparency: "opaque",
		EventType:    "outOfOffice",
	}
}

func (c *Client) googlePushLeave(ctx context.Context, leave Leave, existingEventID string) (string, error) {
	calID := url.PathEscape(c.calendarID())
	body := c.googleEventBody(leave)

	var out googleEvent
	if existingEventID != "" {
		// Update in place (PUT). If the event vanished, fall through to insert.
		u := fmt.Sprintf("%s/calendars/%s/events/%s", googleCalAPI, calID, url.PathEscape(existingEventID))
		status, err := c.do(ctx, http.MethodPut, u, body, &out)
		if err == nil {
			return out.ID, nil
		}
		if status != http.StatusNotFound && status != http.StatusGone {
			return "", err
		}
	}

	u := fmt.Sprintf("%s/calendars/%s/events", googleCalAPI, calID)
	if _, err := c.do(ctx, http.MethodPost, u, body, &out); err != nil {
		// eventType=outOfOffice can be rejected on some calendars; retry without it.
		body.EventType = ""
		if _, err2 := c.do(ctx, http.MethodPost, u, body, &out); err2 != nil {
			return "", err
		}
	}
	return out.ID, nil
}

func (c *Client) googleDeleteEvent(ctx context.Context, eventID string) error {
	calID := url.PathEscape(c.calendarID())
	u := fmt.Sprintf("%s/calendars/%s/events/%s", googleCalAPI, calID, url.PathEscape(eventID))
	status, err := c.do(ctx, http.MethodDelete, u, nil, nil)
	if err != nil && status != http.StatusNotFound && status != http.StatusGone {
		return err
	}
	return nil
}

// freeBusyResponse models the calendar/v3 freeBusy result.
type freeBusyResponse struct {
	Calendars map[string]struct {
		Busy []struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		} `json:"busy"`
	} `json:"calendars"`
}

func (c *Client) googlePullBusy(ctx context.Context, from, to time.Time) ([]BusyBlock, error) {
	calID := c.calendarID()
	reqBody := map[string]any{
		"timeMin": from.UTC().Format(time.RFC3339),
		"timeMax": to.UTC().Format(time.RFC3339),
		"items":   []map[string]string{{"id": calID}},
	}
	var out freeBusyResponse
	u := googleCalAPI + "/freeBusy"
	if _, err := c.do(ctx, http.MethodPost, u, reqBody, &out); err != nil {
		return nil, err
	}

	var blocks []BusyBlock
	for _, cal := range out.Calendars {
		for _, b := range cal.Busy {
			blocks = append(blocks, BusyBlock{Start: b.Start, End: b.End, Status: "busy"})
		}
	}
	return blocks, nil
}
