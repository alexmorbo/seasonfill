package persistence

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// fakeTVDBFinder is the FindByTVDB test double. It records the call
// count and returns a fixed response/error so a test can assert the
// resolver's cooldown suppresses a second /find.
type fakeTVDBFinder struct {
	mu    sync.Mutex
	calls int
	resp  *tmdb.FindResponse
	err   error
}

func (f *fakeTVDBFinder) FindByTVDB(_ context.Context, _ domain.TVDBID) (*tmdb.FindResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.resp, f.err
}

func (f *fakeTVDBFinder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeDispatcher records Enqueue calls so a test can assert the resolver
// enqueued full enrichment for the resolved (or collided) series id.
type fakeDispatcher struct {
	mu    sync.Mutex
	calls []enqueued
}

type enqueued struct {
	kind appenrich.EntityKind
	id   int64
	prio appenrich.Priority
}

func (d *fakeDispatcher) Enqueue(kind appenrich.EntityKind, id int64, p appenrich.Priority) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, enqueued{kind: kind, id: id, prio: p})
}

func (d *fakeDispatcher) Close() {}

func (d *fakeDispatcher) recorded() []enqueued {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]enqueued, len(d.calls))
	copy(out, d.calls)
	return out
}

func TestTVDBResolver_Success_StampsTMDBIDAndEnqueues(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seriesRepo := NewSeriesRepository(db)
			errsRepo := NewEnrichmentErrorsRepository(db)
			ctx := context.Background()

			id, err := seriesRepo.Upsert(ctx, series.Canon{
				Hydration: series.HydrationStub,
				TVDBID:    ptrTVDBID(357276),
				// TMDBID nil — the tmdb-less Sonarr row W15-13 targets.
			})
			require.NoError(t, err)
			require.NotZero(t, id)

			finder := &fakeTVDBFinder{
				resp: &tmdb.FindResponse{TVResults: []tmdb.FindTVResult{{ID: 3512}}},
			}
			disp := &fakeDispatcher{}
			r := appenrich.NewTVDBResolver(seriesRepo, finder, errsRepo, disp, nil, 0, nil)

			require.NoError(t, r.ResolveMissingTMDBID(ctx, domain.TVDBID(357276)))

			got, err := seriesRepo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, got.TMDBID, "tmdb_id stamped")
			assert.Equal(t, domain.TMDBID(3512), *got.TMDBID)

			enq := disp.recorded()
			require.Len(t, enq, 1, "exactly one enqueue")
			assert.Equal(t, appenrich.EntitySeries, enq[0].kind)
			assert.Equal(t, int64(id), enq[0].id)
			assert.Equal(t, appenrich.PriorityCold, enq[0].prio)

			// No cooldown ledger row on the success path.
			_, gerr := errsRepo.GetByEntitySource(ctx,
				enrichment.EntityTypeSeries, int64(id), enrichment.SourceTVDBResolve)
			assert.True(t, errors.Is(gerr, ports.ErrNotFound), "no tvdb_resolve error row on success")
		})
	}
}

func TestTVDBResolver_NotFound_RecordsCooldownAndRespectsIt(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seriesRepo := NewSeriesRepository(db)
			errsRepo := NewEnrichmentErrorsRepository(db)
			ctx := context.Background()

			id, err := seriesRepo.Upsert(ctx, series.Canon{
				Hydration: series.HydrationStub,
				TVDBID:    ptrTVDBID(999999),
			})
			require.NoError(t, err)

			finder := &fakeTVDBFinder{
				resp: &tmdb.FindResponse{TVResults: nil}, // genuine not-found
			}
			disp := &fakeDispatcher{}
			fixed := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
			clock := func() time.Time { return fixed }
			r := appenrich.NewTVDBResolver(seriesRepo, finder, errsRepo, disp, clock, 0, nil)

			// First attempt → records a 7-day cooldown row, no enqueue.
			require.NoError(t, r.ResolveMissingTMDBID(ctx, domain.TVDBID(999999)))
			assert.Equal(t, 1, finder.callCount(), "FindByTVDB called once")
			assert.Empty(t, disp.recorded(), "no enqueue on not-found")

			row, gerr := errsRepo.GetByEntitySource(ctx,
				enrichment.EntityTypeSeries, int64(id), enrichment.SourceTVDBResolve)
			require.NoError(t, gerr, "cooldown row recorded")
			assert.Equal(t, enrichment.SourceTVDBResolve, row.Source)
			require.NotNil(t, row.NextAttemptAt)
			assert.WithinDuration(t, fixed.Add(7*24*time.Hour), *row.NextAttemptAt, time.Minute)

			// Second attempt within the cooldown window (clock unchanged) →
			// must NOT re-call /find and must NOT enqueue.
			require.NoError(t, r.ResolveMissingTMDBID(ctx, domain.TVDBID(999999)))
			assert.Equal(t, 1, finder.callCount(), "cooldown respected: no second FindByTVDB")
			assert.Empty(t, disp.recorded(), "still no enqueue")
		})
	}
}

