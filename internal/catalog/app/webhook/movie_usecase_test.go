package webhook

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	domainwebhook "github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// stubStateAdapter routes scan.MovieStateUpserter.Upsert → the THIN
// MovieStatesRepository.UpsertStub (the webhook writer).
type stubStateAdapter struct {
	repo *catalogpersistence.MovieStatesRepository
}

func (a stubStateAdapter) Upsert(ctx context.Context, e movie.StateEntry) error {
	return a.repo.UpsertStub(ctx, e)
}

// TestMovieUseCase_MovieAdded_WritesBothTables — synthetic Radarr webhook:
// MovieAdded lands BOTH the movies canon stub AND the movie_states row (through
// the shared F-21 helper); MovieDelete soft-deletes the state row.
func TestMovieUseCase_MovieAdded_WritesBothTables(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := enrichpersistence.NewMovieRepository(db)
			states := catalogpersistence.NewMovieStatesRepository(db)
			uc := NewMovieUseCase(MovieDeps{
				Movies:      movies,
				States:      stubStateAdapter{states}, // THIN UpsertStub
				SoftDeleter: states,
			})

			evt := domainwebhook.MovieEvent{
				Type: domainwebhook.MovieEventTypeUpsert, InstanceName: "radarr-main",
				RadarrMovieID: 7, Title: "Dune", TitleSlug: "dune-2021", Year: 2021,
				TMDBID: 438631, IMDBID: "tt1160419", Monitored: true, HasFile: false,
				MinimumAvailability: "released", RawEventType: "MovieAdded",
			}
			require.NoError(t, uc.Process(ctx, evt))

			// movie_states row landed and is active.
			st, err := states.Get(ctx, "radarr-main", 7)
			require.NoError(t, err)
			require.NotZero(t, st.MovieID)
			assert.True(t, st.IsActive())
			assert.True(t, st.AddedToRadarr)
			require.NotNil(t, st.Availability)
			assert.Equal(t, "released", *st.Availability)

			// movies canon row landed (COALESCE-safe stub) with the tmdb_id.
			mv, err := movies.Get(ctx, st.MovieID)
			require.NoError(t, err)
			require.NotNil(t, mv.TMDBID)
			assert.Equal(t, domain.TMDBID(438631), *mv.TMDBID)

			// MovieDelete → soft-delete.
			require.NoError(t, uc.Process(ctx, domainwebhook.MovieEvent{
				Type: domainwebhook.MovieEventTypeDeleted, InstanceName: "radarr-main", RadarrMovieID: 7,
			}))
			st, err = states.Get(ctx, "radarr-main", 7)
			require.NoError(t, err)
			assert.False(t, st.IsActive(), "soft-deleted by webhook")
		})
	}
}

// TestMovieUseCase_TestPing_NoWrite — an Unsupported (Test/Health) event is a
// no-op: nothing is written to movie_states.
func TestMovieUseCase_TestPing_NoWrite(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			states := catalogpersistence.NewMovieStatesRepository(db)
			uc := NewMovieUseCase(MovieDeps{
				Movies:      enrichpersistence.NewMovieRepository(db),
				States:      stubStateAdapter{states},
				SoftDeleter: states,
			})

			require.NoError(t, uc.Process(ctx, domainwebhook.MovieEvent{
				Type: domainwebhook.MovieEventTypeUnsupported, InstanceName: "radarr-main",
				RadarrMovieID: 7, RawEventType: "Test",
			}))

			_, err := states.Get(ctx, "radarr-main", 7)
			require.ErrorIs(t, err, ports.ErrNotFound, "Test ping must not write a state row")
		})
	}
}

