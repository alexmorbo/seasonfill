package enrichment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// --- fakes ----------------------------------------------------------

type fakePeopleWrite struct {
	person     people.Person
	getErr     error
	upserts    []people.Person
	upsertErr  error
	markSynced []int64
}

func (f *fakePeopleWrite) Get(_ context.Context, id int64, _ string) (people.Person, error) {
	if f.getErr != nil {
		return people.Person{}, f.getErr
	}
	p := f.person
	p.ID = id
	return p, nil
}

func (f *fakePeopleWrite) Upsert(_ context.Context, p people.Person) (int64, error) {
	if f.upsertErr != nil {
		return 0, f.upsertErr
	}
	f.upserts = append(f.upserts, p)
	return p.ID, nil
}

// MarkSynced — 464b: records the (id, now) tuple so tests can assert
// the canon stamp ran exactly once on a happy path.
func (f *fakePeopleWrite) MarkSynced(_ context.Context, id int64, now time.Time) error {
	f.markSynced = append(f.markSynced, id)
	// Mirror the in-memory canon so subsequent Get calls see the stamp.
	t := now
	f.person.EnrichmentSyncedAt = &t
	return nil
}

type fakeBiographies struct {
	rows []people.PersonBiography
}

func (f *fakeBiographies) Upsert(_ context.Context, b people.PersonBiography) error {
	f.rows = append(f.rows, b)
	return nil
}

type fakeCredits struct {
	batches [][]people.PersonCredit
	err     error
}

func (f *fakeCredits) BatchUpsert(_ context.Context, c []people.PersonCredit) ([]int64, error) {
	if f.err != nil {
		return nil, f.err
	}
	cp := make([]people.PersonCredit, len(c))
	copy(cp, c)
	f.batches = append(f.batches, cp)
	ids := make([]int64, len(c))
	return ids, nil
}

type fakePersonExternalIDs struct {
	rows []struct {
		Provider string
		Value    string
	}
}

func (f *fakePersonExternalIDs) Upsert(_ context.Context, _ enrichment.EntityType, _ int64, provider, value string) error {
	f.rows = append(f.rows, struct {
		Provider string
		Value    string
	}{provider, value})
	return nil
}

// fakePersonErrorRepo — person-side EnrichmentErrorRepo fake.
type fakePersonErrorRepo struct {
	preexist *enrichment.EnrichmentError
	failures []enrichment.EnrichmentError
	cleared  []clearedKey
}

func (f *fakePersonErrorRepo) RecordFailure(_ context.Context, e enrichment.EnrichmentError) error {
	f.failures = append(f.failures, e)
	return nil
}

func (f *fakePersonErrorRepo) ClearOnSuccess(_ context.Context, et enrichment.EntityType, id int64, src enrichment.Source) error {
	f.cleared = append(f.cleared, clearedKey{EntityType: et, EntityID: id, Source: src})
	return nil
}

func (f *fakePersonErrorRepo) GetForEntity(_ context.Context, _ enrichment.EntityType, _ int64) ([]enrichment.EnrichmentError, error) {
	return nil, nil
}

func (f *fakePersonErrorRepo) ListDueForRetry(_ context.Context, _ enrichment.Source, _ time.Time, _ int) ([]enrichment.EnrichmentError, error) {
	return nil, nil
}

func (f *fakePersonErrorRepo) GetByEntitySource(_ context.Context, et enrichment.EntityType, id int64, src enrichment.Source) (enrichment.EnrichmentError, error) {
	if f.preexist != nil && f.preexist.EntityType == et && f.preexist.EntityID == id && f.preexist.Source == src {
		return *f.preexist, nil
	}
	return enrichment.EnrichmentError{}, ports.ErrNotFound
}

type fakeTxr struct{}

func (fakeTxr) Transaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type fakeTMDBPerson struct {
	person *tmdb.PersonResponse
	err    error
	calls  int
}

func (f *fakeTMDBPerson) GetTV(context.Context, int64, string) (*tmdb.TVResponse, error) {
	return nil, nil
}

func (f *fakeTMDBPerson) GetSeason(context.Context, int64, int, string) (*tmdb.SeasonResponse, error) {
	return nil, nil
}

