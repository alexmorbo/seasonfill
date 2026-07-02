package seriesdetail

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// --- seasons composer fakes (uniquely named to avoid composer_test.go clashes) ---

type seasonsFakeSeries struct {
	canon series.Canon
	err   error
}

func (f *seasonsFakeSeries) Get(_ context.Context, _ domain.SeriesID) (series.Canon, error) {
	if f.err != nil {
		return series.Canon{}, f.err
	}
	return f.canon, nil
}

func (f *seasonsFakeSeries) GetByTMDBID(_ context.Context, _ domain.TMDBID) (series.Canon, error) {
	return series.Canon{}, errors.New("not used")
}

func (f *seasonsFakeSeries) ListByIDs(_ context.Context, _ []domain.SeriesID) ([]series.Canon, error) {
	return nil, nil
}

func (f *seasonsFakeSeries) ListByTMDBIDs(_ context.Context, _ []domain.TMDBID) ([]series.Canon, error) {
	return nil, nil
}

type seasonsFakeSeasons struct {
	rows []series.CanonSeason
	err  error
}

func (f *seasonsFakeSeasons) ListBySeries(_ context.Context, _ domain.SeriesID) ([]series.CanonSeason, error) {
	return f.rows, f.err
}

type seasonsFakeTexts struct {
	rows map[int]series.SeasonText
	err  error
}

func (f *seasonsFakeTexts) ListBySeriesWithFallback(_ context.Context, _ domain.SeriesID, _ string) (map[int]series.SeasonText, error) {
	return f.rows, f.err
}

type seasonsFakeAgg struct {
	rows map[int]series.SeasonEpisodeAggregate
	err  error
}

func (f *seasonsFakeAgg) AggregateBySeries(_ context.Context, _ domain.SeriesID) (map[int]series.SeasonEpisodeAggregate, error) {
	return f.rows, f.err
}

type seasonsFakeFreshener struct {
	result FreshenResult
}

func (f *seasonsFakeFreshener) EnsureFreshScope(_ context.Context, _ domain.SeriesID, _ string, _ []freshener.Section, _ []int, _ bool, _ EnsureFreshMode) (FreshenResult, error) {
	return f.result, nil
}

func (f *seasonsFakeFreshener) EnsureFresh(_ context.Context, _ domain.SeriesID, _ string) FreshenResult {
	return f.result
}

func seasonsQuietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func fullCanon() series.Canon {
	return series.Canon{Hydration: series.HydrationFull, UpdatedAt: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)}
}

func TestSeasonsComposer_LocalizedName_RuPresent(t *testing.T) {
	t.Parallel()
	c := NewSeasonsComposer(SeasonsDeps{
		Series:  &seasonsFakeSeries{canon: fullCanon()},
		Seasons: &seasonsFakeSeasons{rows: []series.CanonSeason{{SeasonNumber: 1, Name: new("Season 1")}}},
		SeasonTexts: &seasonsFakeTexts{rows: map[int]series.SeasonText{
			1: {SeasonNumber: 1, Language: "ru-RU", Name: new("Сезон 1")},
		}},
		Aggregates: &seasonsFakeAgg{},
		Logger:     seasonsQuietLogger(),
	})
	out, err := c.Compose(context.Background(), 42, "ru-RU")
	require.NoError(t, err)
	require.Len(t, out.Seasons, 1)
	assert.Equal(t, "Сезон 1", out.Seasons[0].Name)
}

func TestSeasonsComposer_LocalizedName_EnFallback(t *testing.T) {
	t.Parallel()
	// Repo already resolved en-US for the season key; composer uses the map value.
	c := NewSeasonsComposer(SeasonsDeps{
		Series:  &seasonsFakeSeries{canon: fullCanon()},
		Seasons: &seasonsFakeSeasons{rows: []series.CanonSeason{{SeasonNumber: 2, Name: new("Canon 2")}}},
		SeasonTexts: &seasonsFakeTexts{rows: map[int]series.SeasonText{
			2: {SeasonNumber: 2, Language: "en-US", Name: new("Season Two")},
		}},
		Aggregates: &seasonsFakeAgg{},
		Logger:     seasonsQuietLogger(),
	})
	out, err := c.Compose(context.Background(), 42, "ru-RU")
	require.NoError(t, err)
	require.Len(t, out.Seasons, 1)
	assert.Equal(t, "Season Two", out.Seasons[0].Name)
}

