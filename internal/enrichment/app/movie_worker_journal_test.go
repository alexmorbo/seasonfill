package enrichment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	enrichdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// fakeMovieJournal is an EnrichmentErrorRepo double that captures writes and can
// be made to fail on each method independently — the journal must degrade to a
// WARN on every one of its own failures, never to a failed hydrate.
type fakeMovieJournal struct {
	records   []enrichdomain.EnrichmentError
	recordErr error

	cleared  []clearedJournalKey
	clearErr error

	getRow   enrichdomain.EnrichmentError
	getErr   error
	getCalls int
}

type clearedJournalKey struct {
	entityType enrichdomain.EntityType
	entityID   int64
	source     enrichdomain.Source
}

func (f *fakeMovieJournal) RecordFailure(_ context.Context, e enrichdomain.EnrichmentError) error {
	f.records = append(f.records, e)
	return f.recordErr
}

func (f *fakeMovieJournal) ClearOnSuccess(_ context.Context, et enrichdomain.EntityType, id int64, src enrichdomain.Source) error {
	f.cleared = append(f.cleared, clearedJournalKey{et, id, src})
	return f.clearErr
}

func (f *fakeMovieJournal) GetForEntity(context.Context, enrichdomain.EntityType, int64) ([]enrichdomain.EnrichmentError, error) {
	return nil, nil
}

func (f *fakeMovieJournal) ListDueForRetry(context.Context, enrichdomain.Source, time.Time, int) ([]enrichdomain.EnrichmentError, error) {
	return nil, nil
}

func (f *fakeMovieJournal) GetByEntitySource(_ context.Context, _ enrichdomain.EntityType, _ int64, _ enrichdomain.Source) (enrichdomain.EnrichmentError, error) {
	f.getCalls++
	if f.getErr != nil {
		return enrichdomain.EnrichmentError{}, f.getErr
	}
	return f.getRow, nil
}

var journalTestClock = time.Date(2026, 9, 11, 4, 0, 0, 0, time.UTC)

// newJournalWorker builds a MovieWorker whose only non-default deps are the TMDB
// double, the canon double and the journal double, on a frozen clock.
func newJournalWorker(t *testing.T, tmdbErr error, journal *fakeMovieJournal) (*MovieWorker, *fakeMovieCanon) {
	t.Helper()
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(42, 1750143)}
	deps := MovieWorkerDeps{
		TMDB:   &fakeMovieTMDB{err: tmdbErr},
		Movies: canon,
		Clock:  func() time.Time { return journalTestClock },
	}
	if journal != nil {
		deps.EnrichmentErrors = journal
	}
	w, err := NewMovieWorker(deps)
	require.NoError(t, err)
	return w, canon
}

// TestMovieWorker_TMDB404_JournalsTerminalRow is ADR-0025 Доказательство №2's
// direct fix: a TMDB-deleted movie must be parked FOREVER (attempts=99, no
// next_attempt_at) so neither the refresh picker nor the nightly retry sweep
// ever touches it again.
func TestMovieWorker_TMDB404_JournalsTerminalRow(t *testing.T) {
	t.Parallel()
	journal := &fakeMovieJournal{}
	w, canon := newJournalWorker(t, &tmdb.APIError{Status: 404, Body: "not found"}, journal)

	err := w.HandleForced(context.Background(), 42)

	// D-2: the error is journalled AND still returned.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetMovie")

	require.Len(t, journal.records, 1)
	rec := journal.records[0]
	assert.Equal(t, enrichdomain.EntityTypeMovie, rec.EntityType)
	assert.Equal(t, int64(42), rec.EntityID)
	assert.Equal(t, enrichdomain.SourceTMDBMovie, rec.Source)
	assert.Equal(t, terminalAttempts, rec.Attempts)
	assert.Nil(t, rec.NextAttemptAt, "a 404 must never be scheduled for retry")
	assert.NotEmpty(t, rec.LastError, "RecordFailure rejects an empty last_error")
	assert.Equal(t, journalTestClock, rec.LastSeenAt)

	// The hydrate itself is untouched: no canon write, no freshness stamp.
	assert.Equal(t, 0, canon.upsertCalls)
	assert.Equal(t, 0, canon.markCalls)
	assert.Empty(t, journal.cleared, "a failure must not clear the ledger")
}

