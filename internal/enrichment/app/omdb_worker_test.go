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

type omdbColumnWrite struct {
	id     domain.SeriesID
	rating *float64
	votes  *int
	rated  *string
	awards *string
}

type fakeOMDbSeries struct {
	canon       series.Canon
	getErr      error
	markErr     error // F-12: force MarkOMDBSynced to fail (tx-abort path)
	upserts     []series.Canon
	omdbUpdates []omdbColumnWrite
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
// from the OMDb worker's success path. F-12: returns markErr when set so
// a test can prove the stamp is inside the success tx.
func (f *fakeOMDbSeries) MarkOMDBSynced(_ context.Context, id domain.SeriesID, now time.Time) error {
	if f.markErr != nil {
		return f.markErr
	}
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

// MarkSkeletonSynced — W18-16: no-op for OMDb tests.
func (f *fakeOMDbSeries) MarkSkeletonSynced(_ context.Context, _ domain.SeriesID, _ time.Time) error {
	return nil
}

// UpdateOMDbColumns — W18-6: records the owner-write the OMDb worker now
// issues instead of Upsert. Captures nil pointers verbatim so the N/A-clears
// contract is assertable.
func (f *fakeOMDbSeries) UpdateOMDbColumns(_ context.Context, id domain.SeriesID, rating *float64, votes *int, rated *string, awards *string) error {
	f.omdbUpdates = append(f.omdbUpdates, omdbColumnWrite{
		id: id, rating: rating, votes: votes, rated: rated, awards: awards,
	})
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
	allow         bool
	coldAvailable bool // W18-8: non-consuming Cold-availability pre-check
	reserves      int  // total across both lanes (existing assertions read this)
	hotCalls      int
	coldCalls     int
	remaining     int
}

func (f *fakeOMDbBudget) ReserveHot() bool {
	f.reserves++
	f.hotCalls++
	return f.allow
}

func (f *fakeOMDbBudget) ReserveCold() bool {
	f.reserves++
	f.coldCalls++
	return f.allow
}

func (f *fakeOMDbBudget) ColdAvailable() bool { return f.coldAvailable }

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

	require.NoError(t, w.HandleCold(context.Background(), 42))
	assert.Equal(t, 1, f.client.calls)
	require.Len(t, f.series.omdbUpdates, 1)

	up := f.series.omdbUpdates[0]
	assert.Equal(t, domain.SeriesID(42), up.id)
	// OMDb-owned columns written with the mapped values.
	require.NotNil(t, up.rating)
	assert.InDelta(t, 9.5, *up.rating, 1e-9)
	require.NotNil(t, up.votes)
	assert.Equal(t, 2034123, *up.votes)
	require.NotNil(t, up.rated)
	assert.Equal(t, "TV-MA", *up.rated)
	require.NotNil(t, up.awards)
	assert.Equal(t, "Won 16 Primetime Emmys", *up.awards)

	// Canon column stamped + no failure row recorded + ClearOnSuccess fired.
	require.NotNil(t, f.series.canon.EnrichmentOMDBSyncedAt, "canon enrichment_omdb_synced_at must be stamped on success")
	assert.Empty(t, f.enrichmentErrors.failures)
	assert.Equal(t, 1, f.enrichmentErrors.clearedRun, "ClearOnSuccess MUST fire on success")
}

func TestOMDbWorker_NoIMDBID_TerminalNotFound(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.series.canon.IMDBID = nil

	require.NoError(t, w.HandleCold(context.Background(), 42))
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

	require.NoError(t, w.HandleCold(context.Background(), 42))
	assert.Zero(t, f.client.calls, "no upstream call when budget exhausted")
	assert.Empty(t, f.enrichmentErrors.failures, "no failure row when budget exhausted")
	assert.Empty(t, f.series.omdbUpdates, "no series omdb write when budget exhausted")
}

func TestOMDbWorker_NotFoundSentinel_JournalsTerminal(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.client.err = fmt.Errorf("%w: Movie not found!", omdb.ErrNotFound)
	f.client.resp = nil

	require.NoError(t, w.HandleCold(context.Background(), 42))
	require.Len(t, f.enrichmentErrors.failures, 1)
	assert.Equal(t, terminalAttempts, f.enrichmentErrors.failures[0].Attempts)
	assert.Nil(t, f.enrichmentErrors.failures[0].NextAttemptAt)
	assert.Empty(t, f.series.omdbUpdates)
}

func TestOMDbWorker_InvalidKey_JournalsAuthFailed(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.client.err = fmt.Errorf("%w: Invalid API key!", omdb.ErrInvalidKey)
	f.client.resp = nil

	require.NoError(t, w.HandleCold(context.Background(), 42))
	require.Len(t, f.enrichmentErrors.failures, 1)
	assert.Contains(t, f.enrichmentErrors.failures[0].LastError, "invalid api key")
}

func TestOMDbWorker_DailyLimit_JournalsAuthFailed(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.client.err = fmt.Errorf("%w: Daily limit reached!", omdb.ErrDailyLimit)
	f.client.resp = nil

	require.NoError(t, w.HandleCold(context.Background(), 42))
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

	require.NoError(t, w.HandleCold(context.Background(), 42))
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

	require.NoError(t, w.HandleCold(context.Background(), 42))
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

	require.NoError(t, w.HandleCold(context.Background(), 42))
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

	require.NoError(t, w.HandleCold(context.Background(), 42))
	require.Len(t, f.series.omdbUpdates, 1)
	up := f.series.omdbUpdates[0]
	assert.Nil(t, up.rating)
	assert.Nil(t, up.votes)
	assert.Nil(t, up.rated)
	assert.Nil(t, up.awards)
}

// TestOMDbWorker_LoadSeriesNotFound_NoCallsNoJournal verifies the
// dispatcher-stale-id path: when the dispatcher hands the worker an
// id whose series row was deleted between enqueue and dequeue, we log
// a warn + return nil — NOT crash, NOT call OMDb, NOT write sync_log.
func TestOMDbWorker_LoadSeriesNotFound_NoCallsNoJournal(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.series.getErr = ports.ErrNotFound

	require.NoError(t, w.HandleCold(context.Background(), 999))
	assert.Zero(t, f.client.calls)
	assert.Empty(t, f.enrichmentErrors.failures)
}

// TestOMDbWorker_LoadSeriesErrorOther_Propagates verifies non-ErrNotFound
// errors from Series.Get propagate as the worker's return error.
func TestOMDbWorker_LoadSeriesErrorOther_Propagates(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.series.getErr = errors.New("db down")

	err := w.HandleCold(context.Background(), 42)
	require.Error(t, err)
}

func TestOMDbWorker_LaneRouting_HotVsCold(t *testing.T) {
	t.Parallel()

	// Hot path → ReserveHot.
	wHot, fHot := newOMDbWorkerForTest(t, nil)
	require.NoError(t, wHot.HandleHot(context.Background(), 42))
	assert.Equal(t, 1, fHot.budget.hotCalls, "HandleHot must draw the Hot lane")
	assert.Equal(t, 0, fHot.budget.coldCalls)

	// Cold path → ReserveCold.
	wCold, fCold := newOMDbWorkerForTest(t, nil)
	require.NoError(t, wCold.HandleCold(context.Background(), 42))
	assert.Equal(t, 1, fCold.budget.coldCalls, "HandleCold must draw the Cold lane")
	assert.Equal(t, 0, fCold.budget.hotCalls)
}

func TestClassifyOMDbKind(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)

	// helper to build a *time.Time offset from `now`.
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	yearsAgo := func(y int) *time.Time { v := now.AddDate(-y, 0, 0); return &v }

	cases := []struct {
		name         string
		inProduction bool
		status       *string
		lastAir      *time.Time
		firstAir     *time.Time
		want         enrichment.Kind
	}{
		{"in_production wins over old dates", true, new("Ended"), yearsAgo(20), yearsAgo(25), enrichment.KindOMDbInProduction},
		{"continuing status no flag", false, new("Returning Series"), yearsAgo(20), nil, enrichment.KindOMDbInProduction},
		{"continuing lowercase (sonarr)", false, new("continuing"), yearsAgo(20), nil, enrichment.KindOMDbInProduction},
		{"both dates nil -> mid", false, new("Ended"), nil, nil, enrichment.KindOMDbMid},
		{"nil status both dates nil -> mid", false, nil, nil, nil, enrichment.KindOMDbMid},
		{"recent <1y (6 months)", false, new("Ended"), at(-180 * 24 * time.Hour), nil, enrichment.KindOMDbRecent},
		{"fallback first_air when last nil", false, new("Ended"), nil, at(-180 * 24 * time.Hour), enrichment.KindOMDbRecent},
		{"boundary exactly 1y -> mid (older tier)", false, new("Ended"), yearsAgo(1), nil, enrichment.KindOMDbMid},
		{"mid 2y", false, new("Ended"), yearsAgo(2), nil, enrichment.KindOMDbMid},
		{"boundary exactly 3y -> old", false, new("Ended"), yearsAgo(3), nil, enrichment.KindOMDbOld},
		{"old 5y", false, new("Ended"), yearsAgo(5), nil, enrichment.KindOMDbOld},
		{"boundary exactly 8y -> ancient", false, new("Ended"), yearsAgo(8), nil, enrichment.KindOMDbAncient},
		{"ancient 12y", false, new("Ended"), yearsAgo(12), nil, enrichment.KindOMDbAncient},
		{"last_air preferred over first_air", false, new("Ended"), yearsAgo(2), yearsAgo(20), enrichment.KindOMDbMid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := series.Canon{
				InProduction: tc.inProduction,
				Status:       tc.status,
				LastAirDate:  tc.lastAir,
				FirstAirDate: tc.firstAir,
			}
			assert.Equal(t, tc.want, classifyOMDbKind(c, now))
		})
	}
}

