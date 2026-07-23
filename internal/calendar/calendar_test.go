// Package calendar — unit tests for pure helpers (date shaping, OOO event bodies,
// inclusive→exclusive all-day handling, Graph datetime parsing) and the busy-block
// parsing in the provider clients (via httptest, no real Google/Graph calls).
package calendar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ── Leave.title ─────────────────────────────────────────────────────────────

func TestLeaveTitle(t *testing.T) {
	cases := []struct {
		kind, name, want string
	}{
		{"pto", "Ada Lovelace", "PTO — Ada Lovelace"},
		{"", "Ada", "PTO — Ada"}, // default label
		{"sick", "Bob", "Sick leave — Bob"},
		{"holiday", "Cy", "Holiday — Cy"},
		{"pto", "", "PTO"}, // no name → label only
		{"holiday", "", "Holiday"},
	}
	for _, c := range cases {
		l := Leave{Kind: c.kind, Name: c.name}
		if got := l.title(); got != c.want {
			t.Errorf("title(kind=%q,name=%q) = %q, want %q", c.kind, c.name, got, c.want)
		}
	}
}

// ── dateOnly + Google all-day exclusive end ─────────────────────────────────

func TestDateOnly(t *testing.T) {
	if got := dateOnly(day(2026, 6, 15)); got != "2026-06-15" {
		t.Errorf("dateOnly = %q", got)
	}
	// Non-UTC input is normalized to UTC date.
	loc := time.FixedZone("X", 5*3600)
	tt := time.Date(2026, 6, 15, 1, 0, 0, 0, loc) // 2026-06-14 20:00 UTC
	if got := dateOnly(tt); got != "2026-06-14" {
		t.Errorf("dateOnly(tz) = %q, want 2026-06-14", got)
	}
}

func TestGoogleEventBody_ExclusiveEnd(t *testing.T) {
	c := New(Conn{Provider: "google"})
	leave := Leave{
		Kind:      "pto",
		Name:      "Ada",
		StartDate: day(2026, 6, 15),
		EndDate:   day(2026, 6, 17), // inclusive last day
		Note:      "vacation",
	}
	ev := c.googleEventBody(leave)
	if ev.Start.Date != "2026-06-15" {
		t.Errorf("Start.Date = %q, want 2026-06-15", ev.Start.Date)
	}
	// Google all-day end is EXCLUSIVE → day after the inclusive end (18th).
	if ev.End.Date != "2026-06-18" {
		t.Errorf("End.Date = %q, want 2026-06-18 (exclusive)", ev.End.Date)
	}
	if ev.Summary != "PTO — Ada" {
		t.Errorf("Summary = %q", ev.Summary)
	}
	if ev.Description != "vacation" {
		t.Errorf("Description = %q", ev.Description)
	}
	if ev.Transparency != "opaque" {
		t.Errorf("Transparency = %q, want opaque (busy)", ev.Transparency)
	}
	if ev.EventType != "outOfOffice" {
		t.Errorf("EventType = %q", ev.EventType)
	}
}

func TestGoogleEventBody_SingleDay(t *testing.T) {
	c := New(Conn{Provider: "google"})
	leave := Leave{StartDate: day(2026, 6, 15), EndDate: day(2026, 6, 15)}
	ev := c.googleEventBody(leave)
	if ev.Start.Date != "2026-06-15" || ev.End.Date != "2026-06-16" {
		t.Errorf("single-day span = %q..%q, want 15..16", ev.Start.Date, ev.End.Date)
	}
}

// ── Microsoft Graph event body ──────────────────────────────────────────────

