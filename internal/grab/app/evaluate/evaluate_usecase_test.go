package evaluate

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
)

type stubSonarr struct {
	releases []release.Release
}

func (s *stubSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{}, nil
}
func (s *stubSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (s *stubSonarr) ListSeriesCache(_ context.Context, _ domain.InstanceName) ([]series.CacheEntry, error) {
	return nil, nil
}
func (s *stubSonarr) GetSeries(_ context.Context, _ domain.SonarrSeriesID) (series.Series, error) {
	return series.Series{}, nil
}
func (s *stubSonarr) ListEpisodes(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]series.Episode, error) {
	return nil, nil
}

func (s *stubSonarr) ListEpisodesBySeries(_ context.Context, _ domain.SonarrSeriesID) ([]series.Episode, error) {
	return nil, nil
}
func (s *stubSonarr) ListEpisodeFiles(_ context.Context, _ domain.SonarrSeriesID) (map[int]int, error) {
	return nil, nil
}
func (s *stubSonarr) ListEpisodeFilesBySeason(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]ports.EpisodeFileDetail, error) {
	return nil, nil
}
func (s *stubSonarr) SearchReleases(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]release.Release, error) {
	return s.releases, nil
}
func (s *stubSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{Items: []ports.QualityItem{{ID: 19, Order: 9, Name: "WEBDL-2160p"}}}, nil
}
func (s *stubSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (s *stubSonarr) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (s *stubSonarr) GrabHistory(_ context.Context, _ domain.SonarrSeriesID) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *stubSonarr) ParseRelease(ctx context.Context, title string) (ports.ParseResult, error) {
	return ports.ParseResult{}, nil
}
func (s *stubSonarr) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}
func (s *stubSonarr) Name() string { return "stub" }

type recDecisions struct{ list []decision.Decision }

func (r *recDecisions) Save(_ context.Context, d decision.Decision) error {
	r.list = append(r.list, d)
	return nil
}

func (r *recDecisions) GetByID(_ context.Context, _ uuid.UUID) (decision.Decision, error) {
	return decision.Decision{}, ports.ErrNotFound
}

