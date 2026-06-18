package cooldown

import (
	"fmt"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type Scope string

const (
	ScopeSeries      Scope = "series"
	ScopeGUID        Scope = "guid"
	ScopeRegrabRetry Scope = "regrab_retry"
)

// ReasonMaxBytes is the application-side cap on Cooldown.Reason
// strings. The DB column is `text` after migration 23 (story 118),
// so the cap is purely operator-facing: a cooldown row's reason
// should be one short sentence that explains "why is this on hold",
// not a stack trace. Stack traces live in decisions.error_detail and
// grab_records.error_message (both `text`, both clamped at 4 KiB by
// errtext.Clamp). 512 bytes comfortably holds a sentence plus an
// upstream tracker URL fragment.
//
// Byte-counted (not rune-counted) because the underlying column
// stores bytes and JSON encodes bytes. Multi-byte UTF-8 sequences may
// be cut mid-codepoint at the boundary; the consumer is a single
// list row and browsers tolerate the resulting U+FFFD replacement
// glyph. Matches errtext.Clamp's semantics.
const ReasonMaxBytes = 512

// Cooldown is a single active blacklist entry. Polymorphic by Scope per D-2.1.
type Cooldown struct {
	Scope     Scope
	Key       string
	ExpiresAt time.Time
	Reason    string
	CreatedAt time.Time
}

// SeriesKey encodes the (instance, series, season) tuple as the cooldown key.
func SeriesKey(instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int) string {
	return fmt.Sprintf("%s:%d:%d", instance, seriesID, season)
}

// GUIDKey returns the raw guid; cooldown is global across instances because
// guids are tracker-global.
func GUIDKey(guid string) string {
	return guid
}

// ClampReason returns reason unchanged if len(reason) <= ReasonMaxBytes;
// otherwise returns the first ReasonMaxBytes bytes with a
// "…(truncated N bytes)" suffix appended (N = the number of bytes
// dropped). The suffix is appended AFTER the cut, so the returned
// string length is ReasonMaxBytes + len(suffix) — fine for a `text`
// column, documented here so callers do not assume a hard byte budget.
//
// Mirrors errtext.Clamp's signature and semantics so call sites stay
// uniform; kept separate (and with a smaller cap) because the reason
// field has a different operator-facing role from grab/decision error
// detail.
func ClampReason(reason string) string {
	if len(reason) <= ReasonMaxBytes {
		return reason
	}
	dropped := len(reason) - ReasonMaxBytes
	return reason[:ReasonMaxBytes] + fmt.Sprintf("…(truncated %d bytes)", dropped)
}

// IsActive returns true if the cooldown has not yet expired at the given moment.
func (c Cooldown) IsActive(now time.Time) bool {
	return c.ExpiresAt.After(now)
}
