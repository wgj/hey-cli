package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

type dayViewServerOptions struct {
	timeZone    string
	calendars   []generated.Calendar
	recordings  map[int64]generated.CalendarRecordingsResponse
	occurrences map[int64]generated.CalendarOccurrencesResponse
	failures    map[string]int
	nullSources map[string]bool
}

type dayViewServerState struct {
	mu      sync.Mutex
	queries map[string][]url.Values
}

func (s *dayViewServerState) queryCount(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queries[key])
}

func (s *dayViewServerState) firstQuery(key string) url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queries[key]) == 0 {
		return nil
	}
	return s.queries[key][0]
}

func dayViewServer(t *testing.T, options dayViewServerOptions) (*httptest.Server, *dayViewServerState) {
	t.Helper()

	state := &dayViewServerState{queries: make(map[string][]url.Values)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimSuffix(r.URL.Path, ".json")
		switch path {
		case "/identity":
			_ = json.NewEncoder(w).Encode(generated.Identity{Id: 1, TimeZone: options.timeZone})
			return
		case "/calendars":
			wrapped := make([]generated.CalendarWithRecordingChangesUrl, 0, len(options.calendars))
			for _, calendar := range options.calendars {
				wrapped = append(wrapped, generated.CalendarWithRecordingChangesUrl{Calendar: calendar})
			}
			_ = json.NewEncoder(w).Encode(generated.CalendarListPayload{Calendars: wrapped})
			return
		}

		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) != 3 || parts[0] != "calendars" {
			http.NotFound(w, r)
			return
		}
		calendarID, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || (parts[2] != "recordings" && parts[2] != "occurrences") {
			http.NotFound(w, r)
			return
		}

		key := fmt.Sprintf("%s:%d", parts[2], calendarID)
		state.mu.Lock()
		state.queries[key] = append(state.queries[key], r.URL.Query())
		state.mu.Unlock()

		if status := options.failures[key]; status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"calendar source unavailable"}`))
			return
		}
		if options.nullSources[key] {
			_, _ = w.Write([]byte("null"))
			return
		}

		switch parts[2] {
		case "recordings":
			response := generated.CalendarRecordingsResponse{}
			if configured, ok := options.recordings[calendarID]; ok {
				response = configured
			}
			_ = json.NewEncoder(w).Encode(response)
		case "occurrences":
			response := generated.CalendarOccurrencesResponse{}
			if configured, ok := options.occurrences[calendarID]; ok {
				response = configured
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	t.Cleanup(server.Close)
	return server, state
}

func runDayView(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
	t.Helper()
	stdout, err := runDayViewOutput(t, server, "--json", args...)

	var response output.Response
	if stdout != "" {
		_ = json.Unmarshal([]byte(stdout), &response)
	}
	return response, err
}

func runDayViewOutput(t *testing.T, server *httptest.Server, outputFlag string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	oldJSON, oldHTML := jsonFlag, htmlOutput
	oldQuiet, oldIDs, oldCount := quietFlag, idsOnly, countFlag
	oldMarkdown, oldStyled, oldAgent := markdownF, styledFlag, agentFlag
	oldStats, oldVerbose, oldBaseURL := statsFlag, verboseFlag, baseURL
	oldConfig, oldAuth, oldWriter, oldSDK := cfg, authMgr, writer, sdk
	defer func() {
		jsonFlag, htmlOutput = oldJSON, oldHTML
		quietFlag, idsOnly, countFlag = oldQuiet, oldIDs, oldCount
		markdownF, styledFlag, agentFlag = oldMarkdown, oldStyled, oldAgent
		statsFlag, verboseFlag, baseURL = oldStats, oldVerbose, oldBaseURL
		cfg, authMgr, writer, sdk = oldConfig, oldAuth, oldWriter, oldSDK
	}()
	jsonFlag, htmlOutput = false, false
	quietFlag, idsOnly, countFlag = false, false, false
	markdownF, styledFlag, agentFlag = false, false, false
	statsFlag, verboseFlag, baseURL = false, 0, ""

	root := newRootCmd()
	var buffer bytes.Buffer
	root.SetOut(&buffer)
	root.SetErr(&buffer)
	commandArgs := append([]string{"day-view", outputFlag, "--base-url", server.URL}, args...)
	root.SetArgs(commandArgs)

	err := root.Execute()
	return buffer.String(), err
}

type dayViewEventResult struct {
	ID           *int64             `json:"id"`
	Title        string             `json:"title"`
	AllDay       bool               `json:"all_day"`
	Recurring    bool               `json:"recurring"`
	OccurrenceID string             `json:"occurrence_id"`
	StartsAt     time.Time          `json:"starts_at"`
	EndsAt       time.Time          `json:"ends_at"`
	Type         string             `json:"type"`
	Calendar     generated.Calendar `json:"calendar"`
}

func dayViewResults(t *testing.T, response output.Response) []dayViewEventResult {
	t.Helper()
	data, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatalf("marshal response data: %v", err)
	}
	var events []dayViewEventResult
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatalf("decode day-view events: %v", err)
	}
	return events
}

func dayViewTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

func dayViewID(value int64) *int64 {
	return &value
}

func TestDayViewMergesFiltersDeduplicatesAndSorts(t *testing.T) {
	calendars := []generated.Calendar{
		{Id: 42, Name: "Family"},
		{Id: 84, Name: "Community"},
	}
	recordings := map[int64]generated.CalendarRecordingsResponse{
		42: {
			dayViewEventType: {
				{Id: 101, Title: "School holiday", Type: dayViewEventType, AllDay: true, StartsAt: dayViewTime(t, "2026-08-10T00:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T00:00:00Z")},
				{Id: 102, Title: "Breakfast", Type: dayViewEventType, StartsAt: dayViewTime(t, "2026-08-10T07:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T08:00:00Z")},
				{Id: 103, Title: "Series master", Type: dayViewEventType, Recurring: true, StartsAt: dayViewTime(t, "2026-08-10T14:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T15:00:00Z")},
				{Id: 104, ParentId: 900, Title: "Team review", Type: dayViewEventType, Recurring: true, StartsAt: dayViewTime(t, "2026-08-10T15:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T16:00:00Z")},
				{Id: 105, Title: "Previous evening", Type: dayViewEventType, StartsAt: dayViewTime(t, "2026-08-10T04:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T05:00:00Z")},
				{Id: 106, Title: "Nighttime", Type: dayViewEventType, StartsAt: dayViewTime(t, "2026-08-10T20:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T21:00:00Z")},
			},
			"Calendar::Todo": {
				{Id: 201, Title: "Pack lunch", Type: "Calendar::Todo", StartsAt: dayViewTime(t, "2026-08-10T00:00:00Z")},
			},
		},
		84: {
			dayViewEventType: {
				{Id: 107, Title: "County fair", Type: dayViewEventType, AllDay: true, StartsAt: dayViewTime(t, "2026-08-09T00:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T00:00:00Z")},
				{Id: 108, Title: "Same title", Type: dayViewEventType, StartsAt: dayViewTime(t, "2026-08-10T16:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T17:00:00Z")},
				{Id: 109, Title: "Same title", Type: dayViewEventType, StartsAt: dayViewTime(t, "2026-08-10T16:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T17:00:00Z")},
			},
		},
	}
	occurrences := map[int64]generated.CalendarOccurrencesResponse{
		42: {
			dayViewEventType: {
				Realized: []generated.CalendarOccurrence{
					{Id: dayViewID(104), ParentId: 900, OccurrenceId: "900_2026-08-10", Title: "Occurrence title must not replace recording", Type: dayViewEventType, Recurring: true, StartsAt: dayViewTime(t, "2026-08-10T15:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T16:00:00Z")},
				},
				Unrealized: []generated.CalendarOccurrence{
					{OccurrenceId: "777_2026-08-10", ParentId: 777, Title: "Reading lesson", Type: dayViewEventType, Recurring: true, StartsAt: dayViewTime(t, "2026-08-11T00:30:00Z"), EndsAt: dayViewTime(t, "2026-08-11T01:00:00Z")},
					{OccurrenceId: "777_2026-08-17", ParentId: 777, Title: "Future reading lesson", Type: dayViewEventType, Recurring: true, StartsAt: dayViewTime(t, "2026-08-18T00:30:00Z"), EndsAt: dayViewTime(t, "2026-08-18T01:00:00Z")},
				},
			},
		},
		84: {},
	}
	server, state := dayViewServer(t, dayViewServerOptions{
		timeZone:    "America/Denver",
		calendars:   calendars,
		recordings:  recordings,
		occurrences: occurrences,
	})

	response, err := runDayView(t, server, "2026-08-10")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	for _, calendarID := range []int64{42, 84} {
		for _, source := range []string{"recordings", "occurrences"} {
			key := fmt.Sprintf("%s:%d", source, calendarID)
			if got := state.queryCount(key); got != 1 {
				t.Errorf("%s query count = %d, want 1", key, got)
			}
			query := state.firstQuery(key)
			if got := query.Get("starts_on"); got != "2026-08-10" {
				t.Errorf("%s starts_on = %q", key, got)
			}
			if got := query.Get("ends_on"); got != "2026-08-11" {
				t.Errorf("%s ends_on = %q", key, got)
			}
		}
	}

	if got := response.Meta["date"]; got != "2026-08-10" {
		t.Errorf("meta.date = %#v", got)
	}
	if got := response.Meta["time_zone"]; got != "America/Denver" {
		t.Errorf("meta.time_zone = %#v", got)
	}
	calendarData, _ := json.Marshal(response.Meta["calendar_ids"])
	var checkedCalendarIDs []int64
	if err := json.Unmarshal(calendarData, &checkedCalendarIDs); err != nil {
		t.Fatalf("decode meta.calendar_ids: %v", err)
	}
	if fmt.Sprint(checkedCalendarIDs) != "[42 84]" {
		t.Errorf("meta.calendar_ids = %v", checkedCalendarIDs)
	}

	events := dayViewResults(t, response)
	wantTitles := []string{
		"County fair",
		"School holiday",
		"Breakfast",
		"Team review",
		"Same title",
		"Same title",
		"Nighttime",
		"Reading lesson",
	}
	if len(events) != len(wantTitles) {
		t.Fatalf("event count = %d, want %d: %#v", len(events), len(wantTitles), events)
	}
	for i, want := range wantTitles {
		if events[i].Title != want {
			t.Errorf("event %d title = %q, want %q", i, events[i].Title, want)
		}
		if events[i].Type != dayViewEventType {
			t.Errorf("event %d type = %q", i, events[i].Type)
		}
		if !events[i].StartsAt.IsZero() && events[i].StartsAt.Location() != time.UTC {
			t.Errorf("event %d starts_at is not UTC: %s", i, events[i].StartsAt)
		}
	}
	if events[3].OccurrenceID != "900_2026-08-10" {
		t.Errorf("recording duplicate did not gain occurrence ID: %#v", events[3])
	}
	if events[7].ID != nil || events[7].OccurrenceID != "777_2026-08-10" {
		t.Errorf("virtual occurrence = %#v", events[7])
	}
	if events[0].Calendar.Id != 84 || events[0].Calendar.Name != "Community" {
		t.Errorf("event calendar = %#v", events[0].Calendar)
	}
	if events[1].Calendar.Id != 42 || events[1].Calendar.Name != "Family" {
		t.Errorf("event calendar = %#v", events[1].Calendar)
	}

	rawData, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatalf("marshal raw events: %v", err)
	}
	var rawEvents []map[string]any
	if err := json.Unmarshal(rawData, &rawEvents); err != nil {
		t.Fatalf("decode raw events: %v", err)
	}
	if _, hasID := rawEvents[7]["id"]; hasID {
		t.Errorf("virtual occurrence exposed numeric ID: %#v", rawEvents[7]["id"])
	}
}

func TestDayViewDeduplicatesIdentityAliasesTransitively(t *testing.T) {
	start := dayViewTime(t, "2026-08-10T15:00:00Z")
	end := dayViewTime(t, "2026-08-10T16:00:00Z")
	recording := newDayViewEvent(generated.Recording{Id: 10, Title: "Persisted event", Type: dayViewEventType, StartsAt: start, EndsAt: end}, dayViewID(10), generated.Calendar{Id: 42, Name: "Family"}, dayViewRecording)
	bridge := newDayViewEvent(generated.Recording{Id: 10, Title: "Realized event", Type: dayViewEventType, OccurrenceId: "series_day", StartsAt: start, EndsAt: end}, dayViewID(10), generated.Calendar{Id: 42, Name: "Family"}, dayViewRealizedOccurrence)
	virtual := newDayViewEvent(generated.Recording{Title: "Virtual event", Type: dayViewEventType, OccurrenceId: "series_day", StartsAt: start, EndsAt: end}, nil, generated.Calendar{Id: 42, Name: "Family"}, dayViewUnrealizedOccurrence)

	events := deduplicateDayViewEvents([]dayViewEvent{recording, bridge, virtual})
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1: %#v", len(events), events)
	}
	if events[0].Title != "Persisted event" || events[0].OccurrenceId != "series_day" || events[0].ID == nil || *events[0].ID != 10 {
		t.Errorf("merged event = %#v", events[0])
	}

	chain := []dayViewEvent{
		newDayViewEvent(generated.Recording{Id: 30, Title: "Persisted chain event", Type: dayViewEventType, OccurrenceId: "series_a", StartsAt: start, EndsAt: end}, dayViewID(30), generated.Calendar{Id: 42}, dayViewRecording),
		newDayViewEvent(generated.Recording{Id: 31, Title: "First bridge", Type: dayViewEventType, OccurrenceId: "series_a", StartsAt: start, EndsAt: end}, dayViewID(31), generated.Calendar{Id: 42}, dayViewRealizedOccurrence),
		newDayViewEvent(generated.Recording{Id: 31, Title: "Second bridge", Type: dayViewEventType, OccurrenceId: "series_b", StartsAt: start, EndsAt: end}, dayViewID(31), generated.Calendar{Id: 42}, dayViewRealizedOccurrence),
		newDayViewEvent(generated.Recording{Id: 32, Title: "End of chain", Type: dayViewEventType, OccurrenceId: "series_b", StartsAt: start, EndsAt: end}, dayViewID(32), generated.Calendar{Id: 42}, dayViewUnrealizedOccurrence),
	}
	chainedEvents := deduplicateDayViewEvents(chain)
	if len(chainedEvents) != 1 {
		t.Fatalf("chained event count = %d, want 1: %#v", len(chainedEvents), chainedEvents)
	}
	if chainedEvents[0].Title != "Persisted chain event" {
		t.Errorf("chained event title = %q", chainedEvents[0].Title)
	}

	first := newDayViewEvent(generated.Recording{Id: 20, Title: "Same", Type: dayViewEventType, StartsAt: start, EndsAt: end}, dayViewID(20), generated.Calendar{Id: 42}, dayViewRecording)
	second := newDayViewEvent(generated.Recording{Id: 21, Title: "Same", Type: dayViewEventType, StartsAt: start, EndsAt: end}, dayViewID(21), generated.Calendar{Id: 42}, dayViewRecording)
	identitylessA := newDayViewEvent(generated.Recording{Title: "Same", Type: dayViewEventType, StartsAt: start, EndsAt: end}, nil, generated.Calendar{Id: 42}, dayViewRecording)
	identitylessB := newDayViewEvent(generated.Recording{Title: "Same", Type: dayViewEventType, StartsAt: start, EndsAt: end}, nil, generated.Calendar{Id: 42}, dayViewRecording)
	if got := len(deduplicateDayViewEvents([]dayViewEvent{first, second, identitylessA, identitylessB})); got != 4 {
		t.Errorf("different identities with equal title/time collapsed: got %d events", got)
	}
}

func TestDayViewCalendarSelection(t *testing.T) {
	server, state := dayViewServer(t, dayViewServerOptions{
		timeZone: "America/Denver",
		calendars: []generated.Calendar{
			{Id: 42, Name: "Family"},
			{Id: 84, Name: "Community"},
		},
	})

	response, err := runDayView(t, server, "2026-08-10", "--calendar", "84", "--calendar", "84")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := state.queryCount("recordings:42") + state.queryCount("occurrences:42"); got != 0 {
		t.Errorf("unselected calendar query count = %d", got)
	}
	if got := state.queryCount("recordings:84"); got != 1 {
		t.Errorf("recordings:84 query count = %d", got)
	}
	if got := state.queryCount("occurrences:84"); got != 1 {
		t.Errorf("occurrences:84 query count = %d", got)
	}
	calendarData, _ := json.Marshal(response.Meta["calendar_ids"])
	if string(calendarData) != "[84]" {
		t.Errorf("meta.calendar_ids = %s", calendarData)
	}
}

func TestDayViewRejectsInvalidInputAndSourceFailures(t *testing.T) {
	t.Run("invalid date", func(t *testing.T) {
		server, _ := dayViewServer(t, dayViewServerOptions{timeZone: "America/Denver"})
		_, err := runDayView(t, server, "2026-02-30")
		if err == nil || output.AsError(err).Code != "usage" {
			t.Fatalf("error = %v, want usage", err)
		}
	})

	t.Run("non-positive calendar", func(t *testing.T) {
		server, _ := dayViewServer(t, dayViewServerOptions{timeZone: "America/Denver"})
		_, err := runDayView(t, server, "2026-08-10", "--calendar", "0")
		if err == nil || output.AsError(err).Code != "usage" {
			t.Fatalf("error = %v, want usage", err)
		}
	})

	t.Run("unknown calendar", func(t *testing.T) {
		server, state := dayViewServer(t, dayViewServerOptions{
			timeZone:  "America/Denver",
			calendars: []generated.Calendar{{Id: 42, Name: "Family"}},
		})
		_, err := runDayView(t, server, "2026-08-10", "--calendar", "999")
		if err == nil || output.AsError(err).Code != "not_found" {
			t.Fatalf("error = %v, want not_found", err)
		}
		if got := state.queryCount("recordings:999") + state.queryCount("occurrences:999"); got != 0 {
			t.Errorf("unknown calendar query count = %d", got)
		}
	})

	tests := []struct {
		name        string
		failures    map[string]int
		nullSources map[string]bool
		wantRecord  int
		wantOccur   int
	}{
		{name: "recordings failure", failures: map[string]int{"recordings:42": http.StatusBadRequest}, wantRecord: 1, wantOccur: 0},
		{name: "occurrences failure", failures: map[string]int{"occurrences:42": http.StatusBadRequest}, wantRecord: 1, wantOccur: 1},
		{name: "null recordings", nullSources: map[string]bool{"recordings:42": true}, wantRecord: 1, wantOccur: 0},
		{name: "null occurrences", nullSources: map[string]bool{"occurrences:42": true}, wantRecord: 1, wantOccur: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, state := dayViewServer(t, dayViewServerOptions{
				timeZone:    "America/Denver",
				calendars:   []generated.Calendar{{Id: 42, Name: "Family"}},
				failures:    test.failures,
				nullSources: test.nullSources,
			})
			_, err := runDayView(t, server, "2026-08-10")
			if err == nil || output.AsError(err).Code != "api" {
				t.Fatalf("error = %v, want api", err)
			}
			if got := state.queryCount("recordings:42"); got != test.wantRecord {
				t.Errorf("recordings query count = %d, want %d", got, test.wantRecord)
			}
			if got := state.queryCount("occurrences:42"); got != test.wantOccur {
				t.Errorf("occurrences query count = %d, want %d", got, test.wantOccur)
			}
		})
	}
}

func TestDayViewUsesHEYTimeZone(t *testing.T) {
	t.Run("default date", func(t *testing.T) {
		location, err := time.LoadLocation("Pacific/Kiritimati")
		if err != nil {
			t.Fatalf("load location: %v", err)
		}
		before := time.Now().In(location).Format("2006-01-02")
		server, _ := dayViewServer(t, dayViewServerOptions{timeZone: location.String()})
		response, err := runDayView(t, server)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		after := time.Now().In(location).Format("2006-01-02")
		got, _ := response.Meta["date"].(string)
		if got != before && got != after {
			t.Errorf("meta.date = %q, want %q or %q", got, before, after)
		}
	})

	t.Run("missing time zone", func(t *testing.T) {
		server, _ := dayViewServer(t, dayViewServerOptions{})
		_, err := runDayView(t, server, "2026-08-10")
		if err == nil || !strings.Contains(err.Error(), "time zone is unavailable") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("invalid time zone", func(t *testing.T) {
		server, _ := dayViewServer(t, dayViewServerOptions{timeZone: "Mars/Olympus_Mons"})
		_, err := runDayView(t, server, "2026-08-10")
		if err == nil || !strings.Contains(err.Error(), "invalid HEY account time zone") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("styled output across DST", func(t *testing.T) {
		server, _ := dayViewServer(t, dayViewServerOptions{
			timeZone:  "America/Denver",
			calendars: []generated.Calendar{{Id: 42, Name: "Family"}},
			recordings: map[int64]generated.CalendarRecordingsResponse{
				42: {
					dayViewEventType: {
						{Id: 101, Title: "Early train", Type: dayViewEventType, StartsAt: dayViewTime(t, "2026-03-08T08:30:00Z"), EndsAt: dayViewTime(t, "2026-03-08T09:30:00Z")},
					},
				},
			},
		})
		stdout, err := runDayViewOutput(t, server, "--styled", "2026-03-08")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(stdout, "2026-03-08T01:30") || !strings.Contains(stdout, "2026-03-08T03:30") {
			t.Errorf("styled output does not use HEY time across DST: %q", stdout)
		}
	})
}

func TestDayViewEventOverlap(t *testing.T) {
	location, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	rangeStart := time.Date(2026, time.August, 10, 0, 0, 0, 0, location)
	rangeEnd := rangeStart.AddDate(0, 0, 1)

	tests := []struct {
		name      string
		recording generated.Recording
		want      bool
	}{
		{name: "inside", recording: generated.Recording{StartsAt: dayViewTime(t, "2026-08-10T15:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T16:00:00Z")}, want: true},
		{name: "crosses start", recording: generated.Recording{StartsAt: dayViewTime(t, "2026-08-10T05:30:00Z"), EndsAt: dayViewTime(t, "2026-08-10T06:30:00Z")}, want: true},
		{name: "ends at start", recording: generated.Recording{StartsAt: dayViewTime(t, "2026-08-10T05:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T06:00:00Z")}, want: false},
		{name: "starts at end", recording: generated.Recording{StartsAt: dayViewTime(t, "2026-08-11T06:00:00Z"), EndsAt: dayViewTime(t, "2026-08-11T07:00:00Z")}, want: false},
		{name: "zero duration inside", recording: generated.Recording{StartsAt: dayViewTime(t, "2026-08-10T15:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T15:00:00Z")}, want: true},
		{name: "missing start", recording: generated.Recording{}, want: false},
		{name: "single all-day", recording: generated.Recording{AllDay: true, StartsAt: dayViewTime(t, "2026-08-10T00:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T00:00:00Z")}, want: true},
		{name: "all-day previous date", recording: generated.Recording{AllDay: true, StartsAt: dayViewTime(t, "2026-08-09T00:00:00Z"), EndsAt: dayViewTime(t, "2026-08-09T00:00:00Z")}, want: false},
		{name: "all-day inclusive final date", recording: generated.Recording{AllDay: true, StartsAt: dayViewTime(t, "2026-08-09T00:00:00Z"), EndsAt: dayViewTime(t, "2026-08-10T00:00:00Z")}, want: true},
		{name: "all-day missing end", recording: generated.Recording{AllDay: true, StartsAt: dayViewTime(t, "2026-08-10T00:00:00Z")}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := dayViewEventOverlaps(test.recording, rangeStart, rangeEnd); got != test.want {
				t.Errorf("dayViewEventOverlaps() = %t, want %t", got, test.want)
			}
		})
	}

	springStart := time.Date(2026, time.March, 8, 0, 0, 0, 0, location)
	if got := springStart.AddDate(0, 0, 1).Sub(springStart); got != 23*time.Hour {
		t.Errorf("spring Day View duration = %s, want 23h", got)
	}
	fallStart := time.Date(2026, time.November, 1, 0, 0, 0, 0, location)
	if got := fallStart.AddDate(0, 0, 1).Sub(fallStart); got != 25*time.Hour {
		t.Errorf("fall Day View duration = %s, want 25h", got)
	}
}

func TestDayViewEmptyResultIsAnArray(t *testing.T) {
	server, _ := dayViewServer(t, dayViewServerOptions{timeZone: "America/Denver"})
	response, err := runDayView(t, server, "2026-08-10")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	if string(data) != "[]" {
		t.Errorf("empty data = %s, want []", data)
	}

	stdout, err := runDayViewOutput(t, server, "--styled", "2026-08-10")
	if err != nil {
		t.Fatalf("styled execute: %v", err)
	}
	if !strings.Contains(stdout, "No events for 2026-08-10 (America/Denver).") {
		t.Errorf("styled output = %q", stdout)
	}
}
