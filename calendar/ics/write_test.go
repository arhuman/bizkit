package ics

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arhuman/bizkit/calendar"
)

var opts = Options{Domain: "easybiz.example"}

func encode(t *testing.T, f Feed, o Options) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, f, o); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.String()
}

func requireLine(t *testing.T, out, line string) {
	t.Helper()
	if !strings.Contains(out, "\r\n"+line+"\r\n") {
		t.Errorf("missing line %q in:\n%s", line, out)
	}
}

func refuteContains(t *testing.T, out, s string) {
	t.Helper()
	if strings.Contains(out, s) {
		t.Errorf("unexpected %q in:\n%s", s, out)
	}
}

func day(y int, m time.Month, d int) calendar.Date {
	return calendar.Date{Year: y, Month: m, Day: d}
}

func TestWrite_AllDayUsesValueDate(t *testing.T) {
	out := encode(t, Feed{Events: []calendar.Event{
		{ID: "1", Title: "Trip", AllDay: true, StartDate: day(2026, 6, 9), EndDate: day(2026, 6, 11)},
	}}, opts)
	requireLine(t, out, "DTSTART;VALUE=DATE:20260609")
	requireLine(t, out, "DTEND;VALUE=DATE:20260612")
}

func TestWrite_AllDaySingleDayEndsNextDay(t *testing.T) {
	for name, end := range map[string]calendar.Date{
		"zero end":         {},
		"end before start": day(2026, 6, 1),
		"end equals start": day(2026, 12, 31),
	} {
		t.Run(name, func(t *testing.T) {
			out := encode(t, Feed{Events: []calendar.Event{
				{ID: "1", AllDay: true, StartDate: day(2026, 12, 31), EndDate: end},
			}}, opts)
			requireLine(t, out, "DTSTART;VALUE=DATE:20261231")
			requireLine(t, out, "DTEND;VALUE=DATE:20270101")
		})
	}
}

func TestWrite_TimedEventInUTC(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	out := encode(t, Feed{Events: []calendar.Event{
		{ID: "1", Start: time.Date(2026, 6, 9, 14, 30, 0, 0, paris), End: time.Date(2026, 6, 9, 16, 0, 0, 0, paris)},
	}}, opts)
	requireLine(t, out, "DTSTART:20260609T123000Z")
	requireLine(t, out, "DTEND:20260609T140000Z")
	refuteContains(t, out, "TZID")
}

func TestWrite_TimedPointEventHasNoDTEND(t *testing.T) {
	start := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)
	for name, end := range map[string]time.Time{"zero end": {}, "end before start": start.Add(-time.Hour)} {
		t.Run(name, func(t *testing.T) {
			out := encode(t, Feed{Events: []calendar.Event{{ID: "1", Start: start, End: end}}}, opts)
			requireLine(t, out, "DTSTART:20260115T090000Z")
			refuteContains(t, out, "DTEND")
		})
	}
}

