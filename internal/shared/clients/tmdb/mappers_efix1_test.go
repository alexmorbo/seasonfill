package tmdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// TestMapSeasonToEpisodes_ZeroSeasonID_BirthsNilFK — E-FIX-1. The enrichment
// path passes seasonID==0 (the "resolve in tx" sentinel); every episode MUST be
// born with a NULL season_id, NOT a &0 pointer — season_id=0 references no
// seasons row and trips episodes_season_id_fkey (SQLSTATE 23503). The slim
// refresh path threads a real minted id verbatim.
func TestMapSeasonToEpisodes_ZeroSeasonID_BirthsNilFK(t *testing.T) {
	t.Parallel()
	season := &SeasonResponse{
		SeasonNumber: 1,
		Episodes: []SeasonEpisode{
			{ID: 111, SeasonNumber: 1, EpisodeNumber: 1},
			{ID: 222, SeasonNumber: 7, EpisodeNumber: 3}, // mismatched bucket
		},
	}

	// Enrichment path: seasonID==0 sentinel → season_id MUST be NULL, never &0.
	eps := MapSeasonToEpisodes(season, domain.SeriesID(42), 0)
	require.Len(t, eps, 2)
	for _, e := range eps {
		assert.Nil(t, e.SeasonID, "seasonID==0 sentinel must birth a NULL FK, not &0")
	}

	// Slim path: a real minted id is threaded verbatim.
	eps = MapSeasonToEpisodes(season, domain.SeriesID(42), 1234)
	require.Len(t, eps, 2)
	for _, e := range eps {
		require.NotNil(t, e.SeasonID)
		assert.Equal(t, int64(1234), *e.SeasonID)
	}
}
