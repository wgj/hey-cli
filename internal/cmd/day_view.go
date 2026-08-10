package cmd

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

const dayViewEventType = "Calendar::Event"

type dayViewCommand struct {
	cmd         *cobra.Command
	calendarIDs []int64
}

type dayViewEventSource int

const (
	dayViewUnrealizedOccurrence dayViewEventSource = iota
	dayViewRealizedOccurrence
	dayViewRecording
)

// dayViewEvent shadows Recording.Id so virtual occurrences do not expose the
// generated Recording type's zero-value ID.
type dayViewEvent struct {
	generated.Recording
	ID *int64 `json:"id,omitempty"`

	source         dayViewEventSource
	allDayStartsOn string
	allDayEndsOn   string
}

func newDayViewCommand() *dayViewCommand {
	dayViewCommand := &dayViewCommand{}
	dayViewCommand.cmd = &cobra.Command{
		Use:   "day-view [YYYY-MM-DD]",
		Short: "List events in HEY Calendar Day View",
		Annotations: map[string]string{
			"agent_notes": "Use this command to answer daily schedule questions. It returns a complete event list for one HEY account-local day across all calendars by default.",
		},
		Example: `  hey day-view
  hey day-view 2026-08-10
  hey day-view 2026-08-10 --calendar 123 --calendar 456
  hey day-view --json`,
		RunE: dayViewCommand.run,
		Args: cobra.MaximumNArgs(1),
	}

	dayViewCommand.cmd.Flags().Int64SliceVar(&dayViewCommand.calendarIDs, "calendar", nil, "Calendar ID to include (repeatable, defaults to all calendars)")

	return dayViewCommand
}

func (c *dayViewCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	date := ""
	if len(args) == 1 {
		var err error
		date, err = parseDayViewDate(args[0])
		if err != nil {
			return err
		}
	}
	for _, calendarID := range c.calendarIDs {
		if calendarID <= 0 {
			return output.ErrUsage(fmt.Sprintf("invalid calendar ID: %d", calendarID))
		}
	}

	ctx := cmd.Context()
	identity, err := sdk.Identity().GetIdentity(ctx)
	if err != nil {
		return convertSDKError(err)
	}
	if identity == nil || identity.TimeZone == "" {
		return output.ErrAPI(0, "HEY account time zone is unavailable")
	}
	location, err := time.LoadLocation(identity.TimeZone)
	if err != nil {
		return output.ErrAPI(0, fmt.Sprintf("invalid HEY account time zone: %s", identity.TimeZone))
	}
	if date == "" {
		date = time.Now().In(location).Format("2006-01-02")
	}

	rangeStart, err := time.ParseInLocation("2006-01-02", date, location)
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid date: %s", date))
	}
	rangeEnd := rangeStart.AddDate(0, 0, 1)
	endsOn := rangeEnd.Format("2006-01-02")

	payload, err := sdk.Calendars().List(ctx)
	if err != nil {
		return convertSDKError(err)
	}
	calendars, err := selectDayViewCalendars(unwrapCalendars(payload), c.calendarIDs)
	if err != nil {
		return err
	}

	events := make([]dayViewEvent, 0)
	calendarIDs := make([]int64, 0, len(calendars))
	for _, calendar := range calendars {
		calendarIDs = append(calendarIDs, calendar.Id)

		calendarEvents, getErr := getDayViewCalendarEvents(ctx, calendar, date, endsOn, rangeStart, rangeEnd)
		if getErr != nil {
			return getErr
		}
		events = append(events, calendarEvents...)
	}

	events = deduplicateDayViewEvents(events)
	sortDayViewEvents(events)

	if writer.IsStyled() {
		return printDayView(cmd, events, date, identity.TimeZone, location)
	}

	return writeOK(events,
		output.WithSummary(fmt.Sprintf("%d events for %s across %d calendars", len(events), date, len(calendars))),
		output.WithMeta("date", date),
		output.WithMeta("time_zone", identity.TimeZone),
		output.WithMeta("calendar_ids", calendarIDs),
	)
}

func parseDayViewDate(date string) (string, error) {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil || parsed.Format("2006-01-02") != date {
		return "", output.ErrUsage(fmt.Sprintf("invalid date: %s", date))
	}
	return date, nil
}