func TestOMDbWorker_HandleWithPriority_RoutesLane(t *testing.T) {
	t.Parallel()

	// PriorityHot → Hot lane.
	wHot, fHot := newOMDbWorkerForTest(t, nil)
	require.NoError(t, wHot.HandleWithPriority(context.Background(), 42, PriorityHot))
	assert.Equal(t, 1, fHot.budget.hotCalls, "PriorityHot must draw the Hot lane")
	assert.Equal(t, 0, fHot.budget.coldCalls)

	// PriorityCold → Cold lane.
	wCold, fCold := newOMDbWorkerForTest(t, nil)
	require.NoError(t, wCold.HandleWithPriority(context.Background(), 42, PriorityCold))
	assert.Equal(t, 1, fCold.budget.coldCalls, "PriorityCold must draw the Cold lane")
	assert.Equal(t, 0, fCold.budget.hotCalls)
}

// F-04: on the Hot lane, a series whose enrichment_errors row has a FUTURE
// NextAttemptAt is in backoff — the worker must return WITHOUT reserving or
// fetching, so an OMDb outage cannot burn Hot reservations on every re-poll.
func TestOMDbWorker_HotLane_FutureBackoff_SkipsNoReserveNoFetch(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	future := time.Date(2026, 6, 13, 1, 0, 0, 0, time.UTC) // clock + 1h
	f.enrichmentErrors.preexist = &enrichment.EnrichmentError{
		EntityType:    enrichment.EntityTypeSeries,
		EntityID:      42,
		Source:        enrichment.SourceOMDb,
		Attempts:      2,
		NextAttemptAt: &future,
	}

	require.NoError(t, w.HandleHot(context.Background(), 42))
	assert.Zero(t, f.budget.reserves, "Hot backoff must not reserve budget")
	assert.Zero(t, f.budget.hotCalls, "Hot backoff must not touch the Hot lane")
	assert.Zero(t, f.client.calls, "Hot backoff must not call OMDb")
	assert.Empty(t, f.enrichmentErrors.failures, "Hot backoff writes no new failure row")
	assert.Empty(t, f.series.omdbUpdates)
}

