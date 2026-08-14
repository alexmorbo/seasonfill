package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestMovieI18nRead_TitleLanguage_D0 — Ф2.1 served-language signal. Verifies the
// requested → en-US → any ladder resolves the TITLE's source language, and the
// no-title case returns "".
func TestMovieI18nRead_TitleLanguage_D0(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			now := time.Now().UTC()

			movieRepo := NewMovieRepository(db)
			seeder := NewMovieI18nSeeder(db)
			reader := NewMovieI18nReadRepository(db)
			poster := "/p.jpg"

			mkMovie := func(tmdbID int) domain.MovieID {
				tid := domain.TMDBID(tmdbID)
				id, err := movieRepo.Upsert(ctx, movie.Canon{TMDBID: &tid, Hydration: movie.HydrationStub, Title: "x"})
				require.NoError(t, err)
				return id
			}

			t.Run("ru-RU present returns ru-RU", func(t *testing.T) {
				id := mkMovie(2001)
				require.NoError(t, seeder.UpsertEnriched(ctx, id, "en-US", "English", "", "", &poster, nil, now))
				require.NoError(t, seeder.UpsertEnriched(ctx, id, "ru-RU", "Русский", "", "", &poster, nil, now))
				got, err := reader.TitleLanguage(ctx, id, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "ru-RU", got)
			})

			t.Run("ru-RU absent falls back to en-US", func(t *testing.T) {
				id := mkMovie(2002)
				require.NoError(t, seeder.UpsertEnriched(ctx, id, "en-US", "English", "", "", &poster, nil, now))
				got, err := reader.TitleLanguage(ctx, id, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "en-US", got)
			})

			t.Run("no titled row returns empty", func(t *testing.T) {
				id := mkMovie(2003)
				got, err := reader.TitleLanguage(ctx, id, "ru-RU")
				require.NoError(t, err)
				assert.Empty(t, got)
			})
		})
	}
}
