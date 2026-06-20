package regrab

import (
	"errors"
	"fmt"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ErrInvalidCount is the sentinel for a non-positive consecutive-count
// argument; specific to the counter API to keep validation errors
// distinguishable from the BlacklistEntry sentinels.
var ErrInvalidCount = errors.New("regrab: consecutive count must be non-negative")

// NoBetterCounter tracks consecutive evaluate("nothing better") outcomes
// per (instance, series, season). Separate from BlacklistEntry: the
// counter is *live* state — its Consecutive can be 0..N, where N is the
// per-instance threshold. When Consecutive reaches the threshold, the
// regrab use case copies the triple into watchdog_blacklist and calls
// Reset() here. Subsequent re-grab attempts on the same triple are
// short-circuited at the blacklist gate before they ever increment
// this counter again.
type NoBetterCounter struct {
	ID           uint
	InstanceID   uint
	SeriesID     domain.SonarrSeriesID
	SeasonNumber int
	Consecutive  int
	LastSeenAt   time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NewNoBetterCounter constructs a zero-counter entry with CreatedAt =
// UpdatedAt = LastSeenAt = now. The regrab loop calls this on first
// detection of a "nothing better" outcome for a triple it hasn't seen
// before; subsequent detections call Increment on the persisted row.
func NewNoBetterCounter(instanceID uint, seriesID domain.SonarrSeriesID, season int, now time.Time) (NoBetterCounter, error) {
	if instanceID == 0 {
		return NoBetterCounter{}, fmt.Errorf("%w: got %d", ErrInvalidInstance, instanceID)
	}
	if seriesID <= 0 {
		return NoBetterCounter{}, fmt.Errorf("%w: got %d", ErrInvalidSeries, seriesID)
	}
	if season < 0 {
		return NoBetterCounter{}, fmt.Errorf("%w: got %d", ErrInvalidSeason, season)
	}
	utc := now.UTC()
	return NoBetterCounter{
		InstanceID:   instanceID,
		SeriesID:     seriesID,
		SeasonNumber: season,
		Consecutive:  0,
		LastSeenAt:   utc,
		CreatedAt:    utc,
		UpdatedAt:    utc,
	}, nil
}

// Increment returns a new NoBetterCounter with Consecutive bumped by 1
// and UpdatedAt = LastSeenAt = now. Pure: never mutates the receiver.
func (c NoBetterCounter) Increment(now time.Time) NoBetterCounter {
	utc := now.UTC()
	out := c
	out.Consecutive++
	out.LastSeenAt = utc
	out.UpdatedAt = utc
	return out
}

// Reset returns a copy with Consecutive zeroed and UpdatedAt = now.
// LastSeenAt is preserved so debug queries can still answer "when did
// this triple last produce a no-better detection cycle?".
func (c NoBetterCounter) Reset(now time.Time) NoBetterCounter {
	out := c
	out.Consecutive = 0
	out.UpdatedAt = now.UTC()
	return out
}

// HasReachedThreshold reports whether Consecutive >= threshold. The
// regrab use case calls this with the per-instance
// max_consecutive_no_better setting; on true it transitions the
// triple into the blacklist table and calls Reset.
func (c NoBetterCounter) HasReachedThreshold(threshold int) bool {
	if threshold <= 0 {
		return false
	}
	return c.Consecutive >= threshold
}
