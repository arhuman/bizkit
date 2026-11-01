package calendar

import (
	"slices"
	"testing"
	"time"
	_ "time/tzdata" // tests must not depend on the CI image shipping zoneinfo
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

// daysWith returns the dates (YYYY-MM-DD) of the cells listing the event id.
func daysWith(g Grid, id string) []string {
	var days []string
	for _, c := range g.Cells {
		for _, e := range c.Events {
			if e.ID == id {
				days = append(days, c.Date.String())
			}
		}
	}
	return days
}

func assertDays(t *testing.T, g Grid, id string, want []string) {
	t.Helper()
	if got := daysWith(g, id); !slices.Equal(got, want) {
		t.Errorf("event %q on %v, want %v", id, got, want)
	}
}

func TestMonthGrid_MonthStartingOnMonday(t *testing.T) {
	g := MonthGrid(2026, time.June, nil, time.UTC)
	if len(g.Cells) != 42 {
		t.Fatalf("len(Cells) = %d, want 42", len(g.Cells))
	}
	checks := []struct {
		i       int
		date    string
		inMonth bool
	}{
		{0, "2026-06-01", true},
		{29, "2026-06-30", true},
		{30, "2026-07-01", false},
		{41, "2026-07-12", false},
	}
	for _, c := range checks {
		cell := g.Cells[c.i]
		if cell.Date.String() != c.date || cell.InMonth != c.inMonth {
			t.Errorf("Cells[%d] = %s inMonth=%v, want %s inMonth=%v", c.i, cell.Date, cell.InMonth, c.date, c.inMonth)
		}
	}
	if g.Cells[0].Date.Weekday() != time.Monday {
		t.Errorf("Cells[0] is a %v, want Monday", g.Cells[0].Date.Weekday())
	}
}

func TestMonthGrid_MonthStartingOnSunday(t *testing.T) {
	g := MonthGrid(2026, time.March, nil, time.UTC)
	checks := []struct {
		i       int
		date    string
		inMonth bool
	}{
		{0, "2026-02-23", false},
		{5, "2026-02-28", false},
		{6, "2026-03-01", true},
		{36, "2026-03-31", true},
		{37, "2026-04-01", false},
		{41, "2026-04-05", false},
	}
	for _, c := range checks {
		cell := g.Cells[c.i]
		if cell.Date.String() != c.date || cell.InMonth != c.inMonth {
			t.Errorf("Cells[%d] = %s inMonth=%v, want %s inMonth=%v", c.i, cell.Date, cell.InMonth, c.date, c.inMonth)
		}
	}
	if g.Year != 2026 || g.Month != time.March {
		t.Errorf("grid = %d-%v, want 2026-March", g.Year, g.Month)
	}
}

func TestMonthGrid_MultiDaySpansEachDay(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	events := []Event{
		{ID: "trip", Start: time.Date(2026, 6, 9, 14, 0, 0, 0, paris), End: time.Date(2026, 6, 12, 10, 0, 0, 0, paris)},
		{ID: "fair", AllDay: true, StartDate: Date{2026, time.June, 15}, EndDate: Date{2026, time.June, 17}},
		{ID: "single", AllDay: true, StartDate: Date{2026, time.June, 20}},
	}
	g := MonthGrid(2026, time.June, events, paris)
	assertDays(t, g, "trip", []string{"2026-06-09", "2026-06-10", "2026-06-11", "2026-06-12"})
	assertDays(t, g, "fair", []string{"2026-06-15", "2026-06-16", "2026-06-17"})
	assertDays(t, g, "single", []string{"2026-06-20"})
}

func TestMonthGrid_MonthStraddlingEvent(t *testing.T) {
	events := []Event{
		{ID: "into", AllDay: true, StartDate: Date{2026, time.February, 26}, EndDate: Date{2026, time.March, 2}},
		{ID: "out", AllDay: true, StartDate: Date{2026, time.March, 30}, EndDate: Date{2026, time.April, 7}},
		{ID: "before", AllDay: true, StartDate: Date{2026, time.January, 1}, EndDate: Date{2026, time.February, 22}},
		{ID: "over", AllDay: true, StartDate: Date{2026, time.January, 1}, EndDate: Date{2026, time.December, 31}},
	}
	g := MonthGrid(2026, time.March, events, time.UTC)
	assertDays(t, g, "into", []string{"2026-02-26", "2026-02-27", "2026-02-28", "2026-03-01", "2026-03-02"})
	assertDays(t, g, "out", []string{"2026-03-30", "2026-03-31", "2026-04-01", "2026-04-02", "2026-04-03", "2026-04-04", "2026-04-05"})
	assertDays(t, g, "before", nil)
	if n := len(daysWith(g, "over")); n != 42 {
		t.Errorf("year-long event on %d cells, want 42", n)
	}
}

func TestMonthGrid_AllDayNeverShifts(t *testing.T) {
	events := []Event{
		{ID: "spring", AllDay: true, StartDate: Date{2026, time.March, 29}},
		{ID: "autumn", AllDay: true, StartDate: Date{2026, time.October, 25}, EndDate: Date{2026, time.October, 26}},
	}
	for _, name := range []string{"Europe/Paris", "Pacific/Auckland", "America/Los_Angeles", "UTC"} {
		t.Run(name, func(t *testing.T) {
			loc := mustLoc(t, name)
			assertDays(t, MonthGrid(2026, time.March, events, loc), "spring", []string{"2026-03-29"})
			assertDays(t, MonthGrid(2026, time.October, events, loc), "autumn", []string{"2026-10-25", "2026-10-26"})
		})
	}
}

func TestMonthGrid_TimedEventCrossesLocalMidnight(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	auckland := mustLoc(t, "Pacific/Auckland")
	// 2026-06-10 22:00 to 2026-06-11 02:00 in Paris is 20:00 to 00:00 UTC.
	party := Event{ID: "party", Start: time.Date(2026, 6, 10, 22, 0, 0, 0, paris), End: time.Date(2026, 6, 11, 2, 0, 0, 0, paris)}
	events := []Event{party}

	assertDays(t, MonthGrid(2026, time.June, events, paris), "party", []string{"2026-06-10", "2026-06-11"})
	assertDays(t, MonthGrid(2026, time.June, events, time.UTC), "party", []string{"2026-06-10"})
	// Auckland is UTC+12 in June: 08:00 to 12:00 on 2026-06-11.
	assertDays(t, MonthGrid(2026, time.June, events, auckland), "party", []string{"2026-06-11"})
}

func TestMonthGrid_EndAtMidnightIsExclusive(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	events := []Event{
		{ID: "evening", Start: time.Date(2026, 6, 10, 20, 0, 0, 0, paris), End: time.Date(2026, 6, 11, 0, 0, 0, 0, paris)},
		{ID: "day", Start: time.Date(2026, 6, 12, 0, 0, 0, 0, paris), End: time.Date(2026, 6, 13, 0, 0, 0, 0, paris)},
		{ID: "point", Start: time.Date(2026, 6, 14, 0, 0, 0, 0, paris)},
		{ID: "inverted", Start: time.Date(2026, 6, 15, 9, 0, 0, 0, paris), End: time.Date(2026, 6, 14, 9, 0, 0, 0, paris)},
	}
	g := MonthGrid(2026, time.June, events, paris)
	assertDays(t, g, "evening", []string{"2026-06-10"})
	assertDays(t, g, "day", []string{"2026-06-12"})
	assertDays(t, g, "point", []string{"2026-06-14"})
	assertDays(t, g, "inverted", []string{"2026-06-15"})
}

func TestMonthGrid_DSTTransitionDays(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	events := []Event{
		// 2026-03-29: clocks jump 02:00 CET to 03:00 CEST; the day is 23 hours.
		{ID: "spring-day", Start: time.Date(2026, 3, 29, 0, 0, 0, 0, paris), End: time.Date(2026, 3, 30, 0, 0, 0, 0, paris)},
		{ID: "spring-night", Start: time.Date(2026, 3, 29, 1, 30, 0, 0, paris), End: time.Date(2026, 3, 29, 3, 30, 0, 0, paris)},
		{ID: "spring-late", Start: time.Date(2026, 3, 29, 23, 30, 0, 0, paris), End: time.Date(2026, 3, 30, 0, 30, 0, 0, paris)},
		// 2026-10-25: clocks fall back 03:00 CEST to 02:00 CET; the day is 25 hours.
		{ID: "autumn-day", Start: time.Date(2026, 10, 25, 0, 0, 0, 0, paris), End: time.Date(2026, 10, 26, 0, 0, 0, 0, paris)},
		{ID: "autumn-late", Start: time.Date(2026, 10, 25, 23, 30, 0, 0, paris), End: time.Date(2026, 10, 26, 0, 30, 0, 0, paris)},
	}
	assertDays(t, MonthGrid(2026, time.March, events, paris), "spring-day", []string{"2026-03-29"})
	assertDays(t, MonthGrid(2026, time.March, events, paris), "spring-night", []string{"2026-03-29"})
	assertDays(t, MonthGrid(2026, time.March, events, paris), "spring-late", []string{"2026-03-29", "2026-03-30"})
	assertDays(t, MonthGrid(2026, time.October, events, paris), "autumn-day", []string{"2026-10-25"})
	assertDays(t, MonthGrid(2026, time.October, events, paris), "autumn-late", []string{"2026-10-25", "2026-10-26"})
}

func TestMonthGrid_CellEventsInStartOrder(t *testing.T) {
	paris := mustLoc(t, "Europe/Paris")
	events := []Event{
		{ID: "late", Start: time.Date(2026, 6, 10, 18, 0, 0, 0, paris)},
		{ID: "early", Start: time.Date(2026, 6, 10, 8, 0, 0, 0, paris)},
		{ID: "allday", AllDay: true, StartDate: Date{2026, time.June, 10}},
	}
	g := MonthGrid(2026, time.June, events, paris)
	var got []string
	for _, e := range g.Cells[9].Events {
		got = append(got, e.ID)
	}
	if want := []string{"allday", "early", "late"}; !slices.Equal(got, want) {
		t.Errorf("2026-06-10 events = %v, want %v", got, want)
	}
}

func TestDate_Helpers(t *testing.T) {
	if got := (Date{2026, time.January, 32}).AddDays(0).String(); got != "2026-02-01" {
		t.Errorf("normalized = %s, want 2026-02-01", got)
	}
	if got := (Date{2026, time.March, 1}).AddDays(-1).String(); got != "2026-02-28" {
		t.Errorf("AddDays(-1) = %s, want 2026-02-28", got)
	}
	la := mustLoc(t, "America/Los_Angeles")
	if got := DateOf(time.Date(2026, 6, 11, 3, 0, 0, 0, time.UTC), la).String(); got != "2026-06-10" {
		t.Errorf("DateOf = %s, want 2026-06-10", got)
	}
	if !(Date{}).IsZero() || (Date{2026, time.June, 1}).IsZero() {
		t.Error("IsZero wrong")
	}
}
