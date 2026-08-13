package enrichment

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// TestSeriesWorker_UnresolvableSeasonNumber_NoZeroSeasonID — E-FIX-1.
// A fetched season detail contains an episode whose per-episode season_number
// is NOT among tv.Seasons (mismatched bucket). Pre-fix that episode reached
// BatchUpsert with season_id=&0 (→ 23503 against episodes_season_id_fkey).
// Post-fix it lands with season_id=NULL; the in-bucket episode gets the real id.
func TestSeriesWorker_UnresolvableSeasonNumber_NoZeroSeasonID(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(15871)
	// tv.Seasons lists ONLY season 1.
	tv := &tmdb.TVResponse{
		ID:      15871,
		Name:    "Shark Week",
		Seasons: []tmdb.TVSeasonStub{{ID: 1, SeasonNumber: 1, EpisodeCount: 2}},
	}
	// Season-1 detail carries a season_1 episode AND a stray season_7 episode.
	seasonResp := map[int]*tmdb.SeasonResponse{
		1: {
			SeasonNumber: 1,
			Episodes: []tmdb.SeasonEpisode{
				{ID: 900001, SeasonNumber: 1, EpisodeNumber: 1},
				{ID: 900007, SeasonNumber: 7, EpisodeNumber: 1}, // unresolvable
			},
		},
	}
	f := newWorkerFixture(t, tv, seasonResp)
	f.seedCanon(1, &tmdbID)

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	require.NotEmpty(t, f.episodes.rows, "episodes must persist (tx must commit, not roll back)")
	var sawSeason1, sawSeason7 bool
	for _, e := range f.episodes.rows {
		// The invariant: NEVER a non-NULL zero season_id.
		if e.SeasonID != nil {
			assert.NotEqual(t, int64(0), *e.SeasonID, "no episode may carry season_id=0")
		}
		switch e.SeasonNumber {
		case 1:
			sawSeason1 = true
			require.NotNil(t, e.SeasonID, "in-bucket episode resolves to the real season id")
			assert.Greater(t, *e.SeasonID, int64(0))
		case 7:
			sawSeason7 = true
			assert.Nil(t, e.SeasonID, "unresolvable season_number → NULL season_id")
		}
	}
	assert.True(t, sawSeason1 && sawSeason7, "both episodes must be persisted")
	// No failure journalled — the tx committed cleanly.
	assert.Empty(t, f.enrichmentErrors.failures, "clean commit records no enrichment error")
}

// TestSeriesWorker_ParksAfterMaxAttempts — E-FIX-1. A retryable failure at the
// MaxRetryAttempts boundary is PARKED: recorded with NextAttemptAt=nil (terminal)
// so ListDueForRetry skips it. Below the cap it still schedules a retry.
func TestSeriesWorker_ParksAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)

	t.Run("at cap → parked (next_attempt_at nil)", func(t *testing.T) {
		t.Parallel()
		f := newWorkerFixture(t, nil, nil)
		f.tmdb.tvErr = errors.New("persistent kaboom") // retryable, non-404
		f.seedCanon(1, &tmdbID)
		// prevAttempts = cap-1 → this failure makes attempts == cap.
		f.enrichmentErrors.preexist = &enrichment.EnrichmentError{
			EntityType: enrichment.EntityTypeSeries,
			EntityID:   1,
			Source:     enrichment.SourceTMDBSeries,
			Attempts:   enrichment.MaxRetryAttempts - 1,
		}
		require.NoError(t, f.worker.Handle(context.Background(), 1))
		last := f.enrichmentErrors.lastFailure()
		assert.Equal(t, enrichment.MaxRetryAttempts, last.Attempts)
		assert.Nil(t, last.NextAttemptAt, "parked row is terminal — ListDueForRetry must skip it")
	})

	t.Run("below cap → still scheduled", func(t *testing.T) {
		t.Parallel()
		f := newWorkerFixture(t, nil, nil)
		f.tmdb.tvErr = errors.New("transient kaboom")
		f.seedCanon(1, &tmdbID)
		f.enrichmentErrors.preexist = &enrichment.EnrichmentError{
			EntityType: enrichment.EntityTypeSeries,
			EntityID:   1,
			Source:     enrichment.SourceTMDBSeries,
			Attempts:   enrichment.MaxRetryAttempts - 2,
		}
		require.NoError(t, f.worker.Handle(context.Background(), 1))
		last := f.enrichmentErrors.lastFailure()
		assert.Equal(t, enrichment.MaxRetryAttempts-1, last.Attempts)
		require.NotNil(t, last.NextAttemptAt, "below cap still schedules a retry")
	})
}
