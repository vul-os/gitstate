package calendar

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Microsoft Graph v1.0 base.
const graphAPI = "https://graph.microsoft.com/v1.0"

// graphDateTimeUTC formats a time as Graph's dateTimeTimeZone value pair.
func graphDateTimeUTC(t time.Time) map[string]string {
	return map[string]string{
		"dateTime": t.UTC().Format("2006-01-02T15:04:05"),
		"timeZone": "UTC",
	}
}

// graphEvent is the subset of the Graph event resource we use.
type graphEvent struct {
	ID       string            `json:"id,omitempty"`
	Subject  string            `json:"subject,omitempty"`
	Body     *graphItemBody    `json:"body,omitempty"`
	Start    map[string]string `json:"start,omitempty"`
	End      map[string]string `json:"end,omitempty"`
	IsAllDay bool              `json:"isAllDay,omitempty"`
	ShowAs   string            `json:"showAs,omitempty"` // oof for out-of-office
}

type graphItemBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

func (c *Client) graphEventBody(leave Leave) graphEvent {
	// Graph all-day events use midnight start and the day AFTER the last day as
	// the (exclusive) end.
	start := time.Date(leave.StartDate.Year(), leave.StartDate.Month(), leave.StartDate.Day(), 0, 0, 0, 0, time.UTC)
	endExclusive := time.Date(leave.EndDate.Year(), leave.EndDate.Month(), leave.EndDate.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	ev := graphEvent{
		Subject:  leave.title(),
		Start:    graphDateTimeUTC(start),
		End:      graphDateTimeUTC(endExclusive),
		IsAllDay: true,
		ShowAs:   "oof",
	}
	if leave.Note != "" {
		ev.Body = &graphItemBody{ContentType: "text", Content: leave.Note}
	}
	return ev
}

func (c *Client) microsoftPushLeave(ctx context.Context, leave Leave, existingEventID string) (string, error) {
	body := c.graphEventBody(leave)

	var out graphEvent
	if existingEventID != "" {
		u := fmt.Sprintf("%s/me/events/%s", graphAPI, url.PathEscape(existingEventID))
		status, err := c.do(ctx, http.MethodPatch, u, body, &out)
		if err == nil {
			return out.ID, nil
		}
		if status != http.StatusNotFound && status != http.StatusGone {
			return "", err
		}
	}

	u := graphAPI + "/me/events"
	if _, err := c.do(ctx, http.MethodPost, u, body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (c *Client) microsoftDeleteEvent(ctx context.Context, eventID string) error {
	u := fmt.Sprintf("%s/me/events/%s", graphAPI, url.PathEscape(eventID))
	status, err := c.do(ctx, http.MethodDelete, u, nil, nil)
	if err != nil && status != http.StatusNotFound && status != http.StatusGone {
		return err
	}
	return nil
}

// graphCalendarView models GET /me/calendarView (events overlapping a window).
type graphCalendarView struct {
	Value []struct {
		Start    map[string]string `json:"start"`
		End      map[string]string `json:"end"`
		ShowAs   string            `json:"showAs"`
		IsAllDay bool              `json:"isAllDay"`
	} `json:"value"`
}

func (c *Client) microsoftPullBusy(ctx context.Context, from, to time.Time) ([]BusyBlock, error) {
	// calendarView returns concrete (expanded) instances overlapping the window,
	// which we filter to busy/oof. Times come back in UTC (we request it).
	q := url.Values{}
	q.Set("startDateTime", from.UTC().Format("2006-01-02T15:04:05"))
	q.Set("endDateTime", to.UTC().Format("2006-01-02T15:04:05"))
	q.Set("$select", "start,end,showAs,isAllDay")
	q.Set("$top", "200")
	u := graphAPI + "/me/calendarView?" + q.Encode()

	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("calendar: build calendarView request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	// Ask Graph to return times in UTC.
	req.Header.Set("Prefer", `outlook.timezone="UTC"`)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calendar: calendarView request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("calendar: calendarView status %d", resp.StatusCode)
	}

	var view graphCalendarView
	if err := decodeJSON(resp.Body, &view); err != nil {
		return nil, fmt.Errorf("calendar: decode calendarView: %w", err)
	}

	var blocks []BusyBlock
	for _, ev := range view.Value {
		show := strings.ToLower(ev.ShowAs)
		if show == "free" {
			continue
		}
		start := parseGraphDateTime(ev.Start)
		end := parseGraphDateTime(ev.End)
		if start.IsZero() || end.IsZero() {
			continue
		}
		blocks = append(blocks, BusyBlock{Start: start, End: end, Status: show})
	}
	return blocks, nil
}

// parseGraphDateTime parses Graph's {dateTime,timeZone} pair (best-effort UTC).
func parseGraphDateTime(v map[string]string) time.Time {
	s := v["dateTime"]
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.0000000",
		"2006-01-02T15:04:05.9999999",
		"2006-01-02T15:04:05",
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
