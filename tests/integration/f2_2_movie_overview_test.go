//go:build integration

// Story Ф2.2 — GET /movies/:tmdb_id/overview i18n read path against a real DB.
// Seeds a movie with a ru-RU title + empty ru-RU overview + a full en-US row and
// asserts the per-field ladder returns the ru-RU title with the en-US overview.
// Runs on SQLite + testcontainers Postgres via testhelpers.AllBackends.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestF22MovieOverview_I18nLadderReadPath(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			now := time.Now().UTC()
			tid := domain.TMDBID(603)

			movieRepo := enrichpersistence.NewMovieRepository(db)
			movieID, err := movieRepo.Upsert(ctx, movie.Canon{
				TMDBID: &tid, Hydration: movie.HydrationFull, Title: "Canon Title",
			})
			require.NoError(t, err)

			// Both rows carry a poster (ladder candidate invariant). ru-RU has an
			// EMPTY overview → NULL → the per-field ladder must fall to en-US.
			poster := "/p.jpg"
			seeder := enrichpersistence.NewMovieI18nSeeder(db)
			require.NoError(t, seeder.UpsertEnriched(ctx, movieID, "en-US", "The Matrix", "en overview", "en tagline", &poster, nil, now))
			require.NoError(t, seeder.UpsertEnriched(ctx, movieID, "ru-RU", "Матрица", "", "", &poster, nil, now))

			i18nRead := enrichpersistence.NewMovieI18nReadRepository(db)
			uc := mdapp.NewOverviewUseCase(movieRepo, i18nRead, i18nRead)

			page, err := uc.Get(ctx, tid, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, "Матрица", page.Title, "ru-RU title wins")
			require.NotNil(t, page.Overview)
			assert.Equal(t, "en overview", *page.Overview, "empty ru-RU overview falls back to en-US via the ladder")
			require.NotNil(t, page.Tagline)
			assert.Equal(t, "en tagline", *page.Tagline)
			assert.Equal(t, "ru-RU", page.ServedLanguage, "title resolved to ru-RU")
			assert.Empty(t, page.Degraded, "served==requested → no missing_lang")
		})
	}
}
