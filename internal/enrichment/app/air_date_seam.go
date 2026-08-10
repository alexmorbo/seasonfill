package enrichment

import (
	"context"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
)

// AirDateAnnouncerPort is the Ф4 N3 post-refresh seam: when a Changes-driven
// refresh shifts a hydrated series' next_air_date to a future value, the
// worker hands (seriesID, title, old, new) to this port, which owns the dedup
// + outbox enqueue. Production impl: *notification/app.AirDateAnnouncer
// (satisfied structurally — no enrichment→notification import). nil-OK.
type AirDateAnnouncerPort interface {
	MaybeAnnounce(ctx context.Context, seriesID int64, title string, oldNext, newNext *time.Time)
}

// maybeAnnounceAirDate mirrors maybeEnqueueOMDbOnIMDBGain: post-tx, once per
// Handle, comparing the Handle-start canon against the just-merged canon. The
// EnrichmentTMDBSyncedAt!=nil gate suppresses a first-hydration flood (a fresh
// library add otherwise fires one air_date.announced per new series); only an
// already-known series that SHIFTS its next_air_date announces.
func (w *SeriesWorker) maybeAnnounceAirDate(ctx context.Context, oldCanon, merged series.Canon, announced *bool, log *slog.Logger) {
	if announced == nil || *announced || w.deps.AirDateAnnouncer == nil {
		return
	}
	if oldCanon.EnrichmentTMDBSyncedAt == nil {
		return // first hydration — do not flood
	}
	*announced = true
	title := ""
	if merged.OriginalTitle != nil {
		title = *merged.OriginalTitle
	}
	w.deps.AirDateAnnouncer.MaybeAnnounce(ctx, int64(merged.ID), title, oldCanon.NextAirDate, merged.NextAirDate)
	_ = log
}