func TestGraphEventBody_ExclusiveEndAndOOF(t *testing.T) {
	c := New(Conn{Provider: "microsoft"})
	leave := Leave{
		Kind:      "sick",
		Name:      "Bob",
		StartDate: day(2026, 6, 15),
		EndDate:   day(2026, 6, 16),
		Note:      "flu",
	}
	ev := c.graphEventBody(leave)
	if ev.Start["dateTime"] != "2026-06-15T00:00:00" {
		t.Errorf("Start = %q", ev.Start["dateTime"])
	}
	// Exclusive end = day after inclusive EndDate (17th) at midnight.
	if ev.End["dateTime"] != "2026-06-17T00:00:00" {
		t.Errorf("End = %q, want 2026-06-17T00:00:00", ev.End["dateTime"])
	}
	if ev.Start["timeZone"] != "UTC" || ev.End["timeZone"] != "UTC" {
		t.Error("timeZone should be UTC")
	}
	if !ev.IsAllDay {
		t.Error("IsAllDay should be true")
	}
	if ev.ShowAs != "oof" {
		t.Errorf("ShowAs = %q, want oof", ev.ShowAs)
	}
	if ev.Body == nil || ev.Body.Content != "flu" || ev.Body.ContentType != "text" {
		t.Errorf("Body = %+v", ev.Body)
	}
	if ev.Subject != "Sick leave — Bob" {
		t.Errorf("Subject = %q", ev.Subject)
	}
}

func TestGraphEventBody_NoNoteOmitsBody(t *testing.T) {
	c := New(Conn{Provider: "microsoft"})
	ev := c.graphEventBody(Leave{StartDate: day(2026, 6, 1), EndDate: day(2026, 6, 1)})
	if ev.Body != nil {
		t.Errorf("empty note should omit Body, got %+v", ev.Body)
	}
}

func TestGraphDateTimeUTC(t *testing.T) {
	loc := time.FixedZone("X", 2*3600)
	tt := time.Date(2026, 6, 15, 10, 30, 0, 0, loc) // 08:30 UTC
	m := graphDateTimeUTC(tt)
	if m["dateTime"] != "2026-06-15T08:30:00" {
		t.Errorf("dateTime = %q, want 2026-06-15T08:30:00", m["dateTime"])
	}
	if m["timeZone"] != "UTC" {
		t.Errorf("timeZone = %q", m["timeZone"])
	}
}

// ── parseGraphDateTime ──────────────────────────────────────────────────────

func TestParseGraphDateTime(t *testing.T) {
	cases := []struct {
		in   map[string]string
		want time.Time
	}{
		{map[string]string{"dateTime": "2026-06-15T09:00:00"}, time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)},
		{map[string]string{"dateTime": "2026-06-15T09:00:00.0000000"}, time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)},
		{map[string]string{"dateTime": "2026-06-15T09:00:00.1234567"}, time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC).Add(123456700 * time.Nanosecond)},
		{map[string]string{"dateTime": "2026-06-15T09:00:00Z"}, time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		got := parseGraphDateTime(c.in)
		if !got.Equal(c.want) {
			t.Errorf("parseGraphDateTime(%q) = %v, want %v", c.in["dateTime"], got, c.want)
		}
	}
	// Empty / unparseable → zero time.
	if !parseGraphDateTime(map[string]string{}).IsZero() {
		t.Error("empty map should yield zero time")
	}
	if !parseGraphDateTime(map[string]string{"dateTime": "not-a-date"}).IsZero() {
		t.Error("garbage should yield zero time")
	}
}

// ── urlPath + snippet ───────────────────────────────────────────────────────

func TestURLPath(t *testing.T) {
	// Strips query so identifiers in the query string never leak into logs.
	if got := urlPath("https://host/calendar/v3/freeBusy?token=secret"); got != "/calendar/v3/freeBusy" {
		t.Errorf("urlPath = %q", got)
	}
	// Unparseable URL returned as-is.
	if got := urlPath("://bad"); got == "" {
		t.Errorf("urlPath of bad URL = %q", got)
	}
}

func TestSnippet(t *testing.T) {
	short := "short body"
	if got := snippet([]byte(short)); got != short {
		t.Errorf("short snippet = %q", got)
	}
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	got := snippet(long)
	if len([]rune(got)) != 240+1 { // 240 chars + ellipsis rune
		t.Errorf("long snippet rune-len = %d, want 241", len([]rune(got)))
	}
}

// ── PullBusy / unsupported-provider dispatch ────────────────────────────────

