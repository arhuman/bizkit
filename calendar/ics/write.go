// Package ics renders calendar.Events as an RFC 5545 iCalendar feed and
// serves it over HTTP behind a host-resolved feed token.
//
// Output is byte-stable: the same Feed and Options always encode to the same
// bytes, so the FeedHandler ETag only changes when the content does. Nothing
// here reads the clock; DTSTAMP comes from Feed.Updated.
package ics

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-ical"

	"github.com/arhuman/bizkit/calendar"
)

// DefaultProdID is the PRODID written when Options.ProdID is empty.
const DefaultProdID = "-//arhuman//bizkit//EN"

// maxLineOctets is the RFC 5545 section 3.1 line length limit, CRLF excluded.
const maxLineOctets = 75

// Feed is the content of one calendar feed.
type Feed struct {
	// Name is written as X-WR-CALNAME when not empty.
	Name   string
	Events []calendar.Event
	// Updated is the DTSTAMP of every event, typically the latest change to
	// any of them. A zero Updated writes 19700101T000000Z, keeping the
	// output deterministic.
	Updated time.Time
}

// Options configure the encoding of a Feed.
type Options struct {
	// Domain makes UIDs globally unique: an event's UID is
	// "<Kind>.<ID>@<Domain>", or "<ID>@<Domain>" when Kind is empty. It is
	// required and must stay constant for UIDs to stay stable.
	Domain string
	// ProdID is the calendar PRODID; DefaultProdID when empty.
	ProdID string
}

// Write encodes f as an iCalendar object to w.
//
// All-day events are written as DTSTART;VALUE=DATE and an exclusive
// DTEND;VALUE=DATE, the day after the inclusive Event.EndDate. Timed events
// are written in UTC (a trailing Z), never with a TZID, so no VTIMEZONE is
// needed; a point event (zero End, or End not after Start) has no DTEND.
// Titles become SUMMARY with RFC 5545 text escaping, and every line is folded
// at 75 octets without splitting a UTF-8 sequence.
//
// A Feed with no events encodes as a VCALENDAR with no components: RFC 5545
// strictly requires one, but clients accept it and an emptied feed must stay
// subscribable. Write returns an error, writing nothing, when Options.Domain
// is empty or an event has an empty ID or a zero start.
func Write(w io.Writer, f Feed, opts Options) error {
	if opts.Domain == "" {
		return errors.New("ics: empty Options.Domain")
	}
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropCalendarScale, "GREGORIAN")
	cal.Props.SetText(ical.PropProductID, cmp.Or(opts.ProdID, DefaultProdID))
	if f.Name != "" {
		p := ical.NewProp("X-WR-CALNAME")
		p.SetText(normalizeNewlines(f.Name))
		p.Params.Del(ical.ParamValue)
		cal.Props.Set(p)
	}

	stamp := f.Updated.UTC()
	if f.Updated.IsZero() {
		stamp = time.Unix(0, 0).UTC()
	}
	for _, e := range f.Events {
		ev, err := vevent(e, stamp, opts.Domain)
		if err != nil {
			return err
		}
		cal.Children = append(cal.Children, ev)
	}

	var raw bytes.Buffer
	if len(cal.Children) == 0 {
		writeEmpty(&raw, cal.Props)
	} else if err := ical.NewEncoder(&raw).Encode(cal); err != nil {
		return fmt.Errorf("ics: encode: %w", err)
	}
	var folded bytes.Buffer
	fold(&folded, raw.Bytes())
	_, err := w.Write(folded.Bytes())
	return err
}

// vevent builds the VEVENT of e.
func vevent(e calendar.Event, stamp time.Time, domain string) (*ical.Component, error) {
	if e.ID == "" {
		return nil, fmt.Errorf("ics: event %q: empty ID", e.Title)
	}
	uid := e.ID + "@" + domain
	if e.Kind != "" {
		uid = e.Kind + "." + uid
	}
	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, uid)
	ev.Props.SetDateTime(ical.PropDateTimeStamp, stamp)
	ev.Props.SetText(ical.PropSummary, normalizeNewlines(e.Title))

	if e.AllDay {
		if e.StartDate.IsZero() {
			return nil, fmt.Errorf("ics: event %q: zero StartDate", e.ID)
		}
		last := e.EndDate
		if last.IsZero() || last.Before(e.StartDate) {
			last = e.StartDate
		}
		ev.Props.SetDate(ical.PropDateTimeStart, e.StartDate.In(time.UTC))
		ev.Props.SetDate(ical.PropDateTimeEnd, last.AddDays(1).In(time.UTC))
		return ev.Component, nil
	}

	if e.Start.IsZero() {
		return nil, fmt.Errorf("ics: event %q: zero Start", e.ID)
	}
	ev.Props.SetDateTime(ical.PropDateTimeStart, e.Start.UTC())
	if e.End.After(e.Start) {
		ev.Props.SetDateTime(ical.PropDateTimeEnd, e.End.UTC())
	}
	return ev.Component, nil
}

// writeEmpty writes a VCALENDAR holding props and no component, which the
// go-ical encoder refuses. props carry no parameters.
func writeEmpty(buf *bytes.Buffer, props ical.Props) {
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	buf.WriteString("BEGIN:" + ical.CompCalendar + "\r\n")
	for _, name := range names {
		for _, p := range props[name] {
			buf.WriteString(name + ":" + p.Value + "\r\n")
		}
	}
	buf.WriteString("END:" + ical.CompCalendar + "\r\n")
}

// fold rewrites the CRLF-terminated lines of src into dst, folding each line
// longer than 75 octets with CRLF and one space (RFC 5545 section 3.1).
func fold(dst *bytes.Buffer, src []byte) {
	for len(src) > 0 {
		line, rest, _ := bytes.Cut(src, []byte("\r\n"))
		src = rest
		n := 0
		for len(line) > 0 {
			_, size := utf8.DecodeRune(line)
			if n+size > maxLineOctets {
				dst.WriteString("\r\n ")
				n = 1
			}
			dst.Write(line[:size])
			n += size
			line = line[size:]
		}
		dst.WriteString("\r\n")
	}
}

// normalizeNewlines turns CRLF and lone CR into LF: go-ical escapes LF as \n
// but refuses a CR.
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}