func selectDayViewCalendars(calendars []generated.Calendar, calendarIDs []int64) ([]generated.Calendar, error) {
	if len(calendarIDs) == 0 {
		return calendars, nil
	}

	byID := make(map[int64]generated.Calendar, len(calendars))
	for _, calendar := range calendars {
		byID[calendar.Id] = calendar
	}

	selected := make([]generated.Calendar, 0, len(calendarIDs))
	seen := make(map[int64]bool, len(calendarIDs))
	for _, calendarID := range calendarIDs {
		if seen[calendarID] {
			continue
		}
		calendar, ok := byID[calendarID]
		if !ok {
			return nil, output.ErrNotFound("calendar", strconv.FormatInt(calendarID, 10))
		}
		selected = append(selected, calendar)
		seen[calendarID] = true
	}
	return selected, nil
}

func getDayViewCalendarEvents(
	ctx context.Context,
	calendar generated.Calendar,
	startsOn, endsOn string,
	rangeStart, rangeEnd time.Time,
) ([]dayViewEvent, error) {
	recordings, err := sdk.Calendars().GetRecordings(ctx, calendar.Id, &generated.GetCalendarRecordingsParams{
		StartsOn: startsOn,
		EndsOn:   endsOn,
	})
	if err != nil {
		return nil, dayViewSourceError("recordings", calendar, err)
	}
	if recordings == nil || *recordings == nil {
		return nil, dayViewEmptySourceError("recordings", calendar)
	}

	occurrences, err := sdk.Calendars().GetOccurrences(ctx, calendar.Id, &generated.GetCalendarOccurrencesParams{
		StartsOn: startsOn,
		EndsOn:   endsOn,
	})
	if err != nil {
		return nil, dayViewSourceError("occurrences", calendar, err)
	}
	if occurrences == nil || *occurrences == nil {
		return nil, dayViewEmptySourceError("occurrences", calendar)
	}

	events := dayViewEventsFromRecordings(recordings, calendar, rangeStart, rangeEnd)
	events = append(events, dayViewEventsFromOccurrences(occurrences, calendar, rangeStart, rangeEnd)...)
	return events, nil
}

func dayViewSourceError(source string, calendar generated.Calendar, err error) error {
	converted := output.AsError(convertSDKError(err))
	return &output.Error{
		Code:       converted.Code,
		Message:    fmt.Sprintf("could not load %s for calendar %q: %s", source, calendar.Name, converted.Message),
		Hint:       converted.Hint,
		HTTPStatus: converted.HTTPStatus,
		Retryable:  converted.Retryable,
		Cause:      converted,
	}
}

func dayViewEmptySourceError(source string, calendar generated.Calendar) error {
	return output.ErrAPI(0, fmt.Sprintf("%s for calendar %q returned no data", source, calendar.Name))
}

func dayViewEventsFromRecordings(
	response *generated.CalendarRecordingsResponse,
	calendar generated.Calendar,
	rangeStart, rangeEnd time.Time,
) []dayViewEvent {
	recordings := (*response)[dayViewEventType]
	events := make([]dayViewEvent, 0, len(recordings))
	for _, recording := range recordings {
		if isDayViewRecurringMaster(recording) || !dayViewEventOverlaps(recording, rangeStart, rangeEnd) {
			continue
		}
		events = append(events, newDayViewEvent(recording, recordingID(recording.Id), calendar, dayViewRecording))
	}
	return events
}

func dayViewEventsFromOccurrences(
	response *generated.CalendarOccurrencesResponse,
	calendar generated.Calendar,
	rangeStart, rangeEnd time.Time,
) []dayViewEvent {
	group, ok := (*response)[dayViewEventType]
	if !ok {
		return nil
	}

	events := make([]dayViewEvent, 0, len(group.Realized)+len(group.Unrealized))
	for _, occurrence := range group.Realized {
		recording := recordingFromOccurrence(occurrence)
		if dayViewEventOverlaps(recording, rangeStart, rangeEnd) {
			events = append(events, newDayViewEvent(recording, occurrence.Id, calendar, dayViewRealizedOccurrence))
		}
	}
	for _, occurrence := range group.Unrealized {
		recording := recordingFromOccurrence(occurrence)
		if dayViewEventOverlaps(recording, rangeStart, rangeEnd) {
			events = append(events, newDayViewEvent(recording, occurrence.Id, calendar, dayViewUnrealizedOccurrence))
		}
	}
	return events
}

