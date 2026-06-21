package persistence

import (
	"context"
	"time"

	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
)

// SyncLogStub satisfies the legacy appenrich.SyncLogRepo port with
// panic bodies. Wired into BuildEnrichment + BuildSeriesDetail during
// 464a so the app/series_worker.go + omdb_worker.go + person_worker.go
// + composer code paths compile against the OLD interface symbols
// without writing to a sync_log table that no longer exists in D-1.
//
// Per ADR D2-revised-roadmap.md Decision 1, the 464a binary is
// "boot-survives, request-time-panics". Boot does NOT invoke any of
// these methods; the dispatcher / scheduler ticks panic only when a
// worker actually fires. 464b finishes the cutover by:
//   - rewriting series_worker.go / omdb_worker.go / person_worker.go to
//     depend on EnrichmentErrorsRepository + canon enrichment_*_synced_at
//     columns directly;
//   - rewriting composer.go's computeDegraded to use the new SyncedAt
//     / Errors maps in DegradedInput;
//   - rewriting cold_start.go to call ListMissingTMDBSync via the new
//     ColdStartScanner port method;
//   - deleting this stub file along with the SyncLogRepo / SyncLogPort
//     interfaces in app/ports.go + seriesdetail/app/ports.go.
type SyncLogStub struct{}

// NewSyncLogStub returns a stub satisfying appenrich.SyncLogRepo.
func NewSyncLogStub() *SyncLogStub { return &SyncLogStub{} }

// Upsert panics — sync_log writes are retired in D-3.
func (*SyncLogStub) Upsert(context.Context, enrichment.SyncLog) error {
	panic("pending D-3 step 464b: sync_log retired — worker rewrite not yet shipped")
}

// GetLastSync panics — sync_log reads are retired in D-3.
func (*SyncLogStub) GetLastSync(context.Context, enrichment.EntityType, int64, enrichment.Source) (enrichment.SyncLog, error) {
	panic("pending D-3 step 464b: sync_log retired — worker rewrite not yet shipped")
}

// StaleScan panics — TTL gating is now driven by the canon
// enrichment_*_synced_at columns + 464b's series-side staleness scan.
func (*SyncLogStub) StaleScan(context.Context, enrichment.Source, time.Time, int) ([]enrichment.SyncLog, error) {
	panic("pending D-3 step 464b: sync_log retired — worker rewrite not yet shipped")
}

// RetryDue panics — retry dispatch now reads enrichment_errors via
// EnrichmentErrorsRepository.ListDueForRetry.
func (*SyncLogStub) RetryDue(context.Context, enrichment.Source, time.Time, int) ([]enrichment.SyncLog, error) {
	panic("pending D-3 step 464b: sync_log retired — worker rewrite not yet shipped")
}

// Compile-time guard against the appenrich.SyncLogRepo interface contract.
var _ appenrich.SyncLogRepo = (*SyncLogStub)(nil)
