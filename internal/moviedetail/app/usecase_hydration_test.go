package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeStale struct {
	calls []domain.MovieID
	err   error
}

func (f *fakeStale) MarkStaleForReenrich(_ context.Context, id domain.MovieID, _ time.Time) error {
	f.calls = append(f.calls, id)
	return f.err
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestUseCase_Get_ColdMovieTriggersMarkOnce(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(693134)
	canon := movie.Canon{ID: domain.MovieID(42), TMDBID: &tid, Title: "Cold", Hydration: movie.HydrationFull}
	// every section stamp nil → probe all-stale
	stale := &fakeStale{}
	uc := New(
		fakeCanon{canon: canon},
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	).WithHydrationTrigger(stale, fixedClock(time.Now()), discardLog())

	d, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "Cold", d.Title)
	require.Len(t, stale.calls, 1, "cold movie marked exactly once")
	assert.Equal(t, domain.MovieID(42), stale.calls[0])
}

func TestUseCase_Get_FreshMovieNoMark(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(1)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-1 * time.Hour)
	canon := movie.Canon{
		ID: domain.MovieID(7), TMDBID: &tid, Title: "Fresh", Hydration: movie.HydrationFull,
		EnrichmentTextSyncedAt:     new(recent),
		EnrichmentCastSyncedAt:     new(recent),
		EnrichmentRecsSyncedAt:     new(recent),
		EnrichmentMediaSyncedAt:    new(recent),
		EnrichmentKeywordsSyncedAt: new(recent),
	}
	stale := &fakeStale{}
	uc := New(
		fakeCanon{canon: canon},
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	).WithHydrationTrigger(stale, fixedClock(now), discardLog())

	_, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Empty(t, stale.calls, "fully-fresh movie is never marked")
}

func TestUseCase_Get_MarkerErrorIsFailOpen(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(1)
	canon := movie.Canon{ID: domain.MovieID(9), TMDBID: &tid, Title: "Boom", Hydration: movie.HydrationFull}
	stale := &fakeStale{err: errors.New("db down")}
	uc := New(
		fakeCanon{canon: canon},
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	).WithHydrationTrigger(stale, fixedClock(time.Now()), discardLog())

	d, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err, "marker error must NOT fail the read")
	assert.Equal(t, "Boom", d.Title)
	require.Len(t, stale.calls, 1, "marker was attempted")
}

func TestUseCase_Get_NoTMDBIDNoMark(t *testing.T) {
	t.Parallel()
	// Radarr orphan: no tmdb_id, stub → probe would say stale, but the picker
	// can never re-enrich a tmdb-less movie, so the trigger must skip it.
	canon := movie.Canon{ID: domain.MovieID(11), TMDBID: nil, Title: "Orphan", Hydration: movie.HydrationStub}
	stale := &fakeStale{}
	uc := New(
		fakeCanon{canon: canon},
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	).WithHydrationTrigger(stale, fixedClock(time.Now()), discardLog())

	_, err := uc.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.NoError(t, err)
	assert.Empty(t, stale.calls, "tmdb-less movie is never marked")
}

func TestUseCase_Get_NoTriggerWiredIsNoop(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(1)
	canon := movie.Canon{ID: domain.MovieID(13), TMDBID: &tid, Title: "Plain", Hydration: movie.HydrationStub}
	// New WITHOUT WithHydrationTrigger — trigger must be a silent no-op.
	uc := New(
		fakeCanon{canon: canon},
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	)

	d, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "Plain", d.Title)
}
