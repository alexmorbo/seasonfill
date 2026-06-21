package regrab

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// WatchdogState is the per-(instance, series, season) regrab tracking
// row from `watchdog_state`. Replaces legacy NoBetterCounter — the
// attempt counter is `AttemptCount`; cooldown_until + last_error are
// new D-1 columns (was implicit in loop scheduler + logs only).
type WatchdogState struct {
	InstanceName   domain.InstanceName
	SonarrSeriesID domain.SonarrSeriesID
	SeasonNumber   int
	AttemptCount   int
	LastAttemptAt  time.Time
	CooldownUntil  *time.Time
	LastError      *string
	UpdatedAt      time.Time
}

// HasReachedThreshold reports whether AttemptCount >= threshold. The
// regrab use case calls this with the per-instance
// max_consecutive_no_better setting; on true it transitions the
// triple into the blacklist table and calls Reset.
func (s WatchdogState) HasReachedThreshold(threshold int) bool {
	if threshold <= 0 {
		return false
	}
	return s.AttemptCount >= threshold
}
