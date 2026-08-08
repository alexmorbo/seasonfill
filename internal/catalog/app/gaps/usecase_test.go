package gaps

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

var gapsNow = time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

// fakeGapRepo satisfies ports.GapRepository so usecase tests exercise the
// real assembly (instance fan-out, clock injection, rank-ordered nested
// folding) without a DB. ranks drives the authoritative series list;
// episodes fills in the season/episode detail.
type fakeGapRepo struct {
	instances    []string
	missing      map[string]int
	wholeSeason  map[string]int
	ranks        map[string][]ports.GapSeriesRank
	episodes     map[string][]ports.GapEpisodeRow
	gotNow       time.Time
	gotIDs       map[string][]domain.SeriesID
	err          error
	instancesErr error
}

func (f *fakeGapRepo) DistinctInstances(context.Context) ([]string, error) {
	if f.instancesErr != nil {
		return nil, f.instancesErr
	}
	return f.instances, nil
}

func (f *fakeGapRepo) MissingEpisodeCount(_ context.Context, instance string, now time.Time) (int, error) {
	f.gotNow = now
	if f.err != nil {
		return 0, f.err
	}
	return f.missing[instance], nil
}

func (f *fakeGapRepo) WholeSeasonMissingCount(_ context.Context, instance string, _ time.Time) (int, error) {
	return f.wholeSeason[instance], nil
}

func (f *fakeGapRepo) GapSeriesRanked(_ context.Context, instance string, _ time.Time, _ int) ([]ports.GapSeriesRank, error) {
	return f.ranks[instance], nil
}

func (f *fakeGapRepo) GapEpisodesForSeries(_ context.Context, instance string, _ time.Time, ids []domain.SeriesID, _ int) ([]ports.GapEpisodeRow, error) {
	if f.gotIDs == nil {
		f.gotIDs = map[string][]domain.SeriesID{}
	}
	f.gotIDs[instance] = ids
	return f.episodes[instance], nil
}

func newFakeUseCase(repo ports.GapRepository) *UseCase {
	return NewUseCase(repo).WithClock(func() time.Time { return gapsNow })
}

func TestUseCase_Build_AggregatesAllInstances(t *testing.T) {
	t.Parallel()
	repo := &fakeGapRepo{
		instances:   []string{"anime", "main"},
		missing:     map[string]int{"anime": 1, "main": 3},
		wholeSeason: map[string]int{"anime": 0, "main": 1},
		ranks: map[string][]ports.GapSeriesRank{
			"main":  {{SeriesID: 42, Title: "The Expanse", GapCount: 3}},
			"anime": {{SeriesID: 7, Title: "Frieren", GapCount: 1}},
		},
		episodes: map[string][]ports.GapEpisodeRow{
			"main": {
				{SeriesID: 42, Title: "The Expanse", SeasonNumber: 2, EpisodeNumber: 3, EpisodeID: 100, SeasonAiredMonitored: 3, SeasonMissing: 3},
				{SeriesID: 42, Title: "The Expanse", SeasonNumber: 2, EpisodeNumber: 4, EpisodeID: 101, SeasonAiredMonitored: 3, SeasonMissing: 3},
				{SeriesID: 42, Title: "The Expanse", SeasonNumber: 2, EpisodeNumber: 5, EpisodeID: 102, SeasonAiredMonitored: 3, SeasonMissing: 3},
			},
			"anime": {
				{SeriesID: 7, Title: "Frieren", SeasonNumber: 1, EpisodeNumber: 9, EpisodeID: 200, SeasonAiredMonitored: 12, SeasonMissing: 1},
			},
		},
	}
	uc := newFakeUseCase(repo)

	rep, err := uc.Build(context.Background(), "")
	require.NoError(t, err)

	assert.Equal(t, gapsNow, rep.GeneratedAt)
	assert.Equal(t, gapsNow, repo.gotNow, "usecase must pass its clock now to the repo as the aired boundary")
	require.Len(t, rep.Instances, 2)

	// Order preserved from DistinctInstances.
	assert.Equal(t, "anime", rep.Instances[0].InstanceName)
	assert.Equal(t, "main", rep.Instances[1].InstanceName)

	main := rep.Instances[1]
	assert.Equal(t, 3, main.MissingEpisodeCount)
	assert.Equal(t, 1, main.WholeSeasonMissingCount)
	require.Len(t, main.Series, 1)
	assert.Equal(t, domain.SeriesID(42), main.Series[0].SeriesID)
	assert.Equal(t, "The Expanse", main.Series[0].Title)
	assert.Equal(t, 3, main.Series[0].MissingCount, "badge = exact rank GapCount")
	require.Len(t, main.Series[0].Seasons, 1)

	// buildInstance must forward the ranked ids to the detail query.
	assert.Equal(t, []domain.SeriesID{42}, repo.gotIDs["main"])

	season := main.Series[0].Seasons[0]
	assert.Equal(t, 2, season.SeasonNumber)
	assert.Equal(t, 3, season.MissingCount)
	assert.Equal(t, 3, season.AiredMonitoredCount)
	assert.True(t, season.WholeSeasonMissing, "3 missing of 3 aired-monitored → whole season missing")
	require.Len(t, season.Episodes, 3)
	assert.Equal(t, domain.EpisodeID(100), season.Episodes[0].EpisodeID)

	anime := rep.Instances[0]
	require.Len(t, anime.Series, 1)
	assert.Equal(t, 1, anime.Series[0].MissingCount)
	require.Len(t, anime.Series[0].Seasons, 1)
	assert.False(t, anime.Series[0].Seasons[0].WholeSeasonMissing, "1 missing of 12 → not whole season")
	assert.Equal(t, 1, anime.Series[0].Seasons[0].MissingCount)
	assert.Equal(t, 12, anime.Series[0].Seasons[0].AiredMonitoredCount)
}

