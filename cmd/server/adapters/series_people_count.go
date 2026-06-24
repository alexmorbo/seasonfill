package adapters

import (
	"context"
	"fmt"

	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesPeopleCountAdapter satisfies adapters.CountByID for the
// "series has at least one person_credit row" emptiness probe used by
// the Story 533 staleness probe.
//
// The probe is permissive: a canon row with no TMDB id (Sonarr-orphan)
// OR a person_credits read error both surface as count=0 with nil err.
// The probe interprets `n == 0 && err == nil` as `empty_people` →
// stale=true, which on a Sonarr-orphan would force the freshener to
// call SeriesWorker.Handle — and the worker's "no tmdb_id" short-circuit
// (series_worker.go:124) is a silent no-op. Net effect: harmless extra
// probe + bounded by 7d TTL upper bound from the canon TTL check above.
type SeriesPeopleCountAdapter struct {
	pc     *enrichpersistence.PersonCreditsRepository
	series SeriesReader
}

// NewSeriesPeopleCountAdapter wires the adapter.
func NewSeriesPeopleCountAdapter(
	pc *enrichpersistence.PersonCreditsRepository,
	series SeriesReader,
) *SeriesPeopleCountAdapter {
	return &SeriesPeopleCountAdapter{pc: pc, series: series}
}

// CountBySeries returns the person_credits row count for the series's
// TMDB id (media_type='tv'). On canon load error OR missing TMDB id
// (Sonarr-orphan) returns 0 + the original error wrapped so the probe
// stays permissive — the probe interprets nil err + n=0 as empty_people
// but discards non-nil err (treats as "unknown, fall through to fresh").
func (a *SeriesPeopleCountAdapter) CountBySeries(ctx context.Context, seriesID domain.SeriesID) (int, error) {
	canon, err := a.series.Get(ctx, seriesID)
	if err != nil {
		// Surface the error so the probe's permissive branch treats it
		// as "unknown" (probe discards non-nil err — does NOT mark stale).
		return 0, fmt.Errorf("series_people count: canon lookup: %w", err)
	}
	if canon.TMDBID == nil {
		// Sonarr-orphan — no TMDB id means no person_credits. NOT an error.
		return 0, nil
	}
	rows, err := a.pc.ListByMedia(ctx, tmdb.MediaTypeTV, int(*canon.TMDBID))
	if err != nil {
		return 0, fmt.Errorf("series_people count: list_by_media: %w", err)
	}
	return len(rows), nil
}
