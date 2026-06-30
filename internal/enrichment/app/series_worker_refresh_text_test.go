package enrichment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// ---- fakes specific to the A2 narrow methods -------------------------

// fakeProbe records the IsStale call args and returns canned verdicts.
// A single err is returned if set (covers fail-open behaviour).
type fakeProbe struct {
	verdicts []freshener.SectionVerdict
	err      error
	calls    int
}

func (f *fakeProbe) IsStale(_ context.Context, _ domain.SeriesID, _ values.LanguageTag, _ []int) ([]freshener.SectionVerdict, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.verdicts, nil
}

// errorSeriesTextsRepo wraps fakeSeriesTextsRepo and triggers an
// injected error on Upsert. Used to assert tx rollback drops the stamp.
type errorSeriesTextsRepo struct {
	*fakeSeriesTextsRepo
	err error
}

func (e *errorSeriesTextsRepo) Upsert(ctx context.Context, t series.SeriesText) error {
	if e.err != nil {
		return e.err
	}
	return e.fakeSeriesTextsRepo.Upsert(ctx, t)
}

// newRefreshFixture builds a workerFixture pre-seeded for narrow refresh
// tests. canonTMDBID nil → seedCanon installs a Sonarr-only canon row
// (no tmdb_id). probe nil OK — Worker's Probe field stays nil.
func newRefreshFixture(t *testing.T, canonTMDBID *domain.TMDBID, probe freshener.Probe) *workerFixture {
	t.Helper()
	// minimalTV is the deterministic GetTV response. Seeded into the fake
	// even when canonTMDBID is nil — the worker short-circuits before
	// dialling so the response is unused there.
	f := newWorkerFixture(t, minimalTV(), map[int]*tmdb.SeasonResponse{})
	// Re-build worker with the optional Probe field set. NewSeriesWorker
	// keeps the rest of the deps identical.
	deps := f.worker.deps
	deps.Probe = probe
	w, err := NewSeriesWorker(deps)
	require.NoError(t, err)
	f.worker = w
	// Seed canon. id=1 keeps the existing test parity.
	f.seedCanon(1, canonTMDBID)
	return f
}

// ---- tests ----------------------------------------------------------

func TestSeriesWorker_RefreshSeriesText_NoTMDBID_Skip(t *testing.T) {
	t.Parallel()
	f := newRefreshFixture(t, nil, nil)
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Zero(t, f.tmdb.getTVHit, "TMDB MUST NOT be called when canon.TMDBID is nil")
	assert.Empty(t, f.seriesTexts.rows, "series_texts MUST stay empty when canon.TMDBID is nil")
	assert.False(t, hasCall(f.rec.list(), "Series.MarkTextSynced"), "stamp MUST NOT be written")
}

func TestSeriesWorker_RefreshSeriesText_SeriesMissing_Skip(t *testing.T) {
	t.Parallel()
	f := newRefreshFixture(t, nil, nil)
	// Wipe the seeded canon so Series.Get returns SeriesNotFoundError.
	delete(f.series.rows, 1)
	// fakeSeriesRepo.Get returns ports.ErrNotFound — we need a
	// SeriesNotFoundError for the worker's errors.As branch. Wrap the
	// fake's Get via a tiny adapter.
	f.worker.deps.Series = &seriesNotFoundRepo{inner: f.series}
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Zero(t, f.tmdb.getTVHit)
	assert.Empty(t, f.seriesTexts.rows)
}

func TestSeriesWorker_RefreshSeriesText_InvalidLang_Error(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRefreshFixture(t, &tmdbID, nil)
	err := f.worker.RefreshSeriesText(context.Background(), 1, "not-a-lang", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid lang")
	assert.Zero(t, f.tmdb.getTVHit, "invalid lang MUST short-circuit before TMDB")
}

func TestSeriesWorker_RefreshSeriesText_ForceTrue_NoProbe_HappyPath(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRefreshFixture(t, &tmdbID, nil)
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit, "exactly 1 GetTV call")
	require.Len(t, f.seriesTexts.rows, 1, "exactly 1 series_texts row")
	row := f.seriesTexts.rows[0]
	assert.Equal(t, "ru-RU", row.Language)
	require.NotNil(t, row.Title)
	assert.Equal(t, "Show", *row.Title)
	assert.True(t, hasCall(f.rec.list(), "Series.MarkTextSynced"), "stamp MUST fire on success")
	// Verify the canon was stamped.
	persisted := f.series.rows[1]
	require.NotNil(t, persisted.EnrichmentTextSyncedAt)
}