func (f *fakeTMDBPerson) FindByTVDB(context.Context, domain.TVDBID) (*tmdb.FindResponse, error) {
	return nil, nil
}

func (f *fakeTMDBPerson) GetPerson(_ context.Context, _ int64, _ string) (*tmdb.PersonResponse, error) {
	f.calls++
	return f.person, f.err
}

// --- helpers --------------------------------------------------------

func tmdbIDPtr(v int) *domain.TMDBID {
	id := domain.TMDBID(v)
	return &id
}

type personWorkerFakes struct {
	people           *fakePeopleWrite
	biographies      *fakeBiographies
	credits          *fakeCredits
	externalIDs      *fakePersonExternalIDs
	enrichmentErrors *fakePersonErrorRepo
	tmdb             *fakeTMDBPerson
}

func newPersonWorkerForTest(t *testing.T, mut func(*PersonWorkerDeps)) (*PersonWorker, *personWorkerFakes) {
	t.Helper()
	f := &personWorkerFakes{
		people: &fakePeopleWrite{
			person: people.Person{
				TMDBID:    tmdbIDPtr(99),
				Hydration: people.HydrationStub,
				Name:      "stub",
			},
		},
		biographies:      &fakeBiographies{},
		credits:          &fakeCredits{},
		externalIDs:      &fakePersonExternalIDs{},
		enrichmentErrors: &fakePersonErrorRepo{},
		tmdb: &fakeTMDBPerson{person: &tmdb.PersonResponse{
			ID:        99,
			Name:      "Pedro Pascal",
			Biography: "An actor.",
			IMDBID:    "nm0050959",
			Homepage:  "https://example.com",
			TVCredits: &tmdb.PersonTVCredits{Cast: []tmdb.PersonTVCredit{
				{ID: 1, CreditID: "c1", Name: "The Mandalorian", EpisodeCount: 16},
			}},
		}},
	}
	deps := PersonWorkerDeps{
		TMDB:              f.tmdb,
		Tx:                fakeTxr{},
		Language:          "en-US",
		People:            f.people,
		PersonBiographies: f.biographies,
		PersonCredits:     f.credits,
		ExternalIDs:       f.externalIDs,
		EnrichmentErrors:  f.enrichmentErrors,
		Logger:            quietLogger(),
		Clock:             func() time.Time { return time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC) },
	}
	if mut != nil {
		mut(&deps)
	}
	w, err := NewPersonWorker(deps)
	require.NoError(t, err)
	return w, f
}

// --- tests ----------------------------------------------------------

func TestPersonWorker_StubToFull_HappyPath(t *testing.T) {
	t.Parallel()
	w, f := newPersonWorkerForTest(t, nil)
	require.NoError(t, w.Handle(context.Background(), 42))

	assert.Equal(t, 1, f.tmdb.calls, "single TMDB round-trip")
	require.Len(t, f.people.upserts, 1)
	assert.Equal(t, people.HydrationFull, f.people.upserts[0].Hydration)
	assert.Equal(t, int64(42), f.people.upserts[0].ID)

	require.Len(t, f.biographies.rows, 1, "biography written (non-empty)")
	assert.Equal(t, "en-US", f.biographies.rows[0].Language)
	require.NotNil(t, f.biographies.rows[0].Biography)
	assert.Equal(t, "An actor.", *f.biographies.rows[0].Biography)

	require.Len(t, f.credits.batches, 1, "single credits batch")
	require.Equal(t, 1, len(f.credits.batches[0]))
	assert.Equal(t, int64(42), f.credits.batches[0][0].PersonID)

	require.GreaterOrEqual(t, len(f.externalIDs.rows), 2, "imdb + homepage at minimum")
	providers := map[string]bool{}
	for _, r := range f.externalIDs.rows {
		providers[r.Provider] = true
	}
	assert.True(t, providers["imdb"])
	assert.True(t, providers["homepage"])

	require.Len(t, f.people.markSynced, 1, "single MarkSynced call")
	assert.Equal(t, int64(42), f.people.markSynced[0])
	assert.Empty(t, f.enrichmentErrors.failures)
	require.NotEmpty(t, f.enrichmentErrors.cleared, "ClearOnSuccess must fire on happy path")
}

