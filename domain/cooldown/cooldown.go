package cooldown

import (
	"fmt"
	"time"
)

type Scope string

const (
	ScopeSeries Scope = "series"
	ScopeGUID   Scope = "guid"
)

// Cooldown is a single active blacklist entry. Polymorphic by Scope per D-2.1.
type Cooldown struct {
	Scope     Scope
	Key       string
	ExpiresAt time.Time
	Reason    string
	CreatedAt time.Time
}

// SeriesKey encodes the (instance, series, season) tuple as the cooldown key.
func SeriesKey(instance string, seriesID, season int) string {
	return fmt.Sprintf("%s:%d:%d", instance, seriesID, season)
}

// GUIDKey returns the raw guid; cooldown is global across instances because
// guids are tracker-global.
func GUIDKey(guid string) string {
	return guid
}

// IsActive returns true if the cooldown has not yet expired at the given moment.
func (c Cooldown) IsActive(now time.Time) bool {
	return c.ExpiresAt.After(now)
}