func TestTVDBResolver_AlreadyHasTMDBID_NoOp(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seriesRepo := NewSeriesRepository(db)
			errsRepo := NewEnrichmentErrorsRepository(db)
			ctx := context.Background()

			id, err := seriesRepo.Upsert(ctx, series.Canon{
				Hydration: series.HydrationStub,
				TMDBID:    ptrTMDBID(5000),
				TVDBID:    ptrTVDBID(357276),
			})
			require.NoError(t, err)

			finder := &fakeTVDBFinder{
				resp: &tmdb.FindResponse{TVResults: []tmdb.FindTVResult{{ID: 3512}}},
			}
			disp := &fakeDispatcher{}
			r := appenrich.NewTVDBResolver(seriesRepo, finder, errsRepo, disp, nil, 0, nil)

			require.NoError(t, r.ResolveMissingTMDBID(ctx, domain.TVDBID(357276)))

			assert.Equal(t, 0, finder.callCount(), "FindByTVDB not called when canon already has tmdb_id")
			assert.Empty(t, disp.recorded(), "no enqueue")

			got, err := seriesRepo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, got.TMDBID)
			assert.Equal(t, domain.TMDBID(5000), *got.TMDBID, "tmdb_id untouched")
		})
	}
}

func TestTVDBResolver_Collision_EnqueuesExistingWithoutStamping(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seriesRepo := NewSeriesRepository(db)
			errsRepo := NewEnrichmentErrorsRepository(db)
			ctx := context.Background()

			// A: tmdb-less row we are resolving (tvdb only).
			idA, err := seriesRepo.Upsert(ctx, series.Canon{
				Hydration: series.HydrationStub,
				TVDBID:    ptrTVDBID(111),
			})
			require.NoError(t, err)

			// B: pre-existing row that already owns the resolved tmdb_id.
			idB, err := seriesRepo.Upsert(ctx, series.Canon{
				Hydration: series.HydrationStub,
				TMDBID:    ptrTMDBID(777),
			})
			require.NoError(t, err)
			require.NotEqual(t, idA, idB)

			finder := &fakeTVDBFinder{
				resp: &tmdb.FindResponse{TVResults: []tmdb.FindTVResult{{ID: 777}}},
			}
			disp := &fakeDispatcher{}
			r := appenrich.NewTVDBResolver(seriesRepo, finder, errsRepo, disp, nil, 0, nil)

			require.NoError(t, r.ResolveMissingTMDBID(ctx, domain.TVDBID(111)))

			// A must NOT get tmdb_id stamped (would violate the partial
			// unique index); B is enqueued instead.
			gotA, err := seriesRepo.Get(ctx, idA)
			require.NoError(t, err)
			assert.Nil(t, gotA.TMDBID, "collision: A.tmdb_id left NULL")

			enq := disp.recorded()
			require.Len(t, enq, 1)
			assert.Equal(t, appenrich.EntitySeries, enq[0].kind)
			assert.Equal(t, int64(idB), enq[0].id, "existing owner enqueued")
		})
	}
}
