package smoke_test

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCalendarsList(t *testing.T) {
	resp := heyJSON(t, "calendars")

	type Calendar struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Owned bool   `json:"owned"`
	}
	calendars := dataAs[[]Calendar](t, resp)

	if len(calendars) == 0 {
		t.Fatal("expected at least one calendar")
	}

	// Should have a personal calendar.
	found := false
	for _, c := range calendars {
		if c.Owned {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected at least one owned calendar")
	}

	// Cross-verify: the calendar page should be accessible and non-empty.
	html := fetchHTML(t, baseURL+"/calendar")
	if len(html) == 0 {
		t.Error("calendar page returned empty HTML")
	}
}

func TestRecordings(t *testing.T) {
	// Get a calendar ID first.
	resp := heyJSON(t, "calendars")
	type Calendar struct {
		ID int `json:"id"`
	}
	calendars := dataAs[[]Calendar](t, resp)
	if len(calendars) == 0 {
		t.Fatal("no calendars available")
	}

	calID := calendars[0].ID
	recResp := heyJSON(t, "recordings", intStr(calID))
	// Recordings response is a map of type → []recording.
	// Just verify the command succeeds and returns a map.
	if recResp.Data == nil || string(recResp.Data) == "null" {
		// Empty recordings returns null — that's acceptable.
		return
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal(recResp.Data, &data); err != nil {
		t.Fatalf("recordings data is not a map: %v", err)
	}

	// Cross-verify: if there are recordings, pick one and verify its title
	// exists on the calendar page.
	type Recording struct {
		Title string `json:"title"`
	}
	for _, raw := range data {
		var recordings []Recording
		if err := json.Unmarshal(raw, &recordings); err != nil {
			continue
		}
		if len(recordings) > 0 && recordings[0].Title != "" {
			html := fetchHTML(t, baseURL+"/calendar")
			assertContains(t, html, recordings[0].Title)
			break
		}
	}
}

func TestRecordingsWithDateRange(t *testing.T) {
	resp := heyJSON(t, "calendars")
	type Calendar struct {
		ID int `json:"id"`
	}
	calendars := dataAs[[]Calendar](t, resp)
	if len(calendars) == 0 {
		t.Fatal("no calendars available")
	}

	calID := calendars[0].ID
	heyJSON(t, "recordings", intStr(calID),
		"--starts-on", "2024-01-01",
		"--ends-on", "2024-12-31",
	)
}

func TestRecordingsLimit(t *testing.T) {
	resp := heyJSON(t, "calendars")
	type Calendar struct {
		ID int `json:"id"`
	}
	calendars := dataAs[[]Calendar](t, resp)
	if len(calendars) == 0 {
		t.Fatal("no calendars available")
	}

	calID := calendars[0].ID
	heyJSON(t, "recordings", intStr(calID), "--limit", "5")
}

func TestRecordingsAll(t *testing.T) {
	resp := heyJSON(t, "calendars")
	type Calendar struct {
		ID int `json:"id"`
	}
	calendars := dataAs[[]Calendar](t, resp)
	if len(calendars) == 0 {
		t.Fatal("no calendars available")
	}

	calID := calendars[0].ID
	heyJSON(t, "recordings", intStr(calID), "--all")
}

func TestRecordingsNoArgument(t *testing.T) {
	heyFail(t, "recordings", "--json")
}

func TestRecordingsInvalidCalendarID(t *testing.T) {
	heyFail(t, "recordings", "999999999", "--json")
}

func TestDayView(t *testing.T) {
	type Calendar struct {
		ID int `json:"id"`
	}
	type Event struct {
		Type     string   `json:"type"`
		StartsAt string   `json:"starts_at"`
		EndsAt   string   `json:"ends_at"`
		Calendar Calendar `json:"calendar"`
	}

	const date = "2026-01-15"
	resp := heyJSON(t, "day-view", date)
	events := dataAs[[]Event](t, resp)
	if got := resp.Meta["date"]; got != date {
		t.Errorf("day-view date = %#v, want %q", got, date)
	}
	if got, _ := resp.Meta["time_zone"].(string); got == "" {
		t.Error("day-view time_zone metadata is empty")
	}
	for _, event := range events {
		if event.Type != "Calendar::Event" {
			t.Errorf("day-view returned recording type %q", event.Type)
		}
		for field, value := range map[string]string{"starts_at": event.StartsAt, "ends_at": event.EndsAt} {
			if value == "" {
				continue
			}
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				t.Errorf("day-view %s = %q, want RFC 3339 time", field, value)
				continue
			}
			_, offset := parsed.Zone()
			if offset != 0 {
				t.Errorf("day-view %s = %q, want UTC", field, value)
			}
		}
	}

	calendarResp := heyJSON(t, "calendars")
	calendars := dataAs[[]Calendar](t, calendarResp)
	if len(calendars) == 0 {
		t.Fatal("no calendars available")
	}

	calendarID := calendars[0].ID
	scopedResp := heyJSON(t, "day-view", date, "--calendar", intStr(calendarID))
	for _, event := range dataAs[[]Event](t, scopedResp) {
		if event.Calendar.ID != calendarID {
			t.Errorf("day-view event calendar = %d, want %d", event.Calendar.ID, calendarID)
		}
	}
}