// TestMovieUseCase_WebhookDoesNotBlankEnrichedCanon — the prod COALESCE assert
// (mirror movie_repository_test.go:39): a TMDB-enriched movies row must survive
// a webhook stub write carrying nil enrichment columns.
func TestMovieUseCase_WebhookDoesNotBlankEnrichedCanon(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := enrichpersistence.NewMovieRepository(db)
			states := catalogpersistence.NewMovieStatesRepository(db)

			// Seed an enriched (full) canon.
			tmdb := domain.TMDBID(438631)
			rating := 8.1
			poster := "/p.jpg"
			status := "Released"
			enrichedID, err := movies.Upsert(ctx, movie.Canon{
				TMDBID:      &tmdb,
				Hydration:   movie.HydrationFull,
				Title:       "Dune",
				Status:      &status,
				PosterAsset: &poster,
				TMDBRating:  &rating,
			})
			require.NoError(t, err)
			require.NotZero(t, enrichedID)

			uc := NewMovieUseCase(MovieDeps{
				Movies:      movies,
				States:      stubStateAdapter{states},
				SoftDeleter: states,
			})
			// Webhook MovieAdded with the SAME tmdb_id, nil enrichment columns.
			require.NoError(t, uc.Process(ctx, domainwebhook.MovieEvent{
				Type: domainwebhook.MovieEventTypeUpsert, InstanceName: "radarr-main",
				RadarrMovieID: 7, Title: "Dune", TMDBID: 438631, Monitored: true,
				RawEventType: "MovieAdded",
			}))

			got, err := movies.Get(ctx, enrichedID)
			require.NoError(t, err)
			require.NotNil(t, got.TMDBRating)
			assert.InDelta(t, 8.1, *got.TMDBRating, 1e-9)
			require.NotNil(t, got.PosterAsset)
			assert.Equal(t, "/p.jpg", *got.PosterAsset)
			assert.Equal(t, movie.HydrationFull, got.Hydration, "hydration must stay full")
		})
	}
}

// TestMovieUseCase_TwoWriter_IdenticalStateRow — the F-21 persist-level proof:
// the SAME ports.RadarrMovie funnelled through the rich (sync) writer and the
// thin (webhook) writer produces byte-identical movie_states rows on a fresh
// INSERT (the two writers only diverge on a conflict UPDATE, where the thin
// writer preserves stats). Complements the pure-builder anti-drift test in
// internal/catalog/app/scan.
func TestMovieUseCase_TwoWriter_IdenticalStateRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := enrichpersistence.NewMovieRepository(db)
			states := catalogpersistence.NewMovieStatesRepository(db)

			m := ports.RadarrMovie{
				RadarrMovieID: 7, Title: "Dune", TitleSlug: "dune-2021", Year: 2021,
				TMDBID: 438631, IMDBID: "tt1160419", Monitored: true, HasFile: true,
				MinimumAvailability: "released", SizeOnDiskBytes: 5_000_000_000,
			}
			now := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)

			// Sync path (RICH repo writer) → instance "radarr-sync".
			syncCache := scan.BuildRadarrMovieCache("radarr-sync", m, now)
			_, err := scan.PersistRadarrMovieCache(ctx, movies, states, syncCache)
			require.NoError(t, err)

			// Webhook path (THIN stub adapter) → instance "radarr-webhook".
			webhookCache := scan.BuildRadarrMovieCache("radarr-webhook", m, now)
			_, err = scan.PersistRadarrMovieCache(ctx, movies, stubStateAdapter{states}, webhookCache)
			require.NoError(t, err)

			syncRow, err := states.Get(ctx, "radarr-sync", 7)
			require.NoError(t, err)
			webhookRow, err := states.Get(ctx, "radarr-webhook", 7)
			require.NoError(t, err)

			// Normalise the fields that legitimately differ (instance + the
			// repo-stamped UpdatedAt) before comparing.
			syncRow.InstanceName = ""
			webhookRow.InstanceName = ""
			syncRow.UpdatedAt = time.Time{}
			webhookRow.UpdatedAt = time.Time{}
			assert.Equal(t, syncRow, webhookRow, "rich and thin writers must land identical rows on fresh insert")
			// Both resolve to the SAME canon movie id (one real movie).
			assert.Equal(t, syncRow.MovieID, webhookRow.MovieID)
			require.NotNil(t, syncRow.Availability)
			assert.Equal(t, "released", *syncRow.Availability)
			assert.Equal(t, int64(5_000_000_000), syncRow.SizeOnDiskBytes)
		})
	}
}