func TestSeasonsComposer_BothAbsent_CanonName(t *testing.T) {
	t.Parallel()
	c := NewSeasonsComposer(SeasonsDeps{
		Series:      &seasonsFakeSeries{canon: fullCanon()},
		Seasons:     &seasonsFakeSeasons{rows: []series.CanonSeason{{SeasonNumber: 3, Name: new("Canon 3")}}},
		SeasonTexts: &seasonsFakeTexts{rows: map[int]series.SeasonText{}},
		Aggregates:  &seasonsFakeAgg{},
		Logger:      seasonsQuietLogger(),
	})
	out, err := c.Compose(context.Background(), 42, "ru-RU")
	require.NoError(t, err)
	require.Len(t, out.Seasons, 1)
	assert.Equal(t, "Canon 3", out.Seasons[0].Name)
}

func TestSeasonsComposer_BothAbsent_CanonNameNil_EmptyString(t *testing.T) {
	t.Parallel()
	c := NewSeasonsComposer(SeasonsDeps{
		Series:      &seasonsFakeSeries{canon: fullCanon()},
		Seasons:     &seasonsFakeSeasons{rows: []series.CanonSeason{{SeasonNumber: 4, Name: nil}}},
		SeasonTexts: &seasonsFakeTexts{rows: map[int]series.SeasonText{}},
		Aggregates:  &seasonsFakeAgg{},
		Logger:      seasonsQuietLogger(),
	})
	out, err := c.Compose(context.Background(), 42, "ru-RU")
	require.NoError(t, err)
	require.Len(t, out.Seasons, 1)
	assert.Equal(t, "", out.Seasons[0].Name)
}

func TestSeasonsComposer_AirDateEnd_MaxAndStartFallback(t *testing.T) {
	t.Parallel()
	end := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewSeasonsComposer(SeasonsDeps{
		Series: &seasonsFakeSeries{canon: fullCanon()},
		// canon AirDate nil → AirDateStart falls back to aggregate FirstAirDate.
		Seasons:     &seasonsFakeSeasons{rows: []series.CanonSeason{{SeasonNumber: 1, AirDate: nil}}},
		SeasonTexts: &seasonsFakeTexts{},
		Aggregates: &seasonsFakeAgg{rows: map[int]series.SeasonEpisodeAggregate{
			1: {SeasonNumber: 1, EpisodeCount: 3, FirstAirDate: &start, LastAirDate: &end},
		}},
		Logger: seasonsQuietLogger(),
	})
	out, err := c.Compose(context.Background(), 42, "ru-RU")
	require.NoError(t, err)
	require.Len(t, out.Seasons, 1)
	require.NotNil(t, out.Seasons[0].AirDateEnd)
	assert.True(t, out.Seasons[0].AirDateEnd.Equal(end))
	require.NotNil(t, out.Seasons[0].AirDateStart)
	assert.True(t, out.Seasons[0].AirDateStart.Equal(start), "start falls back to aggregate MIN when canon AirDate nil")
}

func TestSeasonsComposer_AirDateStart_PrefersCanon(t *testing.T) {
	t.Parallel()
	canonStart := time.Date(2020, 5, 5, 0, 0, 0, 0, time.UTC)
	aggStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewSeasonsComposer(SeasonsDeps{
		Series:      &seasonsFakeSeries{canon: fullCanon()},
		Seasons:     &seasonsFakeSeasons{rows: []series.CanonSeason{{SeasonNumber: 1, AirDate: &canonStart}}},
		SeasonTexts: &seasonsFakeTexts{},
		Aggregates: &seasonsFakeAgg{rows: map[int]series.SeasonEpisodeAggregate{
			1: {SeasonNumber: 1, FirstAirDate: &aggStart},
		}},
		Logger: seasonsQuietLogger(),
	})
	out, err := c.Compose(context.Background(), 42, "ru-RU")
	require.NoError(t, err)
	require.NotNil(t, out.Seasons[0].AirDateStart)
	assert.True(t, out.Seasons[0].AirDateStart.Equal(canonStart), "canon AirDate wins over aggregate MIN")
}

func TestSeasonsComposer_EpisodeCount_CanonWins(t *testing.T) {
	t.Parallel()
	c := NewSeasonsComposer(SeasonsDeps{
		Series:      &seasonsFakeSeries{canon: fullCanon()},
		Seasons:     &seasonsFakeSeasons{rows: []series.CanonSeason{{SeasonNumber: 1, EpisodeCount: new(22)}}},
		SeasonTexts: &seasonsFakeTexts{},
		Aggregates: &seasonsFakeAgg{rows: map[int]series.SeasonEpisodeAggregate{
			1: {SeasonNumber: 1, EpisodeCount: 5},
		}},
		Logger: seasonsQuietLogger(),
	})
	out, err := c.Compose(context.Background(), 42, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, 22, out.Seasons[0].EpisodeCount, "canon TMDB-declared count wins")
}

