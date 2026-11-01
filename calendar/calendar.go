// Package calendar lays out a host's events on a month grid and lists the
// upcoming ones as an agenda. It is a pure read model: hosts assemble Events
// from their own sources and pass the viewer's location and the current time
// explicitly; nothing here reads the clock or the process time zone.
package calendar

import (
	"cmp"
	"slices"
	"time"
)

// GridCells is the number of cells in a MonthGrid: six Monday-first weeks.
const GridCells = 42

// Event is a host-neutral calendar entry.
//
// An all-day event (AllDay true) covers the civil dates StartDate through
// EndDate, both inclusive; a zero EndDate means the single day StartDate.
// Start and End are ignored. Its dates never shift with the location.
//
// A timed event (AllDay false) covers the half-open interval [Start, End):
// an event ending exactly at local midnight does not touch the next day. A
// zero End, or an End not after Start, makes it a point event at Start.
// StartDate and EndDate are ignored.
type Event struct {
	ID    string
	Title string
	// Kind is a free host label for the event's source, such as "invoice".
	Kind string

	AllDay    bool
	StartDate Date
	EndDate   Date

	Start time.Time
	End   time.Time
}

// span returns the first and last civil dates e touches in loc.
func (e Event) span(loc *time.Location) (first, last Date) {
	if e.AllDay {
		if e.EndDate.IsZero() || e.EndDate.Before(e.StartDate) {
			return e.StartDate, e.StartDate
		}
		return e.StartDate, e.EndDate
	}
	first = DateOf(e.Start, loc)
	if !e.End.After(e.Start) {
		return first, first
	}
	return first, DateOf(e.End.Add(-time.Nanosecond), loc)
}

// startIn returns the instant e starts at, all-day events starting at the
// beginning of their first day in loc.
func (e Event) startIn(loc *time.Location) time.Time {
	if e.AllDay {
		return e.StartDate.In(loc)
	}
	return e.Start
}

// ended reports whether e is over at now, as seen in loc. An all-day event
// ends when its last date is past; a timed event when End is not after now; a
// point event once now is past Start.
func (e Event) ended(now time.Time, loc *time.Location) bool {
	if e.AllDay {
		_, last := e.span(loc)
		return last.Before(DateOf(now, loc))
	}
	if !e.End.After(e.Start) {
		return e.Start.Before(now)
	}
	return !e.End.After(now)
}

// Cell is one day of a MonthGrid. InMonth is false for the leading and
// trailing days borrowed from the adjacent months. Events lists every event
// touching Date, in start order.
type Cell struct {
	Date    Date
	InMonth bool
	Events  []Event
}

// Grid is a month laid out as GridCells days, Monday first. Cells[0] is the
// Monday on or before the first of the month.
type Grid struct {
	Year  int
	Month time.Month
	Cells [GridCells]Cell
}

// MonthGrid lays out year/month as six Monday-first weeks and places every
// event on each day it touches in loc, including the adjacent-month days of
// the grid. Events outside the grid are dropped. loc must not be nil.
func MonthGrid(year int, month time.Month, events []Event, loc *time.Location) Grid {
	first := Date{Year: year, Month: month, Day: 1}
	gridStart := first.AddDays(-((int(first.Weekday()) + 6) % 7))
	gridEnd := gridStart.AddDays(GridCells - 1)

	g := Grid{Year: year, Month: month}
	for i := range g.Cells {
		d := gridStart.AddDays(i)
		g.Cells[i] = Cell{Date: d, InMonth: d.Month == month}
	}

	for _, e := range sortedByStart(events, loc) {
		from, to := e.span(loc)
		if from.Before(gridStart) {
			from = gridStart
		}
		if gridEnd.Before(to) {
			to = gridEnd
		}
		for d := from; !to.Before(d); d = d.AddDays(1) {
			c := &g.Cells[daysBetween(gridStart, d)]
			c.Events = append(c.Events, e)
		}
	}
	return g
}

// AgendaOptions bounds an Agenda. A zero value means no bound.
type AgendaOptions struct {
	// Limit caps the number of returned events.
	Limit int
	// Until excludes events starting at or after it.
	Until time.Time
}

// Agenda returns the events that have not ended at now, in start order
// (all-day before timed on equal starts, then input order). Events already in
// progress at now are included. All-day dates and the end of an all-day event
// are evaluated in loc, which must not be nil.
func Agenda(events []Event, now time.Time, loc *time.Location, opts AgendaOptions) []Event {
	var out []Event
	for _, e := range sortedByStart(events, loc) {
		if e.ended(now, loc) {
			continue
		}
		if !opts.Until.IsZero() && !e.startIn(loc).Before(opts.Until) {
			continue
		}
		out = append(out, e)
		if opts.Limit > 0 && len(out) == opts.Limit {
			break
		}
	}
	return out
}

// sortedByStart returns a copy of events in start order, all-day first on
// equal starts, stable otherwise.
func sortedByStart(events []Event, loc *time.Location) []Event {
	s := slices.Clone(events)
	slices.SortStableFunc(s, func(a, b Event) int {
		if c := a.startIn(loc).Compare(b.startIn(loc)); c != 0 {
			return c
		}
		return cmp.Compare(boolRank(!a.AllDay), boolRank(!b.AllDay))
	})
	return s
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}
