package enrichment

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
)

// NextRetry is the application-layer wrapper around
// domain/enrichment.NextAttemptAt. It documents the contract the
// worker uses: previousAttempts is the count of attempts BEFORE
// this failure; failureAt is the wall-clock of the failure being
// scheduled FROM. Returns (newAttemptCount, nextRetryWallClock).
//
// Story 211 ships this as a passthrough — the wrapper exists so
// 212 (person worker) + D-1 (OMDb) can share the convention
// without re-deriving "attempts++" semantics.
func NextRetry(previousAttempts int, failureAt time.Time) (int, time.Time) {
	attempts := previousAttempts + 1
	return attempts, enrichment.NextAttemptAt(attempts, failureAt)
}
