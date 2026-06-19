package enrichment

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/enrichment"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/omdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// --- fakes for OMDb worker -----------------------------------------

type fakeOMDbSeries struct {
	canon   series.Canon
	getErr  error
	upserts []series.Canon
}

func (f *fakeOMDbSeries) Get(_ context.Context, id domain.SeriesID) (series.Canon, error) {
	if f.getErr != nil {
		return series.Canon{}, f.getErr
	}
	c := f.canon
	c.ID = id
	return c, nil
}

func (f *fakeOMDbSeries) Upsert(_ context.Context, c series.Canon) (domain.SeriesID, error) {
	f.upserts = append(f.upserts, c)
	if c.ID == 0 {
		return 1, nil
	}
	return c.ID, nil
}

// UpsertStub — Story 319: OMDb tests never exercise the recommendation
// stub path; a delegate to Upsert keeps the fake satisfying SeriesRepo.
func (f *fakeOMDbSeries) UpsertStub(ctx context.Context, c series.Canon) (domain.SeriesID, error) {
	return f.Upsert(ctx, c)
}

type fakeOMDbSyncLog struct {
	last    enrichment.SyncLog
	lastErr error
	upserts []enrichment.SyncLog
}

func (f *fakeOMDbSyncLog) Upsert(_ context.Context, e enrichment.SyncLog) error {
	f.upserts = append(f.upserts, e)
	return nil
}

func (f *fakeOMDbSyncLog) GetLastSync(_ context.Context, _ enrichment.EntityType, _ int64, _ enrichment.Source) (enrichment.SyncLog, error) {
	return f.last, f.lastErr
}

func (f *fakeOMDbSyncLog) StaleScan(_ context.Context, _ enrichment.Source, _ time.Time, _ int) ([]enrichment.SyncLog, error) {
	return nil, nil
}

func (f *fakeOMDbSyncLog) RetryDue(_ context.Context, _ enrichment.Source, _ time.Time, _ int) ([]enrichment.SyncLog, error) {
	return nil, nil
}

type fakeOMDbClient struct {
	resp  *omdb.Response
	err   error
	calls int
}

func (f *fakeOMDbClient) GetByIMDB(_ context.Context, _ domain.IMDBID) (*omdb.Response, error) {
	f.calls++
	return f.resp, f.err
}

type fakeOMDbBudget struct {
	allow     bool
	reserves  int
	remaining int
}

func (f *fakeOMDbBudget) Reserve() bool {
	f.reserves++
	return f.allow
}

func (f *fakeOMDbBudget) Remaining() int { return f.remaining }

type omdbWorkerFakes struct {
	series  *fakeOMDbSeries
	syncLog *fakeOMDbSyncLog
	client  *fakeOMDbClient
	budget  *fakeOMDbBudget
}

func imdbPtr(s string) *domain.IMDBID { v := domain.IMDBID(s); return &v }

func newOMDbWorkerForTest(t *testing.T, mut func(*OMDbWorkerDeps)) (*OMDbWorker, *omdbWorkerFakes) {
	t.Helper()
	f := &omdbWorkerFakes{
		series: &fakeOMDbSeries{canon: series.Canon{
			Title:     "Breaking Bad",
			Hydration: series.HydrationFull,
			IMDBID:    imdbPtr("tt0903747"),
		}},
		syncLog: &fakeOMDbSyncLog{},
		client: &fakeOMDbClient{resp: &omdb.Response{
			IMDBRating:   "9.5",
			IMDBVotes:    "2,034,123",
			Rated:        "TV-MA",
			Awards:       "Won 16 Primetime Emmys",
			ResponseFlag: "True",
		}},
		budget: &fakeOMDbBudget{allow: true, remaining: 899},
	}
	deps := OMDbWorkerDeps{
		Client:  func() OMDbClient { return f.client },
		Budget:  f.budget,
		Tx:      fakeTxr{},
		Series:  f.series,
		SyncLog: f.syncLog,
		Logger:  quietLogger(),
		Clock:   func() time.Time { return time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC) },
	}
	if mut != nil {
		mut(&deps)
	}
	w, err := NewOMDbWorker(deps)
	require.NoError(t, err)
	return w, f
}

// --- tests ----------------------------------------------------------