// TestMovieWorker_RetryableError_JournalsBackoffRow covers the non-404 arm:
// previousAttempts+1 with the shared exponential backoff.
func TestMovieWorker_RetryableError_JournalsBackoffRow(t *testing.T) {
	t.Parallel()
	journal := &fakeMovieJournal{
		getRow: enrichdomain.EnrichmentError{Attempts: 2},
	}
	w, _ := newJournalWorker(t, &tmdb.APIError{Status: 503, Body: "upstream"}, journal)

	require.Error(t, w.HandleForced(context.Background(), 42))

	require.Len(t, journal.records, 1)
	rec := journal.records[0]
	assert.Equal(t, 3, rec.Attempts, "previousAttempts+1")
	require.NotNil(t, rec.NextAttemptAt, "a retryable failure MUST be re-schedulable")
	assert.Equal(t, enrichdomain.NextAttemptAt(3, journalTestClock), *rec.NextAttemptAt)
	assert.Equal(t, 1, journal.getCalls, "the attempts read is lazy: error path only")
}

// TestMovieWorker_NonAPIError_JournalsBackoffRow proves the 404 arm keys off the
// typed *tmdb.APIError and not off string matching: a plain error is retryable.
func TestMovieWorker_NonAPIError_JournalsBackoffRow(t *testing.T) {
	t.Parallel()
	journal := &fakeMovieJournal{}
	w, _ := newJournalWorker(t, errors.New("dial tcp: connection refused"), journal)

	require.Error(t, w.HandleForced(context.Background(), 42))

	require.Len(t, journal.records, 1)
	assert.Equal(t, 1, journal.records[0].Attempts)
	assert.NotNil(t, journal.records[0].NextAttemptAt)
}

// TestMovieWorker_RetryBudgetExhausted_Parks is the E-FIX-1 parity case (D-3).
// Without it a permanently-broken movie would be re-swept by the Ф1b nightly arm
// forever — ListDueForRetry has no attempts cap of its own.
func TestMovieWorker_RetryBudgetExhausted_Parks(t *testing.T) {
	t.Parallel()
	journal := &fakeMovieJournal{
		getRow: enrichdomain.EnrichmentError{Attempts: enrichdomain.MaxRetryAttempts - 1},
	}
	w, _ := newJournalWorker(t, errors.New("poisoned"), journal)

	require.Error(t, w.HandleForced(context.Background(), 42))

	require.Len(t, journal.records, 1)
	assert.Equal(t, enrichdomain.MaxRetryAttempts, journal.records[0].Attempts)
	assert.Nil(t, journal.records[0].NextAttemptAt,
		"at MaxRetryAttempts the row is PARKED terminally, not re-scheduled")
}

// TestMovieWorker_JustBelowBudget_StillRetries is the negative twin of the park
// case — the boundary must be exclusive on the low side.
func TestMovieWorker_JustBelowBudget_StillRetries(t *testing.T) {
	t.Parallel()
	journal := &fakeMovieJournal{
		getRow: enrichdomain.EnrichmentError{Attempts: enrichdomain.MaxRetryAttempts - 2},
	}
	w, _ := newJournalWorker(t, errors.New("transient"), journal)

	require.Error(t, w.HandleForced(context.Background(), 42))

	require.Len(t, journal.records, 1)
	assert.Equal(t, enrichdomain.MaxRetryAttempts-1, journal.records[0].Attempts)
	assert.NotNil(t, journal.records[0].NextAttemptAt)
}