// F-04 healthy path: a PAST NextAttemptAt (backoff elapsed) must still fetch
// on Hot — the guard must not block a series whose cooldown is over.
func TestOMDbWorker_HotLane_PastBackoff_StillFetches(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	past := time.Date(2026, 6, 12, 23, 0, 0, 0, time.UTC) // clock - 1h
	f.enrichmentErrors.preexist = &enrichment.EnrichmentError{
		EntityType:    enrichment.EntityTypeSeries,
		EntityID:      42,
		Source:        enrichment.SourceOMDb,
		Attempts:      2,
		NextAttemptAt: &past,
	}

	require.NoError(t, w.HandleHot(context.Background(), 42))
	assert.Equal(t, 1, f.budget.hotCalls, "elapsed backoff must still reserve Hot")
	assert.Equal(t, 1, f.client.calls, "elapsed backoff must still fetch")
	require.Len(t, f.series.omdbUpdates, 1)
}

// F-04 scope: the backoff guard is Hot-only. A Cold job with a FUTURE
// NextAttemptAt must be UNAFFECTED (Cold's backoff is enforced at the query
// layer, not in-band) — so it still reserves + fetches here.
func TestOMDbWorker_ColdLane_FutureBackoff_Unaffected(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	future := time.Date(2026, 6, 13, 1, 0, 0, 0, time.UTC)
	f.enrichmentErrors.preexist = &enrichment.EnrichmentError{
		EntityType:    enrichment.EntityTypeSeries,
		EntityID:      42,
		Source:        enrichment.SourceOMDb,
		Attempts:      2,
		NextAttemptAt: &future,
	}

	require.NoError(t, w.HandleCold(context.Background(), 42))
	assert.Equal(t, 1, f.budget.coldCalls, "Cold lane ignores in-band backoff")
	assert.Equal(t, 1, f.client.calls)
	require.Len(t, f.series.omdbUpdates, 1)
}