func TestPersonWorker_Idempotency_FreshFullSkips(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC)
	syncedAt := now.Add(-1 * time.Hour)
	w, f := newPersonWorkerForTest(t, nil)
	// Re-seed the people fake to return hydration=full + synced 1h ago.
	f.people.person.Hydration = people.HydrationFull
	f.people.person.EnrichmentSyncedAt = &syncedAt
	require.NoError(t, w.Handle(context.Background(), 42))

	assert.Zero(t, f.tmdb.calls, "no TMDB calls on fresh full")
	assert.Empty(t, f.credits.batches, "no credits written")
	assert.Empty(t, f.people.markSynced, "no MarkSynced on fresh-skip")
	_ = now
}

func TestPersonWorker_BatchCredits_ChunksAt500(t *testing.T) {
	t.Parallel()
	cast := make([]tmdb.PersonTVCredit, 600)
	for i := range cast {
		cast[i] = tmdb.PersonTVCredit{ID: int64(i + 1), CreditID: itoa(int64(i + 1)), Name: "x"}
	}
	w, f := newPersonWorkerForTest(t, nil)
	f.tmdb.person.TVCredits = &tmdb.PersonTVCredits{Cast: cast}

	require.NoError(t, w.Handle(context.Background(), 42))
	require.Len(t, f.credits.batches, 2, "600 rows → 2 batches")
	assert.Equal(t, 500, len(f.credits.batches[0]))
	assert.Equal(t, 100, len(f.credits.batches[1]))
}

func TestPersonWorker_TMDB404_TerminalNotFound(t *testing.T) {
	t.Parallel()
	w, f := newPersonWorkerForTest(t, nil)
	f.tmdb.err = &tmdb.APIError{Status: 404, Body: "Not Found"}
	f.tmdb.person = nil

	require.NoError(t, w.Handle(context.Background(), 42))
	assert.Empty(t, f.credits.batches)
	require.Len(t, f.enrichmentErrors.failures, 1)
	assert.Equal(t, terminalAttempts, f.enrichmentErrors.failures[0].Attempts)
}

func TestPersonWorker_TxFailure_NoHalfWrites(t *testing.T) {
	t.Parallel()
	w, f := newPersonWorkerForTest(t, nil)
	f.credits.err = errors.New("midway db failure")
	// The fake transactor propagates the closure's error verbatim,
	// which the worker observes and journals as outcome=error.
	require.NoError(t, w.Handle(context.Background(), 42))
	assert.Empty(t, f.credits.batches)
	require.NotEmpty(t, f.enrichmentErrors.failures)
	last := f.enrichmentErrors.failures[len(f.enrichmentErrors.failures)-1]
	assert.Equal(t, enrichment.SourceTMDBPerson, last.Source)
	require.NotNil(t, last.NextAttemptAt, "retryable error must have NextAttemptAt set")
}

func TestPersonWorker_PersonMissing_ReturnsNoOp(t *testing.T) {
	t.Parallel()
	w, f := newPersonWorkerForTest(t, nil)
	f.people.getErr = ports.ErrNotFound

	require.NoError(t, w.Handle(context.Background(), 42))
	assert.Zero(t, f.tmdb.calls)
	assert.Empty(t, f.credits.batches)
	assert.Empty(t, f.enrichmentErrors.failures)
}

func TestPersonWorker_NoTMDBID_TerminalNotFound(t *testing.T) {
	t.Parallel()
	w, f := newPersonWorkerForTest(t, nil)
	f.people.person.TMDBID = nil

	require.NoError(t, w.Handle(context.Background(), 42))
	assert.Zero(t, f.tmdb.calls)
	require.Len(t, f.enrichmentErrors.failures, 1)
	assert.Equal(t, terminalAttempts, f.enrichmentErrors.failures[0].Attempts)
}

func TestPersonWorker_RejectsMissingDeps(t *testing.T) {
	t.Parallel()
	_, err := NewPersonWorker(PersonWorkerDeps{})
	require.Error(t, err)

	_, err = NewPersonWorker(PersonWorkerDeps{TMDB: &fakeTMDBPerson{}})
	require.Error(t, err)

	_, err = NewPersonWorker(PersonWorkerDeps{TMDB: &fakeTMDBPerson{}, Tx: fakeTxr{}})
	require.Error(t, err, "missing repository ports should error")
}