func TestSeriesWorker_RefreshSeriesText_ForceFalse_NoProbe_HappyPath(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRefreshFixture(t, &tmdbID, nil)
	// force=false + Probe nil → fetch unconditionally.
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit, "Probe nil → unconditional fetch")
	require.NotNil(t, f.series.rows[1].EnrichmentTextSyncedAt)
}

func TestSeriesWorker_RefreshSeriesText_ForceFalse_ProbeFresh_Skip(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{verdicts: []freshener.SectionVerdict{
		{Section: freshener.SectionOverview, Stale: false, Reason: "fresh"},
	}}
	f := newRefreshFixture(t, &tmdbID, probe)
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Equal(t, 1, probe.calls, "Probe MUST be consulted")
	assert.Zero(t, f.tmdb.getTVHit, "Probe fresh + force=false → skip TMDB")
	assert.Empty(t, f.seriesTexts.rows)
}

func TestSeriesWorker_RefreshSeriesText_ForceTrue_ProbeFresh_Bypass(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{verdicts: []freshener.SectionVerdict{
		{Section: freshener.SectionOverview, Stale: false, Reason: "fresh"},
	}}
	f := newRefreshFixture(t, &tmdbID, probe)
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err)
	assert.Zero(t, probe.calls, "force=true MUST NOT consult Probe")
	assert.Equal(t, 1, f.tmdb.getTVHit, "force=true bypasses gate")
}

func TestSeriesWorker_RefreshSeriesText_ProbeError_FailOpen(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{err: errors.New("probe boom")}
	f := newRefreshFixture(t, &tmdbID, probe)
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err, "probe error → fail-open (no error propagated)")
	assert.Equal(t, 1, probe.calls)
	assert.Equal(t, 1, f.tmdb.getTVHit, "fail-open → still fetches")
	require.NotNil(t, f.series.rows[1].EnrichmentTextSyncedAt)
}

func TestSeriesWorker_RefreshSeriesText_OneLangOnly(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRefreshFixture(t, &tmdbID, nil)
	require.NoError(t, f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true))
	require.Equal(t, 1, f.tmdb.getTVHit, "exactly 1 GetTV call")
	require.Len(t, f.tmdb.getTVLangs, 1)
	assert.Equal(t, "ru-RU", f.tmdb.getTVLangs[0])
}

func TestSeriesWorker_RefreshSeriesText_TMDBError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRefreshFixture(t, &tmdbID, nil)
	f.tmdb.tvErr = errors.New("tmdb 500")
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true)
	require.Error(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.Empty(t, f.seriesTexts.rows, "tx never started")
	assert.False(t, hasCall(f.rec.list(), "Series.MarkTextSynced"), "stamp NOT written")
	assert.Nil(t, f.series.rows[1].EnrichmentTextSyncedAt)
}

func TestSeriesWorker_RefreshSeriesText_UpsertError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRefreshFixture(t, &tmdbID, nil)
	// Wrap the SeriesTexts repo to fail.
	f.worker.deps.SeriesTexts = &errorSeriesTextsRepo{
		fakeSeriesTextsRepo: f.seriesTexts,
		err:                 errors.New("upsert boom"),
	}
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true)
	require.Error(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkTextSynced"), "stamp NEVER written without successful UPSERT")
	assert.Nil(t, f.series.rows[1].EnrichmentTextSyncedAt)
}

// seriesNotFoundRepo wraps fakeSeriesRepo and returns
// SeriesNotFoundError on Get to exercise the errors.As branch in the
// worker. Other methods delegate to the inner fake.
type seriesNotFoundRepo struct {
	inner *fakeSeriesRepo
}

func (s *seriesNotFoundRepo) Get(_ context.Context, id domain.SeriesID) (series.Canon, error) {
	return series.Canon{}, &sharedErrors.SeriesNotFoundError{ID: id}
}

func (s *seriesNotFoundRepo) Upsert(ctx context.Context, c series.Canon) (domain.SeriesID, error) {
	return s.inner.Upsert(ctx, c)
}

func (s *seriesNotFoundRepo) UpsertStub(ctx context.Context, c series.Canon) (domain.SeriesID, error) {
	return s.inner.UpsertStub(ctx, c)
}

func (s *seriesNotFoundRepo) MarkTMDBSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return s.inner.MarkTMDBSynced(ctx, id, now)
}

func (s *seriesNotFoundRepo) MarkOMDBSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return s.inner.MarkOMDBSynced(ctx, id, now)
}

func (s *seriesNotFoundRepo) MarkTextSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return s.inner.MarkTextSynced(ctx, id, now)
}

func (s *seriesNotFoundRepo) MarkCastSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return s.inner.MarkCastSynced(ctx, id, now)
}

func hasCall(list []string, target string) bool {
	for _, c := range list {
		if c == target {
			return true
		}
	}
	return false
}
