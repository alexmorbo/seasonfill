package icsfeed

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/calendar"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func finalePtr() *string { s := "finale"; return &s }

func TestRender_Golden(t *testing.T) {
	t.Parallel()
	rep := calendar.Report{
		GeneratedAt: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		Days: []calendar.Day{{
			Date: "2026-08-15",
			Events: []calendar.Event{{
				SeriesID:  domain.SeriesID(140),
				Title:     "Dune: Part, Two", // comma → must be escaped
				Season:    1,
				Episode:   2,
				AirDate:   time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
				State:     "upcoming",
				Milestone: finalePtr(),
				MediaType: "tv",
			}},
		}},
	}

	got := Render(rep)

	want := strings.Join([]string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//seasonfill//calendar//EN",
		"CALSCALE:GREGORIAN",
		"METHOD:PUBLISH",
		"X-WR-CALNAME:Seasonfill — Release Calendar",
		"BEGIN:VEVENT",
		"UID:series140-s1e2@seasonfill",
		"DTSTAMP:20260809T120000Z",
		"DTSTART;VALUE=DATE:20260815",
		"DTEND;VALUE=DATE:20260816",
		`SUMMARY:Dune: Part\, Two S01E02 🏁`,
		"DESCRIPTION:Status: upcoming",
		"TRANSP:TRANSPARENT",
		"END:VEVENT",
		"END:VCALENDAR",
		"", // trailing CRLF after END:VCALENDAR
	}, "\r\n")

	assert.Equal(t, want, got)
	// explicit CRLF assertions (guards against an editor normalizing \r\n → \n)
	assert.Contains(t, got, "\r\n")
	assert.NotContains(t, strings.ReplaceAll(got, "\r\n", ""), "\n")
}

// TestFoldLine_LongTitle asserts folding never emits a physical line longer
// than 75 octets and never splits a UTF-8 rune.
func TestFoldLine_LongTitle(t *testing.T) {
	t.Parallel()
	long := "SUMMARY:" + strings.Repeat("A", 120) + " бонус" // multibyte tail
	folded := foldLine(long)
	require.Contains(t, folded, "\r\n ", "long line must fold")
	for physical := range strings.SplitSeq(folded, "\r\n") {
		assert.LessOrEqual(t, len(physical), foldLimit, "physical line exceeds 75 octets: %q", physical)
	}
	// unfolding (strip CRLF+space) must restore the original content
	assert.Equal(t, long, strings.ReplaceAll(folded, "\r\n ", ""))
}

func TestEscapeText(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `a\\b\;c\,d\ne`, escapeText("a\\b;c,d\ne"))
}
