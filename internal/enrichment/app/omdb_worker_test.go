package enrichment

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/omdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
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

// MarkTMDBSynced — 464b: no-op for OMDb tests (the OMDb worker only
// calls MarkOMDBSynced; this method is on the shared port).
func (f *fakeOMDbSeries) MarkTMDBSynced(_ context.Context, _ domain.SeriesID, _ time.Time) error {
	return nil
}

// MarkOMDBSynced — 464b: stamps the canon row's EnrichmentOMDBSyncedAt
// from the OMDb worker's success path.
func (f *fakeOMDbSeries) MarkOMDBSynced(_ context.Context, id domain.SeriesID, now time.Time) error {
	c := f.canon
	c.ID = id
	t := now
	c.EnrichmentOMDBSyncedAt = &t
	f.canon = c
	return nil
}

// MarkTextSynced — E-1 A2: no-op for OMDb tests (OMDb worker does not
// stamp this column; method is on the shared port).
func (f *fakeOMDbSeries) MarkTextSynced(_ context.Context, _ domain.SeriesID, _ time.Time) error {
	return nil
}

// MarkCastSynced — E-1 A2: no-op for OMDb tests.
func (f *fakeOMDbSeries) MarkCastSynced(_ context.Context, _ domain.SeriesID, _ time.Time) error {
	return nil
}

// MarkRecsSynced — E-1 A3b: no-op for OMDb tests.
func (f *fakeOMDbSeries) MarkRecsSynced(_ context.Context, _ domain.SeriesID, _ time.Time) error {
	return nil
}

// MarkMediaSynced — E-1 A4: no-op for OMDb tests.
func (f *fakeOMDbSeries) MarkMediaSynced(_ context.Context, _ domain.SeriesID, _ time.Time) error {
	return nil
}

// fakeOMDbErrorRepo is the OMDb-side EnrichmentErrorRepo fake.
// preexist seeds GetByEntitySource to exercise the retry-bump and
// terminal-skip paths.
type fakeOMDbErrorRepo struct {
	preexist   *enrichment.EnrichmentError
	getErr     error
	failures   []enrichment.EnrichmentError
	cleared    []clearedKey
	clearedRun int
}

func (f *fakeOMDbErrorRepo) RecordFailure(_ context.Context, e enrichment.EnrichmentError) error {
	f.failures = append(f.failures, e)
	return nil
}

func (f *fakeOMDbErrorRepo) ClearOnSuccess(_ context.Context, et enrichment.EntityType, id int64, src enrichment.Source) error {
	f.cleared = append(f.cleared, clearedKey{EntityType: et, EntityID: id, Source: src})
	f.clearedRun++
	return nil
}

func (f *fakeOMDbErrorRepo) GetForEntity(_ context.Context, _ enrichment.EntityType, _ int64) ([]enrichment.EnrichmentError, error) {
	return nil, nil
}

func (f *fakeOMDbErrorRepo) ListDueForRetry(_ context.Context, _ enrichment.Source, _ time.Time, _ int) ([]enrichment.EnrichmentError, error) {
	return nil, nil
}

func (f *fakeOMDbErrorRepo) GetByEntitySource(_ context.Context, et enrichment.EntityType, id int64, src enrichment.Source) (enrichment.EnrichmentError, error) {
	if f.getErr != nil {
		return enrichment.EnrichmentError{}, f.getErr
	}
	if f.preexist != nil && f.preexist.EntityType == et && f.preexist.EntityID == id && f.preexist.Source == src {
		return *f.preexist, nil
	}
	return enrichment.EnrichmentError{}, ports.ErrNotFound
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
	series           *fakeOMDbSeries
	enrichmentErrors *fakeOMDbErrorRepo
	client           *fakeOMDbClient
	budget           *fakeOMDbBudget
}

func imdbPtr(s string) *domain.IMDBID { v := domain.IMDBID(s); return &v }

func newOMDbWorkerForTest(t *testing.T, mut func(*OMDbWorkerDeps)) (*OMDbWorker, *omdbWorkerFakes) {
	t.Helper()
	f := &omdbWorkerFakes{
		series: &fakeOMDbSeries{canon: series.Canon{
			OriginalTitle: new("Breaking Bad"),
			Hydration:     series.HydrationFull,
			IMDBID:        imdbPtr("tt0903747"),
		}},
		enrichmentErrors: &fakeOMDbErrorRepo{},
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
		Client:           func() OMDbClient { return f.client },
		Budget:           f.budget,
		Tx:               fakeTxr{},
		Series:           f.series,
		EnrichmentErrors: f.enrichmentErrors,
		Logger:           quietLogger(),
		Clock:            func() time.Time { return time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC) },
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
	// Story 1039 — add the Ratings array to the default fixture response.
	f.client.resp.Ratings = []omdb.Rating{
		{Source: "Internet Movie Database", Value: "9.5/10"},
		{Source: "Rotten Tomatoes", Value: "96%"},
		{Source: "Metacritic", Value: "73/100"},
	}

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
	// S-E3a — canon carries original_title (a fact), no localizable Title.
	require.NotNil(t, upserted.OriginalTitle)
	assert.Equal(t, "Breaking Bad", *upserted.OriginalTitle)

	// Canon column stamped + no failure row recorded + ClearOnSuccess fired.
	require.NotNil(t, f.series.canon.EnrichmentOMDBSyncedAt, "canon enrichment_omdb_synced_at must be stamped on success")
	assert.Empty(t, f.enrichmentErrors.failures)
	assert.Equal(t, 1, f.enrichmentErrors.clearedRun, "ClearOnSuccess MUST fire on success")
}