func TestSeasonsComposer_EpisodeCount_AggregateFallback(t *testing.T) {
	t.Parallel()
	c := NewSeasonsComposer(SeasonsDeps{
		Series:      &seasonsFakeSeries{canon: fullCanon()},
		Seasons:     &seasonsFakeSeasons{rows: []series.CanonSeason{{SeasonNumber: 1, EpisodeCount: nil}}},
		SeasonTexts: &seasonsFakeTexts{},
		Aggregates: &seasonsFakeAgg{rows: map[int]series.SeasonEpisodeAggregate{
			1: {SeasonNumber: 1, EpisodeCount: 7},
		}},
		Logger: seasonsQuietLogger(),
	})
	out, err := c.Compose(context.Background(), 42, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, 7, out.Seasons[0].EpisodeCount, "aggregate count used when canon nil")
}

func TestSeasonsComposer_SingleSeason(t *testing.T) {
	t.Parallel()
	c := NewSeasonsComposer(SeasonsDeps{
		Series:      &seasonsFakeSeries{canon: fullCanon()},
		Seasons:     &seasonsFakeSeasons{rows: []series.CanonSeason{{SeasonNumber: 1, Name: new("Star City S1")}}},
		SeasonTexts: &seasonsFakeTexts{},
		Aggregates:  &seasonsFakeAgg{},
		Logger:      seasonsQuietLogger(),
	})
	out, err := c.Compose(context.Background(), 372, "ru-RU")
	require.NoError(t, err)
	assert.Len(t, out.Seasons, 1)
}

func TestSeasonsComposer_Degraded_ColdCanonAndFreshener(t *testing.T) {
	t.Parallel()
	c := NewSeasonsComposer(SeasonsDeps{
		Series:      &seasonsFakeSeries{canon: series.Canon{Hydration: series.HydrationStub}},
		Seasons:     &seasonsFakeSeasons{rows: []series.CanonSeason{{SeasonNumber: 1}}},
		SeasonTexts: &seasonsFakeTexts{},
		Aggregates:  &seasonsFakeAgg{},
		Freshener:   &seasonsFakeFreshener{result: FreshenResult{Degraded: true}},
		Logger:      seasonsQuietLogger(),
	})
	out, err := c.Compose(context.Background(), 42, "ru-RU")
	require.NoError(t, err)
	assert.Contains(t, out.Degraded, "tmdb_series")
	assert.Contains(t, out.Degraded, "freshener")
}

func TestSeasonsComposer_NotFound(t *testing.T) {
	t.Parallel()
	notFound := errors.Join(&sharedErrors.SeriesNotFoundError{ID: 42}, ports.ErrNotFound)
	c := NewSeasonsComposer(SeasonsDeps{
		Series:      &seasonsFakeSeries{err: notFound},
		Seasons:     &seasonsFakeSeasons{},
		SeasonTexts: &seasonsFakeTexts{},
		Aggregates:  &seasonsFakeAgg{},
		Logger:      seasonsQuietLogger(),
	})
	_, err := c.Compose(context.Background(), 42, "ru-RU")
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)
}

func TestSeasonsComposer_TextsAndAggregateError_NonFatal(t *testing.T) {
	t.Parallel()
	c := NewSeasonsComposer(SeasonsDeps{
		Series:      &seasonsFakeSeries{canon: fullCanon()},
		Seasons:     &seasonsFakeSeasons{rows: []series.CanonSeason{{SeasonNumber: 1, Name: new("Canon 1"), EpisodeCount: new(9)}}},
		SeasonTexts: &seasonsFakeTexts{err: errors.New("texts db down")},
		Aggregates:  &seasonsFakeAgg{err: errors.New("agg db down")},
		Logger:      seasonsQuietLogger(),
	})
	out, err := c.Compose(context.Background(), 42, "ru-RU")
	require.NoError(t, err, "texts/aggregate errors must not fail the page")
	require.Len(t, out.Seasons, 1)
	assert.Equal(t, "Canon 1", out.Seasons[0].Name)
	assert.Equal(t, 9, out.Seasons[0].EpisodeCount)
	assert.NotContains(t, out.Degraded, "freshener")
}