func recordingFromOccurrence(occurrence generated.CalendarOccurrence) generated.Recording {
	recording := generated.Recording{
		AllDay:             occurrence.AllDay,
		AttachedEntry:      occurrence.AttachedEntry,
		AttendanceStatus:   occurrence.AttendanceStatus,
		Attendances:        occurrence.Attendances,
		AttendancesSummary: occurrence.AttendancesSummary,
		Calendar:           occurrence.Calendar,
		CreatedAt:          occurrence.CreatedAt,
		Description:        occurrence.Description,
		EditUrl:            occurrence.EditUrl,
		EndsAt:             occurrence.EndsAt,
		EndsAtTimeZone:     occurrence.EndsAtTimeZone,
		JoinLink:           occurrence.JoinLink,
		Location:           occurrence.Location,
		ManageAttendance:   occurrence.ManageAttendance,
		OccurrenceId:       occurrence.OccurrenceId,
		Organizer:          occurrence.Organizer,
		Parent:             occurrence.Parent,
		ParentId:           occurrence.ParentId,
		RecurrenceSchedule: occurrence.RecurrenceSchedule,
		Recurring:          occurrence.Recurring,
		Reminders:          occurrence.Reminders,
		RemindersLabel:     occurrence.RemindersLabel,
		StartsAt:           occurrence.StartsAt,
		StartsAtTimeZone:   occurrence.StartsAtTimeZone,
		Summary:            occurrence.Summary,
		Title:              occurrence.Title,
		Type:               occurrence.Type,
		UpdatedAt:          occurrence.UpdatedAt,
		Url:                occurrence.Url,
	}
	if occurrence.Id != nil {
		recording.Id = *occurrence.Id
	}
	// Do not synthesize an occurrence ID. Merge only identities supplied by HEY.
	return recording
}

func newDayViewEvent(
	recording generated.Recording,
	id *int64,
	calendar generated.Calendar,
	source dayViewEventSource,
) dayViewEvent {
	recording.Calendar.Id = calendar.Id
	recording.Calendar.Name = calendar.Name
	if recording.Type == "" {
		recording.Type = dayViewEventType
	}

	event := dayViewEvent{
		Recording: recording,
		ID:        recordingIDPointer(id),
		source:    source,
	}
	if event.AllDay {
		event.allDayStartsOn = dayViewCivilDateString(event.StartsAt)
		event.allDayEndsOn = dayViewCivilDateString(event.EndsAt)
		if event.allDayEndsOn == "" {
			event.allDayEndsOn = event.allDayStartsOn
		}
	}
	if !event.StartsAt.IsZero() {
		event.StartsAt = event.StartsAt.UTC()
	}
	if !event.EndsAt.IsZero() {
		event.EndsAt = event.EndsAt.UTC()
	}
	return event
}

func recordingID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func recordingIDPointer(id *int64) *int64 {
	if id == nil || *id == 0 {
		return nil
	}
	value := *id
	return &value
}

func isDayViewRecurringMaster(recording generated.Recording) bool {
	return recording.Recurring && recording.OccurrenceId == "" && recording.ParentId == 0
}

func dayViewEventOverlaps(recording generated.Recording, rangeStart, rangeEnd time.Time) bool {
	if recording.StartsAt.IsZero() || !rangeEnd.After(rangeStart) {
		return false
	}

	startsAt, endsAt := recording.StartsAt, recording.EndsAt
	if recording.AllDay {
		startsAt = dayViewCivilDate(startsAt, rangeStart.Location())
		if endsAt.IsZero() {
			endsAt = startsAt
		} else {
			endsAt = dayViewCivilDate(endsAt, rangeStart.Location())
		}
		// HEY represents the final visible all-day date in ends_at.
		endsAt = endsAt.AddDate(0, 0, 1)
		if !endsAt.After(startsAt) {
			endsAt = startsAt.AddDate(0, 0, 1)
		}
	} else if !endsAt.After(startsAt) {
		// Timed values include full dates, so valid overnight events already end
		// on the following date. Missing or non-positive ends are point events.
		endsAt = startsAt.Add(time.Nanosecond)
	}

	return startsAt.Before(rangeEnd) && endsAt.After(rangeStart)
}

func dayViewCivilDate(value time.Time, location *time.Location) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, location)
}

func dayViewCivilDateString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}