func TestUseCase_Build_InstanceFilter_SingleElement(t *testing.T) {
	t.Parallel()
	repo := &fakeGapRepo{
		// DistinctInstances would return both, but the filter must bypass it.
		instances:   []string{"anime", "main"},
		missing:     map[string]int{"main": 2},
		wholeSeason: map[string]int{"main": 0},
		ranks: map[string][]ports.GapSeriesRank{
			"main": {{SeriesID: 1, Title: "A", GapCount: 2}},
		},
		episodes: map[string][]ports.GapEpisodeRow{
			"main": {
				{SeriesID: 1, Title: "A", SeasonNumber: 1, EpisodeNumber: 1, EpisodeID: 10, SeasonAiredMonitored: 5, SeasonMissing: 2},
				{SeriesID: 1, Title: "A", SeasonNumber: 1, EpisodeNumber: 2, EpisodeID: 11, SeasonAiredMonitored: 5, SeasonMissing: 2},
			},
		},
	}
	uc := newFakeUseCase(repo)

	rep, err := uc.Build(context.Background(), "main")
	require.NoError(t, err)
	require.Len(t, rep.Instances, 1)
	assert.Equal(t, "main", rep.Instances[0].InstanceName)
	assert.Equal(t, 2, rep.Instances[0].MissingEpisodeCount)
	require.Len(t, rep.Instances[0].Series, 1)
	assert.Equal(t, 2, rep.Instances[0].Series[0].MissingCount)
}

// A filtered instance with no gaps still surfaces a zero-count element,
// never a 404 / empty report.
func TestUseCase_Build_InstanceFilter_HealthyIsZeroElement(t *testing.T) {
	t.Parallel()
	repo := &fakeGapRepo{
		missing:     map[string]int{},
		wholeSeason: map[string]int{},
		ranks:       map[string][]ports.GapSeriesRank{},
		episodes:    map[string][]ports.GapEpisodeRow{},
	}
	uc := newFakeUseCase(repo)

	rep, err := uc.Build(context.Background(), "ghost")
	require.NoError(t, err)
	require.Len(t, rep.Instances, 1)
	assert.Equal(t, "ghost", rep.Instances[0].InstanceName)
	assert.Equal(t, 0, rep.Instances[0].MissingEpisodeCount)
	assert.Equal(t, 0, rep.Instances[0].WholeSeasonMissingCount)
	assert.Empty(t, rep.Instances[0].Series)
}

func TestUseCase_Build_EmptyWhenNoInstances(t *testing.T) {
	t.Parallel()
	repo := &fakeGapRepo{instances: nil}
	uc := newFakeUseCase(repo)

	rep, err := uc.Build(context.Background(), "")
	require.NoError(t, err)
	assert.Empty(t, rep.Instances)
	assert.NotNil(t, rep.Instances)
}

