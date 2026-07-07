package seriesdetail

import (
	"context"
	"time"

	seriesdomain "github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ETagFreshnessAdapter resolves per-section enrichment freshness stamps for
// the edge ETag middleware. It reuses the existing repository reads — no new
// SQL — mirroring EnrichmentFreshnessAdapter but keyed by HTTP section token
// instead of enrichment.Source.
//
// Section tokens are bare strings shared by contract with
// internal/shared/http/edge (edge cannot be imported here — it would form an
// edge -> seriesdetailrest -> seriesdetail import cycle). The middleware owns
// the canonical constants; this adapter matches their string values.
type ETagFreshnessAdapter struct {
	series  *enrichpersistence.SeriesRepository
	seasons *enrichpersistence.SeasonsRepository
}

// NewETagFreshnessAdapter constructs the adapter. Both repos are required.
func NewETagFreshnessAdapter(series *enrichpersistence.SeriesRepository, seasons *enrichpersistence.SeasonsRepository) *ETagFreshnessAdapter {
	return &ETagFreshnessAdapter{series: series, seasons: seasons}
}

// SectionSyncedAt returns the last-write timestamp for the requested section.
//
//   - "season": seasons.episodes_synced_at for (seriesID, seasonNumber) via
//     the existing narrow single-column read. Returns ports.ErrNotFound when
//     the season row is absent — the middleware treats any error as fail-open.
//   - "skeleton": series.skeleton_synced_at; "overview": series.enrichment_text_synced_at.
//   - "cast": series.enrichment_cast_synced_at.
//   - "recs": series.enrichment_recs_synced_at.
//
// A nil *time.Time (NULL stamp) is a valid "never synced" result and is
// returned without error; the middleware skips the ETag for it.
func (a *ETagFreshnessAdapter) SectionSyncedAt(ctx context.Context, seriesID domain.SeriesID, section string, seasonNumber int) (*time.Time, error) {
	if section == "season" {
		return a.seasons.GetEpisodesSyncedAt(ctx, seriesID, seasonNumber)
	}

	canon, err := a.series.Get(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	return sectionStamp(canon, section), nil
}

// sectionStamp picks the canon column for a series-level section. Unknown
// tokens return nil (fail-open — no ETag).
func sectionStamp(canon seriesdomain.Canon, section string) *time.Time {
	switch section {
	case "skeleton":
		// W18-16: the skeleton route's cache validator MUST key on the skeleton's
		// OWN freshness clock. Keying on enrichment_text_synced_at (overview) 304'd
		// stale skeletons after a skeleton-only canon change; and a cold row's nil
		// stamp fails the ETag open so the pollWhileDegraded loop still sees fresh
		// bodies.
		return canon.SkeletonSyncedAt
	case "overview":
		return canon.EnrichmentTextSyncedAt
	case "cast":
		return canon.EnrichmentCastSyncedAt
	case "recs":
		return canon.EnrichmentRecsSyncedAt
	default:
		return nil
	}
}
