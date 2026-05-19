package evaluate

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
)

type fakeSonarr struct {
	releases []release.Release
	err      error
}

func (f *fakeSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{Version: "test"}, nil
}
func (f *fakeSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (f *fakeSonarr) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (f *fakeSonarr) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (f *fakeSonarr) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return nil, nil
}
func (f *fakeSonarr) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	return f.releases, f.err
}
func (f *fakeSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (f *fakeSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (f *fakeSonarr) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (f *fakeSonarr) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *fakeSonarr) Name() string { return "test-instance" }

type fakeDecisionRepo struct {
	saved []decision.Decision
}

func (r *fakeDecisionRepo) Save(_ context.Context, d decision.Decision) error {
	r.saved = append(r.saved, d)
	return nil
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func makeProfile() ports.QualityProfile {
	return ports.QualityProfile{
		ID:   14,
		Name: "Any 2160p",
		Items: []ports.QualityItem{
			{ID: 1, Name: "SDTV", Order: 1},
			{ID: 3, Name: "WEBDL-1080p", Order: 5},
			{ID: 19, Name: "WEBDL-2160p", Order: 9},
		},
	}
}

func makeSeason(missing, have []int) series.Season {
	eps := make([]series.Episode, 0)
	for _, n := range have {
		eps = append(eps, series.Episode{ID: n, Number: n, SeasonNumber: 2, Monitored: true, HasFile: true, QualityID: 19, QualityName: "WEBDL-2160p"})
	}
	for _, n := range missing {
		eps = append(eps, series.Episode{ID: n + 1000, Number: n, SeasonNumber: 2, Monitored: true, HasFile: false})
	}
	return series.Season{Number: 2, Monitored: true, Episodes: eps}
}

func makeSeries() series.Series {
	return series.Series{ID: 122, Title: "Hijack", Type: series.SeriesTypeStandard, Monitored: true, QualityProfile: 14}
}

func TestEvaluate_DryRunGrabSelected(t *testing.T) {
	ctx := context.Background()
	repo := &fakeDecisionRepo{}
	sonarr := &fakeSonarr{
		releases: []release.Release{
			{
				GUID:                 "rt-1",
				Title:                "Hijack S02E1-8 WEBDL-2160p",
				IndexerName:          "RuTracker",
				IndexerID:            1,
				IndexerPriority:      1,
				QualityID:            19,
				QualityName:          "WEBDL-2160p",
				CustomFormatScore:    500,
				Seeders:              142,
				SizeBytes:            77_000_000_000,
				MappedEpisodeNumbers: []int{1, 2, 3, 4, 5, 6, 7, 8},
				Rejections:           []string{"Existing file on disk has a equal or higher Custom Format score: 500"},
			},
			{
				GUID:                 "kz-1",
				Title:                "Hijack S02E1-3 WEBDL-2160p",
				IndexerName:          "Kinozal",
				IndexerID:            2,
				IndexerPriority:      5,
				QualityID:            19,
				QualityName:          "WEBDL-2160p",
				CustomFormatScore:    500,
				Seeders:              50,
				SizeBytes:            25_000_000_000,
				MappedEpisodeNumbers: []int{1, 2, 3},
				Rejections:           []string{"Existing file on disk is of equal or higher preference: 500"},
			},
		},
	}

	uc := NewUseCase(sonarr, repo, newLogger())
	d, err := uc.Execute(ctx, Input{
		ScanRunID:   uuid.New(),
		Instance:    "sonarr-main",
		Series:      makeSeries(),
		Season:      makeSeason([]int{4, 5, 6, 7, 8}, []int{1, 2, 3}),
		Profile:     makeProfile(),
		OriginGUID:  "rt-1",
		OriginBonus: 1.0,
		DryRun:      true,
		Now:         time.Now().UTC(),
	})

	require.NoError(t, err)
	assert.Equal(t, decision.OutcomeGrab, d.Outcome)
	assert.Equal(t, decision.ReasonGrabSelectedDryRun, d.Reason)
	assert.True(t, d.WouldGrab)
	require.NotNil(t, d.Selected)
	assert.Equal(t, "rt-1", d.Selected.Release.GUID)
	assert.Equal(t, 5, d.Selected.Coverage)
	assert.Len(t, repo.saved, 1)
}

func TestEvaluate_SkipNoMissing(t *testing.T) {
	ctx := context.Background()
	repo := &fakeDecisionRepo{}
	uc := NewUseCase(&fakeSonarr{}, repo, newLogger())
	d, err := uc.Execute(ctx, Input{
		ScanRunID: uuid.New(),
		Instance:  "x",
		Series:    makeSeries(),
		Season:    makeSeason(nil, []int{1, 2, 3}),
		Profile:   makeProfile(),
		DryRun:    true,
		Now:       time.Now().UTC(),
	})
	require.NoError(t, err)
	assert.Equal(t, decision.OutcomeSkip, d.Outcome)
	assert.Equal(t, decision.ReasonSkipNoMissing, d.Reason)
}

func TestEvaluate_SkipFullMissing(t *testing.T) {
	ctx := context.Background()
	repo := &fakeDecisionRepo{}
	uc := NewUseCase(&fakeSonarr{}, repo, newLogger())
	d, err := uc.Execute(ctx, Input{
		ScanRunID: uuid.New(),
		Instance:  "x",
		Series:    makeSeries(),
		Season:    makeSeason([]int{1, 2, 3, 4}, nil),
		Profile:   makeProfile(),
		DryRun:    true,
		Now:       time.Now().UTC(),
	})
	require.NoError(t, err)
	assert.Equal(t, decision.OutcomeSkip, d.Outcome)
	assert.Equal(t, decision.ReasonSkipFullMissing, d.Reason)
}

func TestEvaluate_SkipAnime(t *testing.T) {
	ctx := context.Background()
	uc := NewUseCase(&fakeSonarr{}, &fakeDecisionRepo{}, newLogger())
	srs := makeSeries()
	srs.Type = series.SeriesTypeAnime
	d, err := uc.Execute(ctx, Input{
		ScanRunID:    uuid.New(),
		Instance:     "x",
		Series:       srs,
		Season:       makeSeason([]int{4}, []int{1, 2, 3}),
		Profile:      makeProfile(),
		SkipAnime:    true,
		SkipSpecials: true,
		DryRun:       true,
		Now:          time.Now().UTC(),
	})
	require.NoError(t, err)
	assert.Equal(t, decision.OutcomeSkip, d.Outcome)
	assert.Equal(t, decision.ReasonSkipAnime, d.Reason)
}

func TestEvaluate_NoCandidatesAfterFilter(t *testing.T) {
	ctx := context.Background()
	sonarr := &fakeSonarr{
		releases: []release.Release{
			{
				GUID:                 "bad-1",
				Title:                "Hijack S02 WEBDL-1080p",
				QualityID:            3,
				QualityName:          "WEBDL-1080p",
				MappedEpisodeNumbers: []int{4, 5, 6, 7, 8},
				Rejections:           []string{"Existing file on disk has a equal or higher Custom Format score: 500"},
			},
		},
	}
	uc := NewUseCase(sonarr, &fakeDecisionRepo{}, newLogger())
	d, err := uc.Execute(ctx, Input{
		ScanRunID: uuid.New(),
		Instance:  "x",
		Series:    makeSeries(),
		Season:    makeSeason([]int{4, 5, 6, 7, 8}, []int{1, 2, 3}),
		Profile:   makeProfile(),
		DryRun:    true,
		Now:       time.Now().UTC(),
	})
	require.NoError(t, err)
	assert.Equal(t, decision.OutcomeSkip, d.Outcome)
	assert.Equal(t, decision.ReasonSkipNoCandidates, d.Reason)
	assert.NotEmpty(t, d.FilteredOut)
}

func TestEvaluate_ErrorFromSonarr(t *testing.T) {
	ctx := context.Background()
	sonarr := &fakeSonarr{err: errors.New("boom")}
	uc := NewUseCase(sonarr, &fakeDecisionRepo{}, newLogger())
	d, err := uc.Execute(ctx, Input{
		ScanRunID: uuid.New(),
		Instance:  "x",
		Series:    makeSeries(),
		Season:    makeSeason([]int{4}, []int{1, 2, 3}),
		Profile:   makeProfile(),
		DryRun:    true,
		Now:       time.Now().UTC(),
	})
	require.Error(t, err)
	assert.Equal(t, decision.OutcomeError, d.Outcome)
}

func TestFilter_QualityDowngradeBlocked(t *testing.T) {
	have := []series.Episode{
		{Number: 1, QualityID: 19, QualityName: "WEBDL-2160p", HasFile: true, Monitored: true},
	}
	in := FilterInput{
		Profile: makeProfile(),
		Have:    have,
		Missing: []int{2, 3},
		Releases: []release.Release{
			{
				GUID:                 "a",
				QualityID:            3,
				QualityName:          "WEBDL-1080p",
				MappedEpisodeNumbers: []int{1, 2, 3},
			},
		},
	}
	res := Filter(in)
	require.Empty(t, res.Kept)
	require.Len(t, res.FilteredOut, 1)
	assert.Equal(t, string(decision.ReasonFilterQualityDowngrade), res.FilteredOut[0].Reason)
}

func TestRank_OriginAsTieBreaker(t *testing.T) {
	rels := []release.Release{
		{GUID: "a", IndexerPriority: 5, CustomFormatScore: 500, MappedEpisodeNumbers: []int{1, 2, 3}, Seeders: 10, SizeBytes: 1000},
		{GUID: "b", IndexerPriority: 5, CustomFormatScore: 500, MappedEpisodeNumbers: []int{1, 2, 3}, Seeders: 10, SizeBytes: 1000},
	}
	scored := Rank(RankInput{Releases: rels, Missing: []int{1, 2, 3}, OriginGUID: "b", OriginBonus: 1.0})
	require.Len(t, scored, 2)
	assert.Equal(t, "b", scored[0].Release.GUID)
}