func TestWrite_EscapesText(t *testing.T) {
	out := encode(t, Feed{Name: "Team, Paris", Events: []calendar.Event{
		{ID: "1", Title: "Lunch, team; notes\nline2\\x\r\nline3\rline4", Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}}, opts)
	requireLine(t, out, `SUMMARY:Lunch\, team\; notes\nline2\\x\nline3\nline4`)
	requireLine(t, out, `X-WR-CALNAME:Team\, Paris`)
}

func TestWrite_FoldsLongLines(t *testing.T) {
	t.Run("ascii", func(t *testing.T) {
		out := encode(t, Feed{Events: []calendar.Event{
			{ID: "1", Title: strings.Repeat("a", 100), Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		}}, opts)
		requireLine(t, out, "SUMMARY:"+strings.Repeat("a", 67)+"\r\n "+strings.Repeat("a", 33))
	})
	t.Run("never splits a UTF-8 sequence", func(t *testing.T) {
		out := encode(t, Feed{Events: []calendar.Event{
			{ID: "1", Title: strings.Repeat("é", 40), Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		}}, opts)
		requireLine(t, out, "SUMMARY:"+strings.Repeat("é", 33)+"\r\n "+strings.Repeat("é", 7))
	})
	t.Run("continuations stay within 75 octets", func(t *testing.T) {
		out := encode(t, Feed{Events: []calendar.Event{
			{ID: "1", Title: strings.Repeat("b", 300), Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		}}, opts)
		for line := range strings.SplitSeq(out, "\r\n") {
			if len(line) > 75 {
				t.Errorf("line of %d octets: %q", len(line), line)
			}
		}
		requireLine(t, out, " "+strings.Repeat("b", 74))
	})
}

func TestWrite_UIDAndDTSTAMPAreStable(t *testing.T) {
	f := Feed{
		Updated: time.Date(2026, 1, 1, 9, 0, 0, 0, time.FixedZone("CET", 3600)),
		Events: []calendar.Event{
			{ID: "42", Kind: "invoice", AllDay: true, StartDate: day(2026, 3, 2)},
			{ID: "7", AllDay: true, StartDate: day(2026, 3, 3)},
		},
	}
	first := encode(t, f, opts)
	requireLine(t, first, "UID:invoice.42@easybiz.example")
	requireLine(t, first, "UID:7@easybiz.example")
	requireLine(t, first, "DTSTAMP:20260101T080000Z")
	if second := encode(t, f, opts); second != first {
		t.Errorf("output not byte-stable:\n%s\nvs\n%s", first, second)
	}

	zero := encode(t, Feed{Events: f.Events[1:]}, opts)
	requireLine(t, zero, "DTSTAMP:19700101T000000Z")
}

func TestWrite_FullDocument(t *testing.T) {
	out := encode(t, Feed{
		Name:    "Team",
		Updated: time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC),
		Events:  []calendar.Event{{ID: "7", Title: "Holiday", AllDay: true, StartDate: day(2026, 6, 9)}},
	}, opts)
	want := "BEGIN:VCALENDAR\r\n" +
		"CALSCALE:GREGORIAN\r\n" +
		"PRODID:-//arhuman//bizkit//EN\r\n" +
		"VERSION:2.0\r\n" +
		"X-WR-CALNAME:Team\r\n" +
		"BEGIN:VEVENT\r\n" +
		"DTEND;VALUE=DATE:20260610\r\n" +
		"DTSTAMP:20260101T080000Z\r\n" +
		"DTSTART;VALUE=DATE:20260609\r\n" +
		"SUMMARY:Holiday\r\n" +
		"UID:7@easybiz.example\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
	if out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestWrite_EmptyFeed(t *testing.T) {
	out := encode(t, Feed{}, Options{Domain: "easybiz.example", ProdID: "-//acme//feed//EN"})
	want := "BEGIN:VCALENDAR\r\n" +
		"CALSCALE:GREGORIAN\r\n" +
		"PRODID:-//acme//feed//EN\r\n" +
		"VERSION:2.0\r\n" +
		"END:VCALENDAR\r\n"
	if out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestWrite_RejectsInvalidInput(t *testing.T) {
	cases := map[string]struct {
		event  calendar.Event
		domain string
	}{
		"empty domain":       {calendar.Event{ID: "1", AllDay: true, StartDate: day(2026, 1, 1)}, ""},
		"empty ID":           {calendar.Event{AllDay: true, StartDate: day(2026, 1, 1)}, "easybiz.example"},
		"zero all-day start": {calendar.Event{ID: "1", AllDay: true}, "easybiz.example"},
		"zero timed start":   {calendar.Event{ID: "1"}, "easybiz.example"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Write(&buf, Feed{Events: []calendar.Event{tc.event}}, Options{Domain: tc.domain}); err == nil {
				t.Fatal("Write succeeded, want error")
			}
			if buf.Len() != 0 {
				t.Errorf("wrote %d bytes on error", buf.Len())
			}
		})
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWrite_PropagatesWriterError(t *testing.T) {
	if err := Write(failWriter{}, Feed{}, opts); err == nil || err.Error() != "disk full" {
		t.Errorf("err = %v, want disk full", err)
	}
}
