package icsfeed

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/calendar"
)

const (
	icsProdID   = "-//seasonfill//calendar//EN"
	icsCalName  = "Seasonfill — Release Calendar"
	dateLayout  = "20060102"         // DTSTART;VALUE=DATE all-day
	stampLayout = "20060102T150405Z" // DTSTAMP UTC
	foldLimit   = 75                 // RFC 5545 §3.1 — octets per physical line
)

// Render assembles an RFC 5545 iCalendar document from a calendar.Report.
// All events are all-day VEVENTs (DTSTART;VALUE=DATE). Lines use CRLF and
// are folded at 75 octets; TEXT values are escaped per §3.3.11. UIDs are
// deterministic so a client re-subscribing sees stable events (no dupes).
func Render(rep calendar.Report) string {
	var b strings.Builder
	writeLine(&b, "BEGIN:VCALENDAR")
	writeLine(&b, "VERSION:2.0")
	writeLine(&b, "PRODID:"+icsProdID)
	writeLine(&b, "CALSCALE:GREGORIAN")
	writeLine(&b, "METHOD:PUBLISH")
	writeLine(&b, "X-WR-CALNAME:"+escapeText(icsCalName))
	stamp := rep.GeneratedAt.UTC().Format(stampLayout)
	for _, day := range rep.Days {
		for _, e := range day.Events {
			writeEvent(&b, e, stamp)
		}
	}
	writeLine(&b, "END:VCALENDAR")
	return b.String()
}

func writeEvent(b *strings.Builder, e calendar.Event, stamp string) {
	start := e.AirDate.UTC()
	writeLine(b, "BEGIN:VEVENT")
	writeLine(b, "UID:"+eventUID(e))
	writeLine(b, "DTSTAMP:"+stamp)
	writeLine(b, "DTSTART;VALUE=DATE:"+start.Format(dateLayout))
	// RFC 5545 all-day DTEND is exclusive → next day.
	writeLine(b, "DTEND;VALUE=DATE:"+start.AddDate(0, 0, 1).Format(dateLayout))
	writeLine(b, "SUMMARY:"+escapeText(summary(e)))
	if desc := description(e); desc != "" {
		writeLine(b, "DESCRIPTION:"+escapeText(desc))
	}
	writeLine(b, "TRANSP:TRANSPARENT")
	writeLine(b, "END:VEVENT")
}

// eventUID is deterministic per (series, season, episode) so a client that
// re-fetches the feed updates the same event rather than duplicating it.
func eventUID(e calendar.Event) string {
	return fmt.Sprintf("series%d-s%de%d@seasonfill", int64(e.SeriesID), e.Season, e.Episode)
}

// summary is "<title> SxxEyy[ <milestone-marker>]".
func summary(e calendar.Event) string {
	return fmt.Sprintf("%s S%02dE%02d%s", e.Title, e.Season, e.Episode, milestoneMarker(e.Milestone))
}

// milestoneMarker returns a leading-space emoji tag, or "" when nil.
func milestoneMarker(m *string) string {
	if m == nil {
		return ""
	}
	switch *m {
	case "premiere":
		return " 🎬"
	case "finale":
		return " 🏁"
	case "return":
		return " 🔄"
	default:
		return ""
	}
}

// description surfaces the per-episode library status when known.
func description(e calendar.Event) string {
	if e.State == "" {
		return ""
	}
	return "Status: " + e.State
}

// writeLine folds the content line and terminates it with CRLF.
func writeLine(b *strings.Builder, line string) {
	b.WriteString(foldLine(line))
	b.WriteString("\r\n")
}

// foldLine inserts `CRLF SPACE` folds so no physical line exceeds 75
// octets (RFC 5545 §3.1). Folds land on octet boundaries but NEVER split a
// UTF-8 rune. The inserted leading space counts toward the next line's 75
// octets and is stripped by clients on unfold. No trailing CRLF (writeLine
// adds it).
func foldLine(s string) string {
	if len(s) <= foldLimit {
		return s
	}
	var out strings.Builder
	lineLen := 0
	for _, r := range s {
		rl := utf8.RuneLen(r)
		if lineLen+rl > foldLimit {
			out.WriteString("\r\n ")
			lineLen = 1 // the leading fold space already on the new line
		}
		out.WriteRune(r)
		lineLen += rl
	}
	return out.String()
}

// escapeText escapes TEXT per RFC 5545 §3.3.11: backslash first, then
// semicolon, comma, and newline. Bare CR is dropped.
func escapeText(s string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`;`, `\;`,
		`,`, `\,`,
		"\n", `\n`,
		"\r", "",
	).Replace(s)
}