func TestOMDbWorker_NoIMDBID_TerminalNotFound(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.series.canon.IMDBID = nil

	require.NoError(t, w.Handle(context.Background(), 42))
	assert.Zero(t, f.client.calls, "no OMDb call without imdb_id")
	assert.Zero(t, f.budget.reserves, "no budget reservation")
	require.Len(t, f.enrichmentErrors.failures, 1)
	assert.Equal(t, terminalAttempts, f.enrichmentErrors.failures[0].Attempts, "no imdb_id ⇒ terminal not_found")
}

func TestOMDbWorker_BudgetExhausted_NoCallNoJournal(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.budget.allow = false
	f.budget.remaining = 0

	require.NoError(t, w.Handle(context.Background(), 42))
	assert.Zero(t, f.client.calls, "no upstream call when budget exhausted")
	assert.Empty(t, f.enrichmentErrors.failures, "no failure row when budget exhausted")
	assert.Empty(t, f.series.upserts, "no series upsert when budget exhausted")
}

func TestOMDbWorker_NotFoundSentinel_JournalsTerminal(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.client.err = fmt.Errorf("%w: Movie not found!", omdb.ErrNotFound)
	f.client.resp = nil

	require.NoError(t, w.Handle(context.Background(), 42))
	require.Len(t, f.enrichmentErrors.failures, 1)
	assert.Equal(t, terminalAttempts, f.enrichmentErrors.failures[0].Attempts)
	assert.Nil(t, f.enrichmentErrors.failures[0].NextAttemptAt)
	assert.Empty(t, f.series.upserts)
}

func TestOMDbWorker_InvalidKey_JournalsAuthFailed(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.client.err = fmt.Errorf("%w: Invalid API key!", omdb.ErrInvalidKey)
	f.client.resp = nil

	require.NoError(t, w.Handle(context.Background(), 42))
	require.Len(t, f.enrichmentErrors.failures, 1)
	assert.Contains(t, f.enrichmentErrors.failures[0].LastError, "invalid api key")
}

func TestOMDbWorker_DailyLimit_JournalsAuthFailed(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.client.err = fmt.Errorf("%w: Daily limit reached!", omdb.ErrDailyLimit)
	f.client.resp = nil

	require.NoError(t, w.Handle(context.Background(), 42))
	require.Len(t, f.enrichmentErrors.failures, 1)
	assert.Contains(t, f.enrichmentErrors.failures[0].LastError, "daily limit")
}

func TestOMDbWorker_GenericError_BackoffSet(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.client.err = &omdb.APIError{Status: 500, Body: "boom"}
	f.client.resp = nil
	f.enrichmentErrors.preexist = &enrichment.EnrichmentError{
		EntityType: enrichment.EntityTypeSeries,
		EntityID:   42,
		Source:     enrichment.SourceOMDb,
		Attempts:   1,
	}

	require.NoError(t, w.Handle(context.Background(), 42))
	require.Len(t, f.enrichmentErrors.failures, 1)
	assert.Equal(t, 2, f.enrichmentErrors.failures[0].Attempts)
	require.NotNil(t, f.enrichmentErrors.failures[0].NextAttemptAt)
}

func TestOMDbWorker_FreshSkip_TTLNotExpired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC)
	syncedAt := now.Add(-1 * time.Hour)
	w, f := newOMDbWorkerForTest(t, nil)
	f.series.canon.EnrichmentOMDBSyncedAt = &syncedAt

	require.NoError(t, w.Handle(context.Background(), 42))
	assert.Zero(t, f.client.calls)
	assert.Zero(t, f.budget.reserves)
	assert.Empty(t, f.enrichmentErrors.failures)
}

func TestOMDbWorker_TerminalNotFoundEntry_SkipsManualEnqueue(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	// Pre-seed a terminalAttempts error row — should short-circuit
	// before the budget guard fires.
	f.enrichmentErrors.preexist = &enrichment.EnrichmentError{
		EntityType: enrichment.EntityTypeSeries,
		EntityID:   42,
		Source:     enrichment.SourceOMDb,
		Attempts:   terminalAttempts,
	}

	require.NoError(t, w.Handle(context.Background(), 42))
	assert.Zero(t, f.client.calls)
	assert.Empty(t, f.enrichmentErrors.failures)
}

func TestOMDbWorker_NAValues_ResultsInNullColumns(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.client.resp = &omdb.Response{
		IMDBRating: "N/A",
		IMDBVotes:  "N/A",
		Rated:      "N/A",
		Awards:     "N/A",
		Ratings: []omdb.Rating{
			{Source: "Rotten Tomatoes", Value: "N/A"},
			{Source: "Metacritic", Value: "N/A"},
		},
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
	tmdbRating := 8.5
	tmdbVotes := 1000

	base := series.Canon{
		ID:            42,
		TMDBID:        &tmdbID,
		TVDBID:        &tvdbID,
		IMDBID:        &imdb,
		Hydration:     series.HydrationFull,
		OriginalTitle: &origTitle,
		Status:        &status,
		Year:          &year,
		Popularity:    &popularity,
		InProduction:  false,
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
	assert.Empty(t, f.enrichmentErrors.failures)
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
