package app

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieETagFreshnessAdapter resolves per-section movie enrichment freshness
// stamps for the shared edge ETag middleware. It is the movie analog of
// seriesdetail.ETagFreshnessAdapter: given the numeric URL id (a TMDB id, from a
// /movies/:tmdb_id route) it resolves the canon row via GetByTMDBID and returns
// that section's movies.enrichment_*_synced_at stamp.
//
// Section tokens are bare strings shared by contract with
// internal/shared/http/edge (edge cannot be imported here — moviedetail/app is
// pulled in by edge via moviedetailrest, so the reverse edge would cycle). The
// middleware owns the canonical section constants; this adapter matches their
// string values.
type MovieETagFreshnessAdapter struct {
	canon CanonReader
}

// NewMovieETagFreshnessAdapter constructs the adapter over the shared movie
// CanonReader (GetByTMDBID). The reader is required.
func NewMovieETagFreshnessAdapter(canon CanonReader) *MovieETagFreshnessAdapter {
	return &MovieETagFreshnessAdapter{canon: canon}
}

// SectionSyncedAt resolves id (a TMDB id) → canon → the section stamp.
// seasonNumber is ignored (movies have no seasons). A GetByTMDBID error
// (including ports.ErrNotFound for an absent movie) bubbles up and the middleware
// fails open (no ETag). A nil *time.Time (section never enriched) is a valid
// "never synced" result returned without error.
func (a *MovieETagFreshnessAdapter) SectionSyncedAt(ctx context.Context, id int, section string, _ int) (*time.Time, error) {
	canon, err := a.canon.GetByTMDBID(ctx, domain.TMDBID(id))
	if err != nil {
		return nil, err
	}
	return movieSectionStamp(canon, section), nil
}

// movieSectionStamp maps an HTTP section token to the canon column feeding the
// Ф2.1+ movie sub-endpoints. Unknown / not-applicable tokens (skeleton, season,
// media, keywords) return nil → the middleware skips the ETag (fail-open).
func movieSectionStamp(canon movie.Canon, section string) *time.Time {
	switch section {
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
