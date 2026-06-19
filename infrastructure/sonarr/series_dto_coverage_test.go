package sonarr

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Directly exercises seriesDTOToCacheEntry's optional-field branches.

func TestSeriesDTOToCacheEntry_AllOptionalFieldsPopulated(t *testing.T) {
	t.Parallel()
	prev := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	d := seriesDTO{
		ID:        7,
		Title:     "Breaking Bad",
		TitleSlug: "breaking-bad",
		Monitored: true,
		Year:      2008,
		TVDBID:    81189,
		IMDBID:    "tt0903747",
		TMDBID:    1396,
		Status:    "ended",
		Runtime:   47,
		Overview:  "A chemistry teacher.",
		Genres:    []string{"Crime", "Drama"},
		Statistics: &statisticsDTO{
			EpisodeCount:     62,
			EpisodeFileCount: 60,
		},
		PreviousAiring: &prev,
		Images: []imageDTO{
			{CoverType: "poster", URL: "/p1.jpg"},
			{CoverType: "fanart", URL: "/f1.jpg"},
			{CoverType: "banner", URL: "/b1.jpg"},
			{CoverType: "poster", URL: "/p2.jpg"}, // duplicate — first wins
			{CoverType: "unknown", URL: "/u.jpg"}, // ignored
		},
	}
	e := seriesDTOToCacheEntry(d, "alpha")
	assert.Equal(t, domain.InstanceName("alpha"), e.InstanceName)
	assert.Equal(t, domain.SonarrSeriesID(7), e.SonarrSeriesID)
	assert.Equal(t, "Breaking Bad", e.Title)
	assert.Equal(t, "breaking-bad", e.TitleSlug)
	assert.True(t, e.Monitored)
	require.NotNil(t, e.Year)
	assert.Equal(t, 2008, *e.Year)
	require.NotNil(t, e.TVDBID)
	assert.Equal(t, domain.TVDBID(81189), *e.TVDBID)
	require.NotNil(t, e.IMDBID)
	assert.Equal(t, domain.IMDBID("tt0903747"), *e.IMDBID)
	require.NotNil(t, e.TMDBID)
	assert.Equal(t, 1396, *e.TMDBID)
	require.NotNil(t, e.Status)
	assert.Equal(t, "ended", *e.Status)
	require.NotNil(t, e.RuntimeMinutes)
	assert.Equal(t, 47, *e.RuntimeMinutes)
	require.NotNil(t, e.Overview)
	assert.Equal(t, "A chemistry teacher.", *e.Overview)
	assert.Equal(t, []string{"Crime", "Drama"}, e.Genres)
	require.NotNil(t, e.LastAiredAt)
	assert.Equal(t, prev, *e.LastAiredAt)
	// First-wins for fanart/banner. Story 350 removed poster from the
	// CacheEntry — the FE consumes posters via media_assets/poster_hash.
	require.NotNil(t, e.FanartPath)
	assert.Equal(t, "/f1.jpg", *e.FanartPath)
	require.NotNil(t, e.BannerPath)
	assert.Equal(t, "/b1.jpg", *e.BannerPath)
	// MissingCount derived from stats: 62 aired - 60 on disk = 2.
	assert.Equal(t, 2, e.MissingCount)
}

func TestSeriesDTOToCacheEntry_EmptyOptionalFieldsRemainNil(t *testing.T) {
	t.Parallel()
	d := seriesDTO{
		ID:    3,
		Title: "T",
		// all optional fields zero/empty.
	}
	e := seriesDTOToCacheEntry(d, "beta")
	assert.Equal(t, domain.InstanceName("beta"), e.InstanceName)
	assert.Equal(t, domain.SonarrSeriesID(3), e.SonarrSeriesID)
	assert.Nil(t, e.Year)
	assert.Nil(t, e.TVDBID)
	assert.Nil(t, e.IMDBID)
	assert.Nil(t, e.TMDBID)
	assert.Nil(t, e.Status)
	assert.Nil(t, e.RuntimeMinutes)
	assert.Nil(t, e.Overview)
	assert.Nil(t, e.Genres)
	assert.Nil(t, e.FanartPath)
	assert.Nil(t, e.BannerPath)
	assert.Nil(t, e.LastAiredAt)
}

func TestSeriesDTOToCacheEntry_EmptyImageURLSkipped(t *testing.T) {
	t.Parallel()
	d := seriesDTO{
		ID:    1,
		Title: "T",
		Images: []imageDTO{
			{CoverType: "fanart", URL: ""},          // skipped
			{CoverType: "fanart", URL: "/real.jpg"}, // takes the slot
		},
	}
	e := seriesDTOToCacheEntry(d, "alpha")
	require.NotNil(t, e.FanartPath)
	assert.Equal(t, "/real.jpg", *e.FanartPath)
}

func TestSeriesDTOToCacheEntry_NoStatisticsLeavesMissingCountZero(t *testing.T) {
	t.Parallel()
	d := seriesDTO{ID: 1, Title: "T"}
	e := seriesDTOToCacheEntry(d, "alpha")
	assert.Equal(t, 0, e.MissingCount)
}
