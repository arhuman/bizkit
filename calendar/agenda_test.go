package calendar

import (
	"slices"
	"testing"
	"time"
)

func ids(events []Event) []string {
	var out []string
	for _, e := range events {
		out = append(out, e.ID)
	}
	return out
}

func agendaFixture(paris *time.Location) []Event {
	return []Event{
		{ID: "next-week", Start: time.Date(2026, 6, 17, 9, 0, 0, 0, paris), End: time.Date(2026, 6, 17, 10, 0, 0, 0, paris)},
		{ID: "past", Start: time.Date(2026, 6, 9, 9, 0, 0, 0, paris), End: time.Date(2026, 6, 9, 10, 0, 0, 0, paris)},
		{ID: "just-ended", Start: time.Date(2026, 6, 10, 11, 0, 0, 0, paris), End: time.Date(2026, 6, 10, 12, 0, 0, 0, paris)},
		{ID: "in-progress", Start: time.Date(2026, 6, 10, 11, 30, 0, 0, paris), End: time.Date(2026, 6, 10, 13, 0, 0, 0, paris)},
		{ID: "later-today", Start: time.Date(2026, 6, 10, 15, 0, 0, 0, paris)},
		{ID: "past-point", Start: time.Date(2026, 6, 10, 8, 0, 0, 0, paris)},
		{ID: "today-allday", AllDay: true, StartDate: Date{2026, time.June, 10}},
		{ID: "yesterday-allday", AllDay: true, StartDate: Date{2026, time.June, 9}},
		{ID: "tomorrow", Start: time.Date(2026, 6, 11, 9, 0, 0, 0, paris), End: time.Date(2026, 6, 11, 10, 0, 0, 0, paris)},
		{ID: "tomorrow-allday", AllDay: true, StartDate: Date{2026, time.June, 11}, EndDate: Date{2026, time.June, 12}},
	}
}

var agendaNow = time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC) // 12:00 in Paris

func TestAgenda_ExcludesPastAndOrdersByStart(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	got := ids(Agenda(agendaFixture(paris), agendaNow, paris, AgendaOptions{}))
	want := []string{"today-allday", "in-progress", "later-today", "tomorrow-allday", "tomorrow", "next-week"}
	if !slices.Equal(got, want) {
		t.Errorf("Agenda = %v, want %v", got, want)
	}
}

func TestAgenda_InProgressIncluded(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	events := []Event{
		{ID: "meeting", Start: time.Date(2026, 6, 10, 11, 0, 0, 0, paris), End: time.Date(2026, 6, 10, 12, 0, 0, 1, paris)},
		{ID: "ends-now", Start: time.Date(2026, 6, 10, 11, 0, 0, 0, paris), End: time.Date(2026, 6, 10, 12, 0, 0, 0, paris)},
		{ID: "point-now", Start: time.Date(2026, 6, 10, 12, 0, 0, 0, paris)},
	}
	got := ids(Agenda(events, agendaNow, paris, AgendaOptions{}))
	if want := []string{"meeting", "point-now"}; !slices.Equal(got, want) {
		t.Errorf("Agenda = %v, want %v", got, want)
	}
}

func TestAgenda_AllDayEndsWithItsLocalDay(t *testing.T) {
	events := []Event{{ID: "holiday", AllDay: true, StartDate: Date{2026, time.June, 10}}}
	// 2026-06-10 23:00 UTC is still 2026-06-10 in Los Angeles but 2026-06-11 in Paris.
	now := time.Date(2026, 6, 10, 23, 0, 0, 0, time.UTC)
	if got := ids(Agenda(events, now, mustLoc(t, "America/Los_Angeles"), AgendaOptions{})); !slices.Equal(got, []string{"holiday"}) {
		t.Errorf("Los Angeles agenda = %v, want [holiday]", got)
	}
	if got := ids(Agenda(events, now, mustLoc(t, "Europe/Paris"), AgendaOptions{})); got != nil {
		t.Errorf("Paris agenda = %v, want none", got)
	}
}

func TestAgenda_Limit(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	got := ids(Agenda(agendaFixture(paris), agendaNow, paris, AgendaOptions{Limit: 2}))
	if want := []string{"today-allday", "in-progress"}; !slices.Equal(got, want) {
		t.Errorf("Agenda = %v, want %v", got, want)
	}
}

func TestAgenda_Horizon(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	until := time.Date(2026, 6, 11, 9, 0, 0, 0, paris)
	got := ids(Agenda(agendaFixture(paris), agendaNow, paris, AgendaOptions{Until: until}))
	if want := []string{"today-allday", "in-progress", "later-today", "tomorrow-allday"}; !slices.Equal(got, want) {
		t.Errorf("Agenda = %v, want %v", got, want)
	}
}

func TestAgenda_DoesNotModifyInput(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	events := agendaFixture(paris)
	Agenda(events, agendaNow, paris, AgendaOptions{})
	if events[0].ID != "next-week" {
		t.Errorf("input reordered: events[0] = %q", events[0].ID)
	}
}
