package seriesdetail

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

func monitorSeasonEntry() series.CacheEntry {
	return series.CacheEntry{InstanceName: "main", SonarrSeriesID: 122}
}

// newMonitorMock returns a SonarrClientMock whose GetSeries yields one series
// with a single season (number/monitored driven by args) and no-op write funcs.
func newMonitorMock(seasonNumber int, monitored bool) *ports.SonarrClientMock {
	return &ports.SonarrClientMock{
		GetSeriesFunc: func(_ context.Context, id domain.SonarrSeriesID) (series.Series, error) {
			return series.Series{
				ID:      id,
				Seasons: []series.Season{{Number: seasonNumber, Monitored: monitored}},
			}, nil
		},
		SetSeasonMonitoredFunc: func(_ context.Context, _ domain.SonarrSeriesID, _ int, _ bool) error {
			return nil
		},
		SearchSeasonFunc: func(_ context.Context, _ domain.SonarrSeriesID, _ int) error {
			return nil
		},
	}
}

func monitorSonarrFor(m ports.SonarrClient) func(domain.InstanceName) (SonarrSeasonMonitor, bool) {
	return func(_ domain.InstanceName) (SonarrSeasonMonitor, bool) {
		if m == nil {
			return nil, false
		}
		return m, true
	}
}

func TestMonitorSeason_monitors_and_searches_when_search_true(t *testing.T) {
	mock := newMonitorMock(2, false)
	uc := NewMonitorSeasonUseCase(MonitorSeasonDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{monitorSeasonEntry()}},
		SonarrFor:   monitorSonarrFor(mock),
	})

	res, err := uc.Execute(context.Background(), "main", 42, 2, true)
	require.NoError(t, err)
	assert.True(t, res.Monitored)
	assert.True(t, res.Searched)
	assert.Equal(t, domain.SonarrSeriesID(122), res.SonarrSeriesID)
	assert.Equal(t, 2, res.SeasonNumber)
	require.Len(t, mock.SetSeasonMonitoredCalls(), 1)
	assert.True(t, mock.SetSeasonMonitoredCalls()[0].Monitored)
	require.Len(t, mock.SearchSeasonCalls(), 1)
}

func TestMonitorSeason_skips_search_when_search_false(t *testing.T) {
	mock := newMonitorMock(2, false)
	uc := NewMonitorSeasonUseCase(MonitorSeasonDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{monitorSeasonEntry()}},
		SonarrFor:   monitorSonarrFor(mock),
	})

	res, err := uc.Execute(context.Background(), "main", 42, 2, false)
	require.NoError(t, err)
	assert.True(t, res.Monitored)
	assert.False(t, res.Searched)
	require.Len(t, mock.SetSeasonMonitoredCalls(), 1)
	assert.Empty(t, mock.SearchSeasonCalls())
}

func TestMonitorSeason_already_monitored_is_noop(t *testing.T) {
	mock := newMonitorMock(2, true) // season already monitored
	uc := NewMonitorSeasonUseCase(MonitorSeasonDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{monitorSeasonEntry()}},
		SonarrFor:   monitorSonarrFor(mock),
	})

	res, err := uc.Execute(context.Background(), "main", 42, 2, true)
	require.NoError(t, err)
	assert.True(t, res.Monitored)
	assert.False(t, res.Searched)
	assert.Empty(t, mock.SetSeasonMonitoredCalls(), "no write when already monitored")
	assert.Empty(t, mock.SearchSeasonCalls(), "no search when already monitored")
}

func TestMonitorSeason_series_not_in_instance_404(t *testing.T) {
	// Cache returns entries, but none for the requested instance.
	uc := NewMonitorSeasonUseCase(MonitorSeasonDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{
			{InstanceName: "other", SonarrSeriesID: 1},
		}},
		SonarrFor: monitorSonarrFor(newMonitorMock(2, false)),
	})

	_, err := uc.Execute(context.Background(), "main", 42, 2, true)
	require.Error(t, err)
	var nf *sharedErrors.InstanceNotFoundError
	require.True(t, errors.As(err, &nf))
}

func TestMonitorSeason_unknown_instance_404(t *testing.T) {
	// Instance is in cache but SonarrFor cannot resolve a live client.
	uc := NewMonitorSeasonUseCase(MonitorSeasonDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{monitorSeasonEntry()}},
		SonarrFor: func(domain.InstanceName) (SonarrSeasonMonitor, bool) {
			return nil, false
		},
	})

	_, err := uc.Execute(context.Background(), "main", 42, 2, true)
	require.Error(t, err)
	var nf *sharedErrors.InstanceNotFoundError
	require.True(t, errors.As(err, &nf))
}

func TestMonitorSeason_season_not_found_404(t *testing.T) {
	mock := newMonitorMock(1, false) // series only has season 1
	uc := NewMonitorSeasonUseCase(MonitorSeasonDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{monitorSeasonEntry()}},
		SonarrFor:   monitorSonarrFor(mock),
	})

	_, err := uc.Execute(context.Background(), "main", 42, 2, true)
	require.Error(t, err)
	var nf *sharedErrors.SeasonNotFoundError
	require.True(t, errors.As(err, &nf))
	assert.Empty(t, mock.SetSeasonMonitoredCalls())
}

func TestMonitorSeason_sonarr_network_error_502(t *testing.T) {
	mock := &ports.SonarrClientMock{
		GetSeriesFunc: func(_ context.Context, _ domain.SonarrSeriesID) (series.Series, error) {
			return series.Series{}, errors.New("dial tcp: connection refused")
		},
	}
	uc := NewMonitorSeasonUseCase(MonitorSeasonDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{monitorSeasonEntry()}},
		SonarrFor:   monitorSonarrFor(mock),
	})

	_, err := uc.Execute(context.Background(), "main", 42, 2, true)
	require.Error(t, err)
	var unreach *sharedErrors.SonarrUnreachableError
	require.True(t, errors.As(err, &unreach))
	assert.Equal(t, 502, sharedErrors.StatusCode(err))
}