// F-12: MarkOMDBSynced is now folded INTO the success tx. A stamp failure must
// abort the success path — the worker records a retryable failure row, does NOT
// stamp enrichment_omdb_synced_at, and does NOT clear the error row. (Under the
// OLD post-tx best-effort stamp, a stamp failure was logged-only: the worker
// still declared success, fired ClearOnSuccess, and left the row unstamped =
// one wasted refetch. This test locks in the new atomic behaviour.)
//
// fakeTxr.Transaction has no rollback, so the fake still records the column
// write in omdbUpdates; in production the real GORM tx rolls back both together.
// The assertion is on the worker's decision: a stamp failure ⇒ NOT success.
func TestOMDbWorker_StampFailsInTx_NoSuccessJournal(t *testing.T) {
	t.Parallel()
	w, f := newOMDbWorkerForTest(t, nil)
	f.series.markErr = errors.New("stamp write failed")

	require.NoError(t, w.HandleCold(context.Background(), 42))

	// Success was NOT declared: no freshness stamp, error row NOT cleared.
	assert.Nil(t, f.series.canon.EnrichmentOMDBSyncedAt,
		"stamp failure inside the tx must leave the row unstamped")
	assert.Zero(t, f.enrichmentErrors.clearedRun,
		"ClearOnSuccess must NOT fire when the success tx failed")
	// A retryable failure row was recorded (tx error → generic backoff path).
	require.Len(t, f.enrichmentErrors.failures, 1)
	assert.Equal(t, 1, f.enrichmentErrors.failures[0].Attempts)
	require.NotNil(t, f.enrichmentErrors.failures[0].NextAttemptAt,
		"tx failure is retryable ⇒ NextAttemptAt set")
}
