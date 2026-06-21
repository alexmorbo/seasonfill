package enrichment

import "time"

// EnrichmentError is the value-type form of one enrichment_errors row
// (D-1 migration 000008, PRD §4.4). The repository handles upsert by
// natural key (EntityType, EntityID, Source). Workers construct an
// EnrichmentError on every failure attempt; ClearOnSuccess deletes the
// row once the source returns a successful payload.
//
// ID is the surrogate PK (autoincrement). FirstSeenAt is preserved
// across update attempts — only LastSeenAt + Attempts + NextAttemptAt +
// LastError change on the upsert path.
type EnrichmentError struct {
	ID            int64
	EntityType    EntityType
	EntityID      int64
	Source        Source
	LastError     string
	Attempts      int
	FirstSeenAt   time.Time
	LastSeenAt    time.Time
	NextAttemptAt *time.Time
}

// IsLive reports whether the error happened recently enough to count
// as a "live" degraded source. PRD §5.6 surface rule: an error rolls
// off the degraded[] list once LastSeenAt is older than `window` (the
// composer passes 48h matching the rolling-stale window for sync
// freshness).
func (e EnrichmentError) IsLive(window time.Duration, now time.Time) bool {
	return e.LastSeenAt.After(now.Add(-window))
}
