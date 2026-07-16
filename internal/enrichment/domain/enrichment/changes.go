package enrichment

import "time"

// changesMaxWindowDays is TMDB's hard cap on GET /tv/changes: the query accepts
// a span of at most 14 INCLUSIVE calendar days (plan §2.1). A single Window
// therefore spans at most 13 days of date-difference — [d, d+13] is 14 inclusive
// days. maxLookbackDays (a PlanWindows param) governs only the stale-cursor reset
// threshold; this const governs chunk size. They coincide in production (both 14)
// but are kept separate so lookback could widen without exceeding the API window.
const changesMaxWindowDays = 14

// Window is one closed [Start, End] calendar-day span for a /tv/changes query.
// Both bounds are UTC-midnight dates and INCLUSIVE — the tmdb client formats them
// as start_date / end_date (YYYY-MM-DD) and TMDB treats both endpoints as whole
// calendar days. Chronological order (oldest first) is guaranteed by PlanWindows.
type Window struct {
	Start time.Time
	End   time.Time
}

// ChangeCursor is the persisted progress of the firehose walk (single row in
// tmdb_changes_state, plan §5.2). Zero LastWindowEnd = empty cursor (first run).
//
// LastWindowEnd is the UTC-midnight date processed INCLUSIVELY so far. It is a
// plain exported field on purpose: the poller (W2-4) reads it BEFORE advancing
// the cursor to obtain dedupBoundary (= prevLastWindowEnd) for MarkChangedByTMDBIDs
// (plan §0-G7 / ADR-0004). Do NOT overlap-shift this value when using it as the
// dedup boundary.
//
// SchemaVersion / LastMatched / LastFirehose are diagnostics carried for lossless
// round-trip through the cursor store (they are NOT read by PlanWindows). The
// poller writes LastMatched / LastFirehose after each poll; SchemaVersion is the
// forward-compat hook for a future per-key dispatcher (plan §3.5).
type ChangeCursor struct {
	SchemaVersion int
	LastWindowEnd time.Time
	LastPollAt    time.Time
	LastMatched   int
	LastFirehose  int
}

// utcMidnight truncates t to 00:00:00 of its UTC calendar day.
func utcMidnight(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// CursorGapDays is the SINGLE authority for the whole-calendar-day gap between a
// cursor's LastWindowEnd and now, both truncated to their UTC-midnight day. Both
// PlanWindows (the stale-cursor reset threshold) and the poller's reset predicate
// compute the gap through THIS helper so the two can never diverge (W2-FIX L1).
// Result is now-day minus lastWindowEnd-day, in days; negative when lastWindowEnd
// is in the future.
func CursorGapDays(lastWindowEnd, now time.Time) int {
	return int(utcMidnight(now).Sub(utcMidnight(lastWindowEnd)).Hours() / 24)
}

// PlanWindows splits [cursor.LastWindowEnd - overlapDays … today] into chronological
// windows of at most changesMaxWindowDays inclusive days each (plan §4.3). Pure
// function — 100% unit-testable.
//
// Reset to a single cold-start window [today-1 … today] when:
//   - cursor is empty (zero LastWindowEnd), OR
//   - cursor is in the future (now < LastWindowEnd — clock skew / corruption), OR
//   - cursor is stale (today - LastWindowEnd > maxLookbackDays days).
//
// Backlog past the lookback floor is deliberately skipped — the TTL sweep (7/14/30d)
// is the catch-up net (plan §4.6).
//
// Otherwise start = utcMidnight(LastWindowEnd) - overlapDays (the taxonomy-safe
// overlap, plan §8), end = today, chunked oldest-first. Both window bounds are
// inclusive UTC-midnight dates.
func PlanWindows(cursor ChangeCursor, now time.Time, overlapDays, maxLookbackDays int) []Window {
	today := utcMidnight(now)
	coldStart := []Window{{Start: today.AddDate(0, 0, -1), End: today}}

	if cursor.LastWindowEnd.IsZero() {
		return coldStart
	}
	if now.Before(cursor.LastWindowEnd) {
		return coldStart
	}
	lweDay := utcMidnight(cursor.LastWindowEnd)
	gapDays := CursorGapDays(cursor.LastWindowEnd, now)
	if gapDays > maxLookbackDays {
		return coldStart
	}

	start := lweDay.AddDate(0, 0, -overlapDays)
	end := today
	if !start.Before(end) {
		// overlap 0 with the cursor already at today (or start clamped to end):
		// a single same-day re-check window [today, today].
		return []Window{{Start: start, End: end}}
	}

	maxSpan := time.Duration(changesMaxWindowDays-1) * 24 * time.Hour // 13 days difference = 14 inclusive days
	var windows []Window
	for s := start; ; {
		e := s.Add(maxSpan)
		if !e.Before(end) {
			windows = append(windows, Window{Start: s, End: end})
			break
		}
		windows = append(windows, Window{Start: s, End: e})
		s = e // next chunk starts on the previous End (shared boundary day is idempotent)
	}
	return windows
}
