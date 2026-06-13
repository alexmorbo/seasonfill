// Package enrichment — Story 212 cold-start backfill.
//
// BackfillSeries scans `series` rows that lack a sync_log(tmdb_series)
// row and enqueues them at PriorityCold. Person backfill happens
// organically — every successful series enrichment upserts person
// stubs and enqueues them (series_worker integration).
//
// Idempotency: after the first pass every series acquires a sync_log
// row (outcome ∈ {ok, error, not_found}). The second pass's LEFT JOIN
// returns zero rows. The function is therefore safe to call from a
// recovery script or repeated restarts.

package enrichment

import (
	"context"
	"fmt"
	"log/slog"
)

// BackfillSeries enqueues every series_id returned by scanner at
// PriorityCold. The dispatcher's dedup protects against double-enqueue
// if the same id is already pending. Limit caps a single sweep at
// 5000 ids — enough to cover a typical library (~300) by an order
// of magnitude, bounded so a runaway query doesn't pin memory.
func BackfillSeries(ctx context.Context, scanner ColdStartScanner, dispatcher Dispatcher, log *slog.Logger) error {
	const sweepLimit = 5000
	if log == nil {
		log = slog.Default()
	}
	ids, err := scanner.ListMissingSyncLog(ctx, "tmdb_series", sweepLimit)
	if err != nil {
		return fmt.Errorf("cold-start scan: %w", err)
	}
	if len(ids) == 0 {
		log.InfoContext(ctx, "enrichment.cold_start.empty")
		return nil
	}
	for _, id := range ids {
		dispatcher.Enqueue(EntitySeries, id, PriorityCold)
	}
	log.InfoContext(ctx, "enrichment.cold_start.enqueued",
		slog.Int("series_count", len(ids)),
		slog.String("priority", "cold"),
	)
	return nil
}