func (r *recDecisions) List(_ context.Context, _ ports.DecisionFilter, _ ports.Pagination) ([]decision.Decision, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

func (r *recDecisions) UpdateSupersededBy(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (r *recDecisions) ClearSupersededBy(context.Context, uuid.UUID) error {
	return nil
}

func (r *recDecisions) UpdateIntent(context.Context, uuid.UUID, *decision.Intent) error {
	return nil
}

func makeSeason(missing []int, have []int) series.Season {
	eps := make([]series.Episode, 0, len(missing)+len(have))
	for _, n := range missing {
		eps = append(eps, series.Episode{Number: n, Monitored: true, HasFile: false})
	}
	for _, n := range have {
		eps = append(eps, series.Episode{Number: n, Monitored: true, HasFile: true, QualityID: 19})
	}
	return series.Season{Number: 2, Monitored: true, Episodes: eps}
}

func TestExecute_GrabDecision_DryRun(t *testing.T) {
	t.Parallel()
	stub := &stubSonarr{releases: []release.Release{
		{
			GUID: "g1", Title: "Pack", QualityID: 19, QualityName: "WEBDL-2160p",
			IndexerName: "RT", MappedEpisodeNumbers: []int{1, 2, 3, 4, 5},
			CustomFormatScore: 500, Rejections: []string{"Full season pack"},
		},
	}}
	uc := NewUseCase(stub, &recDecisions{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := uc.Execute(context.Background(), Input{
		ScanRunID: uuid.New(), Instance: "x",
		Series:  series.Series{ID: 1, Title: "S", Type: series.SeriesTypeStandard, Monitored: true},
		Season:  makeSeason([]int{4, 5}, []int{1, 2, 3}),
		Profile: ports.QualityProfile{Items: []ports.QualityItem{{ID: 19, Order: 9}}},
		Sonarr:  stub,
		DryRun:  true,
		Now:     time.Now().UTC(),
	})
	require.NoError(t, err)
	assert.Equal(t, decision.OutcomeGrab, d.Outcome)
	assert.True(t, d.DryRunWouldGrab)
	require.NotNil(t, d.Selected)
}

func TestExecute_GrabDecision_RealGrabReturnsScored(t *testing.T) {
	t.Parallel()
	stub := &stubSonarr{releases: []release.Release{
		{
			GUID: "g1", Title: "Pack", QualityID: 19, QualityName: "WEBDL-2160p",
			IndexerName: "RT", MappedEpisodeNumbers: []int{1, 2, 3, 4, 5},
			CustomFormatScore: 500, Rejections: []string{"Full season pack"},
		},
	}}
	uc := NewUseCase(stub, &recDecisions{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := uc.Execute(context.Background(), Input{
		ScanRunID: uuid.New(), Instance: "x",
		Series:  series.Series{ID: 1, Title: "S", Type: series.SeriesTypeStandard, Monitored: true},
		Season:  makeSeason([]int{4, 5}, []int{1, 2, 3}),
		Profile: ports.QualityProfile{Items: []ports.QualityItem{{ID: 19, Order: 9}}},
		Sonarr:  stub,
		DryRun:  false,
		Now:     time.Now().UTC(),
	})
	require.NoError(t, err)
	assert.Equal(t, decision.OutcomeGrab, d.Outcome)
	assert.False(t, d.DryRunWouldGrab) // real-grab path; grab_records is the audit
	require.NotNil(t, d.Selected)
	assert.Equal(t, "g1", d.Selected.Release.GUID)
}

func TestExecute_GUIDCooldownExcluded(t *testing.T) {
	t.Parallel()
	stub := &stubSonarr{releases: []release.Release{
		{
			GUID: "g1", Title: "Pack", QualityID: 19, QualityName: "WEBDL-2160p",
			IndexerName: "RT", MappedEpisodeNumbers: []int{1, 2, 3, 4, 5},
			CustomFormatScore: 500, Rejections: []string{"Full season pack"},
		},
		{
			GUID: "g2", Title: "Alt", QualityID: 19, QualityName: "WEBDL-2160p",
			IndexerName: "KZ", MappedEpisodeNumbers: []int{1, 2, 3, 4, 5},
			CustomFormatScore: 400, Rejections: []string{"Full season pack"},
		},
	}}
	uc := NewUseCase(stub, &recDecisions{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := uc.Execute(context.Background(), Input{
		ScanRunID: uuid.New(), Instance: "x",
		Series:       series.Series{ID: 1, Title: "S", Type: series.SeriesTypeStandard, Monitored: true},
		Season:       makeSeason([]int{4, 5}, []int{1, 2, 3}),
		Profile:      ports.QualityProfile{Items: []ports.QualityItem{{ID: 19, Order: 9}}},
		Sonarr:       stub,
		DryRun:       true,
		Now:          time.Now().UTC(),
		ExcludeGUIDs: map[string]struct{}{"g1": {}},
	})
	require.NoError(t, err)
	require.NotNil(t, d.Selected)
	assert.Equal(t, "g2", d.Selected.Release.GUID, "g1 must be filtered by cooldown")

	foundCooldownReason := false
	for _, fc := range d.FilteredOut {
		if fc.GUID == "g1" && fc.Reason == string(decision.ReasonFilterGUIDCooldown) {
			foundCooldownReason = true
		}
	}
	assert.True(t, foundCooldownReason)
}

func TestExecute_AllCandidatesCooldown_NoGrab(t *testing.T) {
	t.Parallel()
	stub := &stubSonarr{releases: []release.Release{
		{
			GUID: "g1", Title: "Pack", QualityID: 19, QualityName: "WEBDL-2160p",
			IndexerName: "RT", MappedEpisodeNumbers: []int{1, 2, 3, 4, 5},
			CustomFormatScore: 500, Rejections: []string{"Full season pack"},
		},
	}}
	uc := NewUseCase(stub, &recDecisions{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := uc.Execute(context.Background(), Input{
		ScanRunID: uuid.New(), Instance: "x",
		Series:       series.Series{ID: 1, Title: "S", Type: series.SeriesTypeStandard, Monitored: true},
		Season:       makeSeason([]int{4, 5}, []int{1, 2, 3}),
		Profile:      ports.QualityProfile{Items: []ports.QualityItem{{ID: 19, Order: 9}}},
		Sonarr:       stub,
		DryRun:       true,
		Now:          time.Now().UTC(),
		ExcludeGUIDs: map[string]struct{}{"g1": {}},
	})
	require.NoError(t, err)
	assert.Equal(t, decision.OutcomeSkip, d.Outcome)
	assert.Equal(t, decision.ReasonSkipNoCandidates, d.Reason)
}

func TestExecute_SkipSpecials(t *testing.T) {
	t.Parallel()
	uc := NewUseCase(&stubSonarr{}, &recDecisions{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := uc.Execute(context.Background(), Input{
		ScanRunID:    uuid.New(),
		Instance:     "x",
		Series:       series.Series{ID: 1, Monitored: true, Type: series.SeriesTypeStandard},
		Season:       series.Season{Number: 0, Monitored: true},
		SkipSpecials: true,
	})
	require.NoError(t, err)
	assert.Equal(t, decision.OutcomeSkip, d.Outcome)
	assert.Equal(t, decision.ReasonSkipSpecials, d.Reason)
}

func TestExecute_SkipAnime(t *testing.T) {
	t.Parallel()
	uc := NewUseCase(&stubSonarr{}, &recDecisions{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	d, err := uc.Execute(context.Background(), Input{
		ScanRunID: uuid.New(),
		Instance:  "x",
		Series:    series.Series{ID: 1, Monitored: true, Type: series.SeriesTypeAnime},
		Season:    series.Season{Number: 1, Monitored: true},
		SkipAnime: true,
	})
	require.NoError(t, err)
	assert.Equal(t, decision.OutcomeSkip, d.Outcome)
	assert.Equal(t, decision.ReasonSkipAnime, d.Reason)
}

// errSonarr returns a fixed error from SearchReleases. All other
// methods inherit the no-op stubSonarr behaviour (embedded).
type errSonarr struct {
	stubSonarr
	err error
}

func (e *errSonarr) SearchReleases(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]release.Release, error) {
	return nil, e.err
}

func TestExecute_PopulatesErrorDetailOnSearchFailure(t *testing.T) {
	t.Parallel()
	errStub := &errSonarr{err: errInfra("sonarr: 503 service unavailable")}
	rec := &recDecisions{}
	uc := NewUseCase(errStub, rec, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	_, err := uc.Execute(context.Background(), Input{
		ScanRunID: uuid.New(), Instance: "alpha",
		Series: series.Series{ID: 1, Title: "Severance", Monitored: true},
		Season: series.Season{Number: 1, Monitored: true,
			Episodes: []series.Episode{
				{Number: 1, Monitored: true, HasFile: true, QualityID: 19},
				{Number: 2, Monitored: true, HasFile: false},
			}},
		Now: time.Now(),
	})
	require.Error(t, err)
	require.Len(t, rec.list, 1)
	saved := rec.list[0]
	assert.Equal(t, decision.OutcomeError, saved.Outcome)
	assert.Equal(t, decision.ReasonErrorFetchReleases, saved.Reason)
	assert.Equal(t, "sonarr: 503 service unavailable", saved.ErrorDetail)
}

// errInfra is a typed-string error so the assertion above sees exactly
// the err.Error() output (no wrapper noise).
type errInfra string

func (e errInfra) Error() string { return string(e) }

type recCooldowns struct{ called atomic.Int32 }

func (r *recCooldowns) Set(context.Context, cooldown.Cooldown) error { return nil }
func (r *recCooldowns) Get(context.Context, cooldown.Scope, string) (cooldown.Cooldown, bool, error) {
	return cooldown.Cooldown{}, false, nil
}
func (r *recCooldowns) FilterActive(_ context.Context, _ cooldown.Scope, _ []string, _ time.Time) ([]cooldown.Cooldown, error) {
	r.called.Add(1)
	return nil, nil
}
func (r *recCooldowns) Sweep(context.Context, time.Time) (int64, error) { return 0, nil }

func makeInput(cd ports.CooldownRepository, ignore bool) Input {
	return Input{
		ScanRunID: uuid.New(), Instance: "alpha",
		Series: series.Series{ID: 1, Title: "Severance", Monitored: true},
		Season: series.Season{Number: 1, Monitored: true,
			Episodes: []series.Episode{
				{Number: 1, Monitored: true, HasFile: true, QualityID: 19},
				{Number: 2, Monitored: true, HasFile: false},
			}},
		Now: time.Now(), Cooldowns: cd, IgnoreCooldown: ignore,
	}
}

func TestExecute_IgnoreCooldown_BypassesGUIDCooldownLookup(t *testing.T) {
	t.Parallel()
	rec := &recDecisions{}
	cd := &recCooldowns{}
	stub := &stubSonarr{releases: []release.Release{{GUID: "g1", Title: "rel"}}}
	uc := NewUseCase(stub, rec, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	_, err := uc.Execute(context.Background(), makeInput(cd, true))
	require.NoError(t, err)
	assert.EqualValues(t, 0, cd.called.Load(),
		"IgnoreCooldown=true must skip FilterActive entirely")
}

func TestExecute_IgnoreCooldownFalse_CallsGUIDCooldownLookup(t *testing.T) {
	t.Parallel()
	rec := &recDecisions{}
	cd := &recCooldowns{}
	stub := &stubSonarr{releases: []release.Release{{GUID: "g1", Title: "rel"}}}
	uc := NewUseCase(stub, rec, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	_, err := uc.Execute(context.Background(), makeInput(cd, false))
	require.NoError(t, err)
	assert.EqualValues(t, 1, cd.called.Load(),
		"default IgnoreCooldown=false must call FilterActive once")
}