// TestMovieWorker_AttemptsReadFailures_DegradeToZero is the NULL/error pair for
// the attempts lookup: ErrNotFound (no row yet) and a real read failure must BOTH
// fall back to 0 so the failure is still journalled.
func TestMovieWorker_AttemptsReadFailures_DegradeToZero(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		getErr error
	}{
		{"no row yet (ErrNotFound)", ports.ErrNotFound},
		{"read failed", errors.New("db is down")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			journal := &fakeMovieJournal{getErr: tc.getErr}
			w, _ := newJournalWorker(t, errors.New("boom"), journal)

			require.Error(t, w.HandleForced(context.Background(), 42))

			require.Len(t, journal.records, 1, "a read miss must never suppress the write")
			assert.Equal(t, 1, journal.records[0].Attempts)
		})
	}
}

// TestMovieWorker_RecordFailureError_IsWarnOnly — the journal's own write failure
// must not change what HandleForced returns.
func TestMovieWorker_RecordFailureError_IsWarnOnly(t *testing.T) {
	t.Parallel()
	journal := &fakeMovieJournal{recordErr: errors.New("unique violation")}
	w, _ := newJournalWorker(t, &tmdb.APIError{Status: 404}, journal)

	err := w.HandleForced(context.Background(), 42)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetMovie",
		"the caller still sees the TMDB error, not the journal error")
	assert.Len(t, journal.records, 1)
}

// TestMovieWorker_NilJournal_IsExactPreF1Behaviour pins the nil-OK contract (D-1):
// with no journal wired the worker must behave byte-for-byte as before Ф1.
func TestMovieWorker_NilJournal_IsExactPreF1Behaviour(t *testing.T) {
	t.Parallel()
	w, canon := newJournalWorker(t, &tmdb.APIError{Status: 404}, nil)

	require.NotPanics(t, func() {
		err := w.HandleForced(context.Background(), 42)
		require.Error(t, err)
	})
	assert.Equal(t, 0, canon.upsertCalls)
	assert.Equal(t, 0, canon.markCalls)
}

// TestMovieWorker_Success_ClearsJournal covers D-4. A journal without a clear is
// a permanent breaker: a recovered movie would keep its attempts>5 row and stay
// excluded by the Ф1b picker gate forever.
func TestMovieWorker_Success_ClearsJournal(t *testing.T) {
	t.Parallel()
	journal := &fakeMovieJournal{}
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 693134)}
	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB: &fakeMovieTMDB{resp: &tmdb.MovieResponse{
			ID: 693134, Title: "Dune: Part Two", Overview: "o", Status: "Released",
		}},
		Movies:           canon,
		EnrichmentErrors: journal,
		Clock:            func() time.Time { return journalTestClock },
	})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 7))

	assert.Empty(t, journal.records)
	require.Len(t, journal.cleared, 1)
	assert.Equal(t, clearedJournalKey{
		entityType: enrichdomain.EntityTypeMovie,
		entityID:   7,
		source:     enrichdomain.SourceTMDBMovie,
	}, journal.cleared[0])
	assert.Equal(t, 0, journal.getCalls, "the happy path must not read the ledger")
	assert.Equal(t, 1, canon.markCalls, "the hydrate still stamps freshness")
}

// TestMovieWorker_ClearOnSuccessError_IsWarnOnly — a clear miss must not fail an
// already-committed hydrate.
func TestMovieWorker_ClearOnSuccessError_IsWarnOnly(t *testing.T) {
	t.Parallel()
	journal := &fakeMovieJournal{clearErr: errors.New("db is down")}
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 693134)}
	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:             &fakeMovieTMDB{resp: &tmdb.MovieResponse{ID: 693134, Title: "x"}},
		Movies:           canon,
		EnrichmentErrors: journal,
		Clock:            func() time.Time { return journalTestClock },
	})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 7),
		"clear-on-success is best-effort; the hydrate already committed")
	assert.Len(t, journal.cleared, 1)
}
