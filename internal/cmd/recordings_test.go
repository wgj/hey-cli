package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

func recordingsServer(t *testing.T) (*httptest.Server, *url.Values) {
	t.Helper()
	var query url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/calendars/42/recordings" {
			http.NotFound(w, r)
			return
		}

		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "Calendar::Event": [
    {"id": 103, "title": "Historical appointment", "type": "Calendar::Event", "recurring": true, "starts_at": "2026-07-10T18:00:00Z", "ends_at": "2026-07-10T19:00:00Z"},
    {"id": 101, "title": "Project review", "type": "Calendar::Event", "starts_at": "2026-08-06T23:00:00Z", "ends_at": "2026-08-07T00:00:00Z"},
    {"id": 102, "title": "Morning appointment", "type": "Calendar::Event", "starts_at": "2026-08-06T15:30:00Z", "ends_at": "2026-08-06T17:00:00Z"},
    {"id": 104, "title": "Future appointment", "type": "Calendar::Event", "starts_at": "2026-08-14T18:00:00Z", "ends_at": "2026-08-14T19:00:00Z"}
  ],
  "Calendar::Todo": [
    {"id": 201, "title": "Send agenda", "type": "Calendar::Todo", "starts_at": "2026-07-10T00:00:00Z"}
  ]
}`))
	}))
	t.Cleanup(server.Close)
	return server, &query
}

func runRecordings(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"recordings", "42", "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func responseRecordings(t *testing.T, resp output.Response) generated.CalendarRecordingsResponse {
	t.Helper()
	data, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal response data: %v", err)
	}

	var recordings generated.CalendarRecordingsResponse
	if err := json.Unmarshal(data, &recordings); err != nil {
		t.Fatalf("decode recordings: %v", err)
	}
	return recordings
}

func TestRecordingsFiltersServerOverfetch(t *testing.T) {
	server, query := recordingsServer(t)
	resp, err := runRecordings(t, server, "--starts-on", "2026-08-06", "--ends-on", "2026-08-07")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got := query.Get("starts_on"); got != "2026-08-06" {
		t.Errorf("starts_on = %q, want %q", got, "2026-08-06")
	}
	if got := query.Get("ends_on"); got != "2026-08-07" {
		t.Errorf("ends_on = %q, want unchanged bound", got)
	}

	recordings := responseRecordings(t, resp)
	events := recordings["Calendar::Event"]
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2: %#v", len(events), events)
	}
	if events[0].Id != 101 || events[0].Title != "Project review" {
		t.Errorf("first event = %#v, want Project review with its original ID", events[0])
	}
	if events[1].Id != 102 || events[1].Title != "Morning appointment" {
		t.Errorf("second event = %#v, want Morning appointment with its original ID", events[1])
	}
	if len(recordings["Calendar::Todo"]) != 1 {
		t.Errorf("non-event recordings changed: %#v", recordings["Calendar::Todo"])
	}
	if resp.Summary != "Recordings for calendar 42 (2026-08-06 to 2026-08-07)" {
		t.Errorf("summary = %q", resp.Summary)
	}
}

func TestRecordingsLimitAppliesAfterFiltering(t *testing.T) {
	server, _ := recordingsServer(t)
	resp, err := runRecordings(t, server, "--starts-on", "2026-08-06", "--ends-on", "2026-08-07", "--limit", "1")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	events := responseRecordings(t, resp)["Calendar::Event"]
	if len(events) != 1 || events[0].Title != "Project review" {
		t.Errorf("limited events = %#v, want first in-range event", events)
	}
}

func TestRecordingsEqualDateBoundsReturnNoEvents(t *testing.T) {
	server, query := recordingsServer(t)
	resp, err := runRecordings(t, server, "--starts-on", "2026-08-06", "--ends-on", "2026-08-06")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got := query.Get("ends_on"); got != "2026-08-06" {
		t.Errorf("ends_on = %q, want unchanged equal bound", got)
	}
	if events := responseRecordings(t, resp)["Calendar::Event"]; len(events) != 0 {
		t.Fatalf("event count = %d, want empty range: %#v", len(events), events)
	}
}

func TestFilterCalendarEventsUsesRequestedDateLocation(t *testing.T) {
	events := generated.CalendarRecordingsResponse{
		"Calendar::Event": {
			{Id: 101, StartsAt: time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 8, 7, 3, 0, 0, 0, time.UTC)},
			{Id: 102, StartsAt: time.Date(2026, 8, 6, 4, 30, 0, 0, time.UTC), EndsAt: time.Date(2026, 8, 6, 5, 30, 0, 0, time.UTC)},
		},
	}

	filterCalendarEvents(&events, "2026-08-06", "2026-08-07", time.FixedZone("MDT", -6*60*60))

	filtered := events["Calendar::Event"]
	if len(filtered) != 1 || filtered[0].Id != 101 {
		t.Errorf("filtered events = %#v, want the event on August 6 in the requested date location", filtered)
	}
}

func TestEventOverlapsDateRange(t *testing.T) {
	parse := func(value string) time.Time {
		t.Helper()
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatalf("parse %q: %v", value, err)
		}
		return parsed
	}
	rangeStart := parse("2026-08-06T00:00:00Z")
	rangeEnd := parse("2026-08-07T00:00:00Z")

	tests := []struct {
		name  string
		event generated.Recording
		want  bool
	}{
		{name: "inside", event: generated.Recording{StartsAt: parse("2026-08-06T15:30:00Z"), EndsAt: parse("2026-08-06T17:00:00Z")}, want: true},
		{name: "crosses start", event: generated.Recording{StartsAt: parse("2026-08-05T23:30:00Z"), EndsAt: parse("2026-08-06T00:30:00Z")}, want: true},
		{name: "ends at start", event: generated.Recording{StartsAt: parse("2026-08-05T23:00:00Z"), EndsAt: rangeStart}, want: false},
		{name: "starts at end", event: generated.Recording{StartsAt: rangeEnd, EndsAt: parse("2026-08-07T01:00:00Z")}, want: false},
		{name: "zero duration inside", event: generated.Recording{StartsAt: parse("2026-08-06T12:00:00Z"), EndsAt: parse("2026-08-06T12:00:00Z")}, want: true},
		{name: "missing start", event: generated.Recording{}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eventOverlapsDateRange(tt.event, rangeStart, rangeEnd); got != tt.want {
				t.Errorf("eventOverlapsDateRange() = %v, want %v", got, tt.want)
			}
		})
	}

	location := time.FixedZone("MDT", -6*60*60)
	localDate := func(day int) time.Time {
		return time.Date(2026, time.August, day, 0, 0, 0, 0, location)
	}
	allDay := generated.Recording{AllDay: true, StartsAt: parse("2026-08-05T00:00:00Z"), EndsAt: parse("2026-08-09T00:00:00Z")}
	if eventOverlapsDateRange(allDay, localDate(4), localDate(5)) {
		t.Error("all-day event should not start before its HEY starts_at date")
	}
	if !eventOverlapsDateRange(allDay, localDate(9), localDate(10)) {
		t.Error("all-day event should include its HEY ends_at date")
	}
	if eventOverlapsDateRange(allDay, localDate(10), localDate(11)) {
		t.Error("all-day event should not extend past its HEY ends_at date")
	}
}
