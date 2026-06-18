// Package regrab is the Phase 10 Watchdog domain. Per parent story 039
// D-T1 the Go package is named `regrab` (not `watchdog`) to avoid
// collision with the existing infrastructure/watchdog/ package (D24
// instance-health recheck loop).
package regrab

import (
	"errors"
	"fmt"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Reason identifies why a (instance, series, season) triple landed on
// the Watchdog blacklist. Closed enum; new values require schema review.
type Reason string

const (
	// ReasonConsecutiveNoBetter — N consecutive evaluate attempts
	// returned "nothing better" (default N=3, configurable per instance).
	ReasonConsecutiveNoBetter Reason = "consecutive_no_better"

	// ReasonQbitUnreachablePersistent — 10 consecutive qBit poll failures
	// on the parent instance. Auto-disables the instance per D63.
	ReasonQbitUnreachablePersistent Reason = "qbit_unreachable_persistent"
)

// IsValid reports whether r is a recognised Reason constant.
func (r Reason) IsValid() bool {
	switch r {
	case ReasonConsecutiveNoBetter, ReasonQbitUnreachablePersistent:
		return true
	default:
		return false
	}
}

// Validation sentinels — caller wraps with %w when adding context.
var (
	ErrInvalidInstance    = errors.New("regrab: instance_id must be positive")
	ErrInvalidSeries      = errors.New("regrab: series_id must be positive")
	ErrInvalidSeason      = errors.New("regrab: season_number must be non-negative")
	ErrInvalidReason      = errors.New("regrab: unknown reason")
	ErrInvalidConsecutive = errors.New("regrab: consecutive must be positive")
)

// BlacklistEntry is the persisted shape of one watchdog_blacklist row.
// ExpiresAt is *time.Time because v1 always writes NULL (manual unblock
// only); the column is in place as a schema hook for a future
// auto-unblock policy.
type BlacklistEntry struct {
	ID           uint
	InstanceID   uint
	SeriesID     domain.SonarrSeriesID
	SeasonNumber int
	Reason       Reason
	Consecutive  int
	CreatedAt    time.Time
	ExpiresAt    *time.Time
}

// NewBlacklistEntry constructs a validated entry with CreatedAt = now
// and ExpiresAt = nil (manual unblock per v1). Returns a typed
// validation error on any invalid input — the caller wraps with %w
// when surfacing in a higher layer.
func NewBlacklistEntry(instanceID uint, seriesID domain.SonarrSeriesID, season, consecutive int, reason Reason, now time.Time) (BlacklistEntry, error) {
	if instanceID == 0 {
		return BlacklistEntry{}, fmt.Errorf("%w: got %d", ErrInvalidInstance, instanceID)
	}
	if seriesID <= 0 {
		return BlacklistEntry{}, fmt.Errorf("%w: got %d", ErrInvalidSeries, seriesID)
	}
	if season < 0 {
		return BlacklistEntry{}, fmt.Errorf("%w: got %d", ErrInvalidSeason, season)
	}
	if !reason.IsValid() {
		return BlacklistEntry{}, fmt.Errorf("%w: %q", ErrInvalidReason, string(reason))
	}
	if consecutive <= 0 {
		return BlacklistEntry{}, fmt.Errorf("%w: got %d", ErrInvalidConsecutive, consecutive)
	}
	return BlacklistEntry{
		InstanceID:   instanceID,
		SeriesID:     seriesID,
		SeasonNumber: season,
		Reason:       reason,
		Consecutive:  consecutive,
		CreatedAt:    now.UTC(),
		ExpiresAt:    nil,
	}, nil
}