func deduplicateDayViewEvents(events []dayViewEvent) []dayViewEvent {
	if len(events) < 2 {
		return events
	}

	parents := make([]int, len(events))
	for index := range parents {
		parents[index] = index
	}
	find := func(index int) int {
		for parents[index] != index {
			parents[index] = parents[parents[index]]
			index = parents[index]
		}
		return index
	}
	union := func(left, right int) {
		leftRoot, rightRoot := find(left), find(right)
		if leftRoot != rightRoot {
			parents[rightRoot] = leftRoot
		}
	}

	// HEY recording and occurrence identities are account-wide. Either matching
	// key proves identity, so connected aliases remain one event even when the
	// first and last source rows do not carry the same key.
	ids := make(map[int64]int, len(events))
	occurrenceIDs := make(map[string]int, len(events))
	for index, event := range events {
		if event.ID != nil {
			if other, ok := ids[*event.ID]; ok {
				union(index, other)
			} else {
				ids[*event.ID] = index
			}
		}
		if event.OccurrenceId != "" {
			if other, ok := occurrenceIDs[event.OccurrenceId]; ok {
				union(index, other)
			} else {
				occurrenceIDs[event.OccurrenceId] = index
			}
		}
	}

	groups := make(map[int][]int, len(events))
	order := make([]int, 0, len(events))
	for index := range events {
		root := find(index)
		if _, ok := groups[root]; !ok {
			order = append(order, root)
		}
		groups[root] = append(groups[root], index)
	}

	result := make([]dayViewEvent, 0, len(order))
	for _, root := range order {
		indices := groups[root]
		winner := indices[0]
		for _, index := range indices[1:] {
			if events[index].source > events[winner].source {
				winner = index
			}
		}

		merged := events[winner]
		for _, index := range indices {
			if index != winner {
				merged = mergeDayViewEvents(merged, events[index])
			}
		}
		result = append(result, merged)
	}
	return result
}

func mergeDayViewEvents(current, candidate dayViewEvent) dayViewEvent {
	winner, other := current, candidate
	if candidate.source > current.source {
		winner, other = candidate, current
	}
	if winner.ID == nil && other.ID != nil {
		winner.ID = recordingIDPointer(other.ID)
		winner.Id = *winner.ID
	}
	if winner.OccurrenceId == "" {
		winner.OccurrenceId = other.OccurrenceId
	}
	if winner.allDayStartsOn == "" {
		winner.allDayStartsOn = other.allDayStartsOn
	}
	if winner.allDayEndsOn == "" {
		winner.allDayEndsOn = other.allDayEndsOn
	}
	return winner
}

func sortDayViewEvents(events []dayViewEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		left, right := events[i], events[j]
		if left.AllDay != right.AllDay {
			return left.AllDay
		}
		if left.AllDay {
			if left.allDayStartsOn != right.allDayStartsOn {
				return left.allDayStartsOn < right.allDayStartsOn
			}
			if left.allDayEndsOn != right.allDayEndsOn {
				return left.allDayEndsOn < right.allDayEndsOn
			}
			return false
		}
		if !left.StartsAt.Equal(right.StartsAt) {
			return left.StartsAt.Before(right.StartsAt)
		}
		leftEnd := dayViewEffectiveEnd(left.Recording)
		rightEnd := dayViewEffectiveEnd(right.Recording)
		if !leftEnd.Equal(rightEnd) {
			return leftEnd.Before(rightEnd)
		}
		return false
	})
}

func dayViewEffectiveEnd(recording generated.Recording) time.Time {
	if recording.EndsAt.After(recording.StartsAt) {
		return recording.EndsAt
	}
	return recording.StartsAt
}

func printDayView(
	cmd *cobra.Command,
	events []dayViewEvent,
	date, timeZone string,
	location *time.Location,
) error {
	if len(events) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No events for %s (%s).\n", date, timeZone)
		return nil
	}

	table := newTable(cmd.OutOrStdout())
	table.addRow([]string{"ID", "Calendar", "Title", "Starts", "Ends"})
	for _, event := range events {
		table.addRow([]string{
			dayViewDisplayID(event.ID),
			event.Calendar.Name,
			event.Title,
			formatDayViewTimestamp(event, false, location),
			formatDayViewTimestamp(event, true, location),
		})
	}
	table.print()
	return nil
}

func dayViewDisplayID(id *int64) string {
	if id == nil {
		return ""
	}
	return strconv.FormatInt(*id, 10)
}

func formatDayViewTimestamp(event dayViewEvent, end bool, location *time.Location) string {
	if event.AllDay {
		if end {
			return event.allDayEndsOn
		}
		return event.allDayStartsOn
	}

	timestamp := event.StartsAt
	if end {
		timestamp = event.EndsAt
	}
	if timestamp.IsZero() {
		return ""
	}
	return timestamp.In(location).Format("2006-01-02T15:04")
}