// Series order comes from the RANK list (biggest-gap-first), not from the
// detail-row order. Season order within a series is first-seen in detail.
func TestUseCase_Build_RankOrderAndSeasonFolding(t *testing.T) {
	t.Parallel()
	repo := &fakeGapRepo{
		instances:   []string{"main"},
		missing:     map[string]int{"main": 4},
		wholeSeason: map[string]int{"main": 1},
		ranks: map[string][]ports.GapSeriesRank{
			// Nine listed first despite higher id — it has more gaps? No:
			// Five has 3 gaps, Nine has 1. Rank order = Five, Nine.
			"main": {
				{SeriesID: 5, Title: "Five", GapCount: 3},
				{SeriesID: 9, Title: "Nine", GapCount: 1},
			},
		},
		episodes: map[string][]ports.GapEpisodeRow{
			"main": {
				{SeriesID: 5, Title: "Five", SeasonNumber: 1, EpisodeNumber: 1, EpisodeID: 1, SeasonAiredMonitored: 2, SeasonMissing: 2},
				{SeriesID: 5, Title: "Five", SeasonNumber: 1, EpisodeNumber: 2, EpisodeID: 2, SeasonAiredMonitored: 2, SeasonMissing: 2},
				{SeriesID: 5, Title: "Five", SeasonNumber: 2, EpisodeNumber: 1, EpisodeID: 3, SeasonAiredMonitored: 10, SeasonMissing: 1},
				{SeriesID: 9, Title: "Nine", SeasonNumber: 3, EpisodeNumber: 4, EpisodeID: 4, SeasonAiredMonitored: 8, SeasonMissing: 1},
			},
		},
	}
	uc := newFakeUseCase(repo)

	rep, err := uc.Build(context.Background(), "main")
	require.NoError(t, err)
	require.Len(t, rep.Instances, 1)
	series := rep.Instances[0].Series
	require.Len(t, series, 2)

	assert.Equal(t, domain.SeriesID(5), series[0].SeriesID)
	assert.Equal(t, 3, series[0].MissingCount, "exact rank GapCount")
	require.Len(t, series[0].Seasons, 2)
	assert.Equal(t, 1, series[0].Seasons[0].SeasonNumber)
	assert.True(t, series[0].Seasons[0].WholeSeasonMissing)
	assert.Equal(t, 2, series[0].Seasons[1].SeasonNumber)
	assert.False(t, series[0].Seasons[1].WholeSeasonMissing)

	assert.Equal(t, domain.SeriesID(9), series[1].SeriesID)
	assert.Equal(t, 1, series[1].MissingCount)
}

// A ranked series whose detail rows were clipped by the safety cap still
// appears with its EXACT badge and an empty (non-nil) Seasons slice — the
// cap can never drop a series.
func TestUseCase_Build_RankedSeriesWithoutDetailKeepsBadge(t *testing.T) {
	t.Parallel()
	repo := &fakeGapRepo{
		instances:   []string{"main"},
		missing:     map[string]int{"main": 70},
		wholeSeason: map[string]int{"main": 0},
		ranks: map[string][]ports.GapSeriesRank{
			"main": {{SeriesID: 3, Title: "Clipped", GapCount: 70}},
		},
		// No detail rows for series 3 (simulating the tail clip).
		episodes: map[string][]ports.GapEpisodeRow{"main": {}},
	}
	uc := newFakeUseCase(repo)

	rep, err := uc.Build(context.Background(), "main")
	require.NoError(t, err)
	require.Len(t, rep.Instances[0].Series, 1)
	s := rep.Instances[0].Series[0]
	assert.Equal(t, domain.SeriesID(3), s.SeriesID)
	assert.Equal(t, 70, s.MissingCount, "badge is exact even with zero detail rows")
	assert.NotNil(t, s.Seasons)
	assert.Empty(t, s.Seasons)
}

func TestUseCase_Build_ListInstancesError(t *testing.T) {
	t.Parallel()
	repo := &fakeGapRepo{instancesErr: errors.New("boom")}
	uc := newFakeUseCase(repo)

	_, err := uc.Build(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list instances")
}

func TestUseCase_Build_RepoError(t *testing.T) {
	t.Parallel()
	repo := &fakeGapRepo{instances: []string{"main"}, err: errors.New("db down")}
	uc := newFakeUseCase(repo)

	_, err := uc.Build(context.Background(), "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing count")
}
