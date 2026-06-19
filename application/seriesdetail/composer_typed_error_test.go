package seriesdetail

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// TestComposer_Get_404_NilSeriesID_PreservesTypedCacheNF verifies that
// when series_cache exists but its series_id pointer is nil/zero, the
// composer returns a SeriesCacheNotFoundError joined with
// ports.ErrNotFound so middleware can dispatch series_cache_not_found.
func TestComposer_Get_404_NilSeriesID_PreservesTypedCacheNF(t *testing.T) {
	deps, cache, _ := baseDeps(t)
	cache.entries[cacheKey("alpha", 2)] = series.CacheEntry{
		InstanceName: "alpha", SonarrSeriesID: 2, SeriesID: nil,
	}
	c := NewComposer(deps)
	_, err := c.Get(context.Background(), "alpha", 2, "en-US")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound),
		"legacy errors.Is(ports.ErrNotFound) must still hold")
	var typed *sharedErrors.SeriesCacheNotFoundError
	require.True(t, errors.As(err, &typed),
		"SeriesCacheNotFoundError chain must survive (F-2c-2)")
	assert.Equal(t, domain.InstanceName("alpha"), typed.InstanceName)
	assert.Equal(t, domain.SonarrSeriesID(2), typed.SonarrSeriesID)
}

// TestComposer_GetSeason_UnknownSeason_PreservesTypedSeasonNF verifies
// that requesting a season number that doesn't exist on the series
// returns a SeasonNotFoundError joined with ports.ErrNotFound so
// middleware can dispatch season_not_found instead of plain not_found.
func TestComposer_GetSeason_UnknownSeason_PreservesTypedSeasonNF(t *testing.T) {
	deps, _, _ := baseDeps(t)
	c := NewComposer(deps)
	_, err := c.GetSeason(context.Background(), "alpha", 1, 99, "en-US")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound),
		"legacy errors.Is(ports.ErrNotFound) must still hold")
	var typed *sharedErrors.SeasonNotFoundError
	require.True(t, errors.As(err, &typed),
		"SeasonNotFoundError chain must survive (F-2c-2)")
	assert.Equal(t, domain.InstanceName("alpha"), typed.InstanceName)
	assert.Equal(t, domain.SonarrSeriesID(1), typed.SonarrSeriesID)
	assert.Equal(t, 99, typed.SeasonNumber)
}
