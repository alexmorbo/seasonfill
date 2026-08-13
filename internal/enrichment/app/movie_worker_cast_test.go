package enrichment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakePeopleUpsert struct {
	next     int64
	upserted []people.Person
}

func (f *fakePeopleUpsert) GetByTMDBID(context.Context, domain.TMDBID) (people.Person, error) {
	return people.Person{}, nil
}

func (f *fakePeopleUpsert) Upsert(_ context.Context, p people.Person) (int64, error) {
	f.next++
	f.upserted = append(f.upserted, p)
	return f.next, nil
}

type fakePersonCredits struct {
	authoritativeRows []people.PersonCredit
}

func (f *fakePersonCredits) BatchUpsert(context.Context, []people.PersonCredit) ([]int64, error) {
	return nil, nil
}

func (f *fakePersonCredits) BatchUpsertAuthoritative(_ context.Context, rows []people.PersonCredit) ([]int64, error) {
	f.authoritativeRows = append(f.authoritativeRows, rows...)
	ids := make([]int64, len(rows))
	return ids, nil
}

// passthroughTx runs fn with the same ctx (no real tx) — the movie worker's
// Transactor seam for unit tests.
type passthroughTx struct{ calls int }

func (t *passthroughTx) Transaction(ctx context.Context, fn func(context.Context) error) error {
	t.calls++
	return fn(ctx)
}

// TestMovieWorker_HandleForced_WritesCastAndStampsCast proves HandleForced upserts
// cast person stubs, writes person_credits authoritatively (carrying credit_order),
// and stamps enrichment_cast_synced_at — inside the Transactor.
func TestMovieWorker_HandleForced_WritesCastAndStampsCast(t *testing.T) {
	tmdbID := domain.TMDBID(42)
	canon := &fakeMovieCanon{getResp: movie.Canon{ID: 7, TMDBID: &tmdbID}}
	people0 := &fakePeopleUpsert{}
	pc := &fakePersonCredits{}
	tx := &passthroughTx{}

	worker, err := NewMovieWorker(MovieWorkerDeps{
		TMDB: &fakeMovieTMDB{resp: &tmdb.MovieResponse{
			ID:    42,
			Title: "Dune",
			Credits: &tmdb.MovieCredits{Cast: []tmdb.MovieCastMember{
				{ID: 100, Name: "Lead", CreditID: "c-lead", Order: 0},
				{ID: 200, Name: "Second", CreditID: "c-2", Order: 1},
			}},
		}},
		Movies:        canon,
		People:        people0,
		PersonCredits: pc,
		Tx:            tx,
		Clock:         func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)

	require.NoError(t, worker.HandleForced(context.Background(), 7))

	assert.Equal(t, 1, tx.calls, "cast write ran inside the Transactor")
	assert.Len(t, people0.upserted, 2, "two cast person stubs upserted")
	require.Len(t, pc.authoritativeRows, 2, "two person_credits rows written authoritatively")
	// PersonID resolved from the stub upsert (fake returns 1,2 in tmdb-id order).
	assert.NotZero(t, pc.authoritativeRows[0].PersonID)
	require.NotNil(t, pc.authoritativeRows[0].CreditOrder, "credit_order carried into the write")
	assert.Equal(t, 1, canon.castMarkCalls, "enrichment_cast_synced_at stamped once")
	assert.Equal(t, domain.MovieID(7), canon.castMarkedID)
}

// TestMovieWorker_HandleForced_NoCastDepsSkipsCast proves the pre-Ф1.1a path: when
// the cast deps are nil the worker hydrates canon WITHOUT touching person_credits.
func TestMovieWorker_HandleForced_NoCastDepsSkipsCast(t *testing.T) {
	tmdbID := domain.TMDBID(42)
	canon := &fakeMovieCanon{getResp: movie.Canon{ID: 7, TMDBID: &tmdbID}}
	worker, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:   &fakeMovieTMDB{resp: &tmdb.MovieResponse{ID: 42, Title: "Dune", Credits: &tmdb.MovieCredits{Cast: []tmdb.MovieCastMember{{ID: 1, CreditID: "c", Order: 0}}}}},
		Movies: canon,
		Clock:  func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)
	require.NoError(t, worker.HandleForced(context.Background(), 7))
	assert.Equal(t, 0, canon.castMarkCalls, "no cast stamp without cast deps")
}

// TestMovieWorker_HandleForced_EmptyCastStillStampsCast proves the Ф1.2b churn fix:
// a movie whose TMDB credits sub-resource is present but carries an EMPTY cast still
// stamps enrichment_cast_synced_at (stamp-only tx) — no person stubs, no
// person_credits, but the clock is bumped so the on-read hydration probe stops
// re-firing forever.
func TestMovieWorker_HandleForced_EmptyCastStillStampsCast(t *testing.T) {
	tmdbID := domain.TMDBID(42)
	canon := &fakeMovieCanon{getResp: movie.Canon{ID: 7, TMDBID: &tmdbID}}
	people0 := &fakePeopleUpsert{}
	pc := &fakePersonCredits{}
	tx := &passthroughTx{}

	worker, err := NewMovieWorker(MovieWorkerDeps{
		TMDB: &fakeMovieTMDB{resp: &tmdb.MovieResponse{
			ID:    42,
			Title: "Cast-less",
			// Credits present (gate resp.Credits != nil passes) but Cast empty —
			// the exact len(credits)==0 churn path.
			Credits: &tmdb.MovieCredits{Cast: []tmdb.MovieCastMember{}},
		}},
		Movies:        canon,
		People:        people0,
		PersonCredits: pc,
		Tx:            tx,
		Clock:         func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)

	require.NoError(t, worker.HandleForced(context.Background(), 7))

	assert.Equal(t, 1, tx.calls, "stamp-only tx still opened for an empty cast")
	assert.Empty(t, people0.upserted, "no person stubs for an empty cast")
	assert.Empty(t, pc.authoritativeRows, "no authoritative person_credits write for an empty cast")
	assert.Equal(t, 1, canon.castMarkCalls, "enrichment_cast_synced_at stamped once (checked-empty)")
	assert.Equal(t, domain.MovieID(7), canon.castMarkedID)
}