func TestOMDbWorker_HappyPath_PatchesFourFieldsOnly(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	// Pre-populate canon with non-OMDb fields that the worker must
	// not touch.
	tmdbRating := 8.0
	tmdbVotes := 100
	f.series.canon.TMDBRating = &tmdbRating
	f.series.canon.TMDBVotes = &tmdbVotes
	status := "Ended"
	f.series.canon.Status = &status

	require.NoError(t, w.Handle(context.Background(), 42))
	assert.Equal(t, 1, f.client.calls)
	require.Len(t, f.series.upserts, 1)

	upserted := f.series.upserts[0]
	// OMDb-owned columns patched.
	require.NotNil(t, upserted.IMDBRating)
	assert.InDelta(t, 9.5, *upserted.IMDBRating, 1e-9)
	require.NotNil(t, upserted.IMDBVotes)
	assert.Equal(t, 2034123, *upserted.IMDBVotes)
	require.NotNil(t, upserted.OMDBRated)
	assert.Equal(t, "TV-MA", *upserted.OMDBRated)
	require.NotNil(t, upserted.OMDBAwards)
	// Non-OMDb fields preserved.
	require.NotNil(t, upserted.TMDBRating)
	assert.InDelta(t, 8.0, *upserted.TMDBRating, 1e-9)
	require.NotNil(t, upserted.TMDBVotes)
	assert.Equal(t, 100, *upserted.TMDBVotes)
	require.NotNil(t, upserted.Status)
	assert.Equal(t, "Ended", *upserted.Status)
	assert.Equal(t, "Breaking Bad", upserted.Title)

	// Sync log written with outcome=ok.
	require.Len(t, f.syncLog.upserts, 1)
	assert.Equal(t, enrichment.OutcomeOK, f.syncLog.upserts[0].Outcome)
	assert.Equal(t, 0, f.syncLog.upserts[0].Attempts)
}

func TestOMDbWorker_NoIMDBID_TerminalNotFound(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.series.canon.IMDBID = nil

	require.NoError(t, w.Handle(context.Background(), 42))
	assert.Zero(t, f.client.calls, "no OMDb call without imdb_id")
	assert.Zero(t, f.budget.reserves, "no budget reservation")
	require.Len(t, f.syncLog.upserts, 1)
	assert.Equal(t, enrichment.OutcomeNotFound, f.syncLog.upserts[0].Outcome)
}

func TestOMDbWorker_BudgetExhausted_NoCallNoJournal(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.budget.allow = false
	f.budget.remaining = 0
	f.syncLog.lastErr = ports.ErrNotFound

	require.NoError(t, w.Handle(context.Background(), 42))
	assert.Zero(t, f.client.calls, "no upstream call when budget exhausted")
	assert.Empty(t, f.syncLog.upserts, "no sync_log write when budget exhausted")
	assert.Empty(t, f.series.upserts, "no series upsert when budget exhausted")
}

func TestOMDbWorker_NotFoundSentinel_JournalsTerminal(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.client.err = fmt.Errorf("%w: Movie not found!", omdb.ErrNotFound)
	f.client.resp = nil

	require.NoError(t, w.Handle(context.Background(), 42))
	require.Len(t, f.syncLog.upserts, 1)
	assert.Equal(t, enrichment.OutcomeNotFound, f.syncLog.upserts[0].Outcome)
	assert.Nil(t, f.syncLog.upserts[0].NextAttemptAt)
	assert.Empty(t, f.series.upserts)
}

func TestOMDbWorker_InvalidKey_JournalsAuthFailed(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.client.err = fmt.Errorf("%w: Invalid API key!", omdb.ErrInvalidKey)
	f.client.resp = nil

	require.NoError(t, w.Handle(context.Background(), 42))
	require.Len(t, f.syncLog.upserts, 1)
	assert.Equal(t, enrichment.OutcomeError, f.syncLog.upserts[0].Outcome)
	require.NotNil(t, f.syncLog.upserts[0].ErrorDetail)
	assert.Contains(t, *f.syncLog.upserts[0].ErrorDetail, "invalid api key")
}

func TestOMDbWorker_DailyLimit_JournalsAuthFailed(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.client.err = fmt.Errorf("%w: Daily limit reached!", omdb.ErrDailyLimit)
	f.client.resp = nil

	require.NoError(t, w.Handle(context.Background(), 42))
	require.Len(t, f.syncLog.upserts, 1)
	assert.Equal(t, enrichment.OutcomeError, f.syncLog.upserts[0].Outcome)
	require.NotNil(t, f.syncLog.upserts[0].ErrorDetail)
	assert.Contains(t, *f.syncLog.upserts[0].ErrorDetail, "daily limit")
}

func TestOMDbWorker_GenericError_BackoffSet(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.client.err = &omdb.APIError{Status: 500, Body: "boom"}
	f.client.resp = nil
	f.syncLog.last = enrichment.SyncLog{Attempts: 1}

	require.NoError(t, w.Handle(context.Background(), 42))
	require.Len(t, f.syncLog.upserts, 1)
	assert.Equal(t, enrichment.OutcomeError, f.syncLog.upserts[0].Outcome)
	assert.Equal(t, 2, f.syncLog.upserts[0].Attempts)
	require.NotNil(t, f.syncLog.upserts[0].NextAttemptAt)
}

