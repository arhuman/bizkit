package calendar

import (
	"fmt"
	"time"
)

// Date is a civil calendar date with no time of day and no location. It is
// how all-day events are expressed, so they never shift with a time zone or
// a DST change.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf returns the civil date of t as seen in loc. loc must not be nil.
func DateOf(t time.Time, loc *time.Location) Date {
	y, m, d := t.In(loc).Date()
	return Date{Year: y, Month: m, Day: d}
}

// IsZero reports whether d is the zero Date.
func (d Date) IsZero() bool {
	return d == Date{}
}

// AddDays returns d moved by n days (negative n moves backwards). Out of
// range fields are normalized, so Date{2026, 1, 32}.AddDays(0) is 1 February.
func (d Date) AddDays(n int) Date {
	y, m, day := d.utc().AddDate(0, 0, n).Date()
	return Date{Year: y, Month: m, Day: day}
}

// Before reports whether d is strictly earlier than o.
func (d Date) Before(o Date) bool {
	return d.utc().Before(o.utc())
}

// Weekday returns the day of the week of d.
func (d Date) Weekday() time.Weekday {
	return d.utc().Weekday()
}

// In returns the instant at which d begins in loc. On a day whose midnight
// is skipped by DST, time.Date normalization applies. loc must not be nil.
func (d Date) In(loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// String formats d as YYYY-MM-DD.
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

// utc anchors d at midnight UTC, where every day is exactly 24 hours long.
func (d Date) utc() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

// daysBetween returns the number of days from a to b.
func daysBetween(a, b Date) int {
	return int(b.utc().Sub(a.utc()).Hours() / 24)
}