func TestUnsupportedProvider(t *testing.T) {
	c := New(Conn{Provider: "nope", AccessToken: "t"})
	if _, err := c.PushLeave(context.Background(), Leave{}, ""); err == nil {
		t.Error("PushLeave: expected unsupported-provider error")
	}
	if err := c.DeleteLeaveEvent(context.Background(), "evt"); err == nil {
		t.Error("DeleteLeaveEvent: expected unsupported-provider error")
	}
	if _, err := c.PullBusy(context.Background(), time.Now(), time.Now()); err == nil {
		t.Error("PullBusy: expected unsupported-provider error")
	}
}

func TestDeleteLeaveEvent_EmptyIDNoop(t *testing.T) {
	c := New(Conn{Provider: "google", AccessToken: "t"})
	if err := c.DeleteLeaveEvent(context.Background(), ""); err != nil {
		t.Errorf("empty eventID should be a no-op, got %v", err)
	}
}

func TestCalendarID_Default(t *testing.T) {
	if id := New(Conn{}).calendarID(); id != "primary" {
		t.Errorf("default calendarID = %q, want primary", id)
	}
	if id := New(Conn{CalendarID: "team@group.calendar"}).calendarID(); id != "team@group.calendar" {
		t.Errorf("explicit calendarID = %q", id)
	}
}

// ── Microsoft busy-block parsing via httptest ──────────────────────────────

// redirectTransport rewrites every request's host to the test server.
type redirectTransport struct{ base string }

func (r redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ts, _ := http.NewRequest(http.MethodGet, r.base, nil)
	u := *req.URL
	u.Scheme = ts.URL.Scheme
	u.Host = ts.URL.Host
	req.URL = &u
	return http.DefaultTransport.RoundTrip(req)
}

func TestMicrosoftPullBusy_FiltersFree(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// One busy, one oof, one free (dropped), one with missing times (dropped).
		w.Write([]byte(`{"value":[
			{"start":{"dateTime":"2026-06-15T09:00:00"},"end":{"dateTime":"2026-06-15T10:00:00"},"showAs":"busy"},
			{"start":{"dateTime":"2026-06-16T00:00:00"},"end":{"dateTime":"2026-06-17T00:00:00"},"showAs":"Oof"},
			{"start":{"dateTime":"2026-06-18T09:00:00"},"end":{"dateTime":"2026-06-18T10:00:00"},"showAs":"free"},
			{"start":{"dateTime":""},"end":{"dateTime":""},"showAs":"busy"}
		]}`))
	}))
	defer ts.Close()

	c := New(Conn{Provider: "microsoft", AccessToken: "tok", Expiry: time.Now().Add(time.Hour)})
	c.http.Transport = redirectTransport{base: ts.URL}

	blocks, err := c.PullBusy(context.Background(), day(2026, 6, 15), day(2026, 6, 20))
	if err != nil {
		t.Fatalf("PullBusy: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (free + missing-time dropped)", len(blocks))
	}
	// Status is lowercased.
	if blocks[0].Status != "busy" || blocks[1].Status != "oof" {
		t.Errorf("statuses = %q, %q", blocks[0].Status, blocks[1].Status)
	}
	if !blocks[0].Start.Equal(time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("block 0 start = %v", blocks[0].Start)
	}
}

func TestGooglePullBusy_ParsesBusy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"calendars":{"primary":{"busy":[
			{"start":"2026-06-15T09:00:00Z","end":"2026-06-15T11:00:00Z"}
		]}}}`))
	}))
	defer ts.Close()

	c := New(Conn{Provider: "google", AccessToken: "tok", Expiry: time.Now().Add(time.Hour)})
	c.http.Transport = redirectTransport{base: ts.URL}

	blocks, err := c.PullBusy(context.Background(), day(2026, 6, 15), day(2026, 6, 16))
	if err != nil {
		t.Fatalf("PullBusy: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	if blocks[0].Status != "busy" {
		t.Errorf("status = %q, want busy", blocks[0].Status)
	}
	if !blocks[0].Start.Equal(time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("start = %v", blocks[0].Start)
	}
}