func TestOMDbWorker_FreshSkip_TTLNotExpired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC)
	syncedAt := now.Add(-1 * time.Hour)
	w, f := newOMDbWorkerForTest(t, nil)
	f.syncLog.last = enrichment.SyncLog{
		Outcome:  enrichment.OutcomeOK,
		SyncedAt: &syncedAt,
	}

	require.NoError(t, w.Handle(context.Background(), 42))
	assert.Zero(t, f.client.calls)
	assert.Zero(t, f.budget.reserves)
	assert.Empty(t, f.syncLog.upserts)
}

func TestOMDbWorker_TerminalNotFoundEntry_SkipsManualEnqueue(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.syncLog.last = enrichment.SyncLog{Outcome: enrichment.OutcomeNotFound}

	require.NoError(t, w.Handle(context.Background(), 42))
	assert.Zero(t, f.client.calls)
	assert.Empty(t, f.syncLog.upserts)
}

func TestOMDbWorker_NAValues_ResultsInNullColumns(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.client.resp = &omdb.Response{
		IMDBRating:   "N/A",
		IMDBVotes:    "N/A",
		Rated:        "N/A",
		Awards:       "N/A",
		ResponseFlag: "True",
	}
	// Pre-populate canon to make sure the worker explicitly clears the
	// fields rather than passively preserving the stale value.
	r := 8.0
	v := 1
	rated := "PG"
	awards := "Some"
	f.series.canon.IMDBRating = &r
	f.series.canon.IMDBVotes = &v
	f.series.canon.OMDBRated = &rated
	f.series.canon.OMDBAwards = &awards

	require.NoError(t, w.Handle(context.Background(), 42))
	require.Len(t, f.series.upserts, 1)
	up := f.series.upserts[0]
	assert.Nil(t, up.IMDBRating)
	assert.Nil(t, up.IMDBVotes)
	assert.Nil(t, up.OMDBRated)
	assert.Nil(t, up.OMDBAwards)
}

// TestOMDbWorker_WritesOnlyFourColumns is the Critical Decision #3
// in-memory diff test. With an empty mapper output (all-nil
// Enrichment) the worker must touch ONLY the four OMDb-owned fields
// on the canon row; every other field stays byte-equal.
func TestOMDbWorker_WritesOnlyFourColumns(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(99)
	tvdbID := domain.TVDBID(100)
	imdb := domain.IMDBID("tt0903747")
	origTitle := "BB original"
	status := "Ended"
	year := 2008
	popularity := 80.5
	posterAsset := "p"
	backdropAsset := "b"
	tmdbRating := 8.5
	tmdbVotes := 1000

	base := series.Canon{
		ID:            42,
		TMDBID:        &tmdbID,
		TVDBID:        &tvdbID,
		IMDBID:        &imdb,
		Hydration:     series.HydrationFull,
		Title:         "Breaking Bad",
		OriginalTitle: &origTitle,
		Status:        &status,
		Year:          &year,
		Popularity:    &popularity,
		InProduction:  false,
		PosterAsset:   &posterAsset,
		BackdropAsset: &backdropAsset,
		TMDBRating:    &tmdbRating,
		TMDBVotes:     &tmdbVotes,
	}

	// Empty Enrichment — mapper output for a response of all "N/A".
	patched := applyOMDbToCanon(base, omdb.Enrichment{})

	// Four OMDb-owned columns cleared to nil.
	assert.Nil(t, patched.IMDBRating)
	assert.Nil(t, patched.IMDBVotes)
	assert.Nil(t, patched.OMDBRated)
	assert.Nil(t, patched.OMDBAwards)

	// Every other field byte-equal — re-clear them on `base` for
	// fair comparison, then diff.
	base.IMDBRating = nil
	base.IMDBVotes = nil
	base.OMDBRated = nil
	base.OMDBAwards = nil
	assert.Equal(t, base, patched, "non-OMDb fields must be unchanged")
}

// TestOMDbWorker_LoadSeriesNotFound_NoCallsNoJournal verifies the
// dispatcher-stale-id path: when the dispatcher hands the worker an
// id whose series row was deleted between enqueue and dequeue, we log
// a warn + return nil — NOT crash, NOT call OMDb, NOT write sync_log.
func TestOMDbWorker_LoadSeriesNotFound_NoCallsNoJournal(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.series.getErr = ports.ErrNotFound

	require.NoError(t, w.Handle(context.Background(), 999))
	assert.Zero(t, f.client.calls)
	assert.Empty(t, f.syncLog.upserts)
}

// TestOMDbWorker_LoadSeriesErrorOther_Propagates verifies non-ErrNotFound
// errors from Series.Get propagate as the worker's return error.
func TestOMDbWorker_LoadSeriesErrorOther_Propagates(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.series.getErr = errors.New("db down")

	err := w.Handle(context.Background(), 42)
	require.Error(t, err)
}
