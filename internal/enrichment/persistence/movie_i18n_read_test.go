package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestMovieI18nRead_Get_Ladder(t *testing.T) {
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

			mkMovie := func(tmdbID int) domain.MovieID {
				tid := domain.TMDBID(tmdbID)
				id, err := movieRepo.Upsert(ctx, movie.Canon{TMDBID: &tid, Hydration: movie.HydrationStub, Title: "x"})
				require.NoError(t, err)
				return id
			}
			poster := "/p.jpg"

			t.Run("ru-RU present returns ru-RU row", func(t *testing.T) {
				id := mkMovie(1001)
				require.NoError(t, seeder.UpsertEnriched(ctx, id, "en-US", "English", "en ov", "", &poster, nil, now))
				require.NoError(t, seeder.UpsertEnriched(ctx, id, "ru-RU", "Русский", "ру ов", "", &poster, nil, now))
				row, err := reader.Get(ctx, id, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, row.Title)
				assert.Equal(t, "Русский", *row.Title)
			})

			t.Run("ru-RU absent falls back to en-US", func(t *testing.T) {
				id := mkMovie(1002)
				require.NoError(t, seeder.UpsertEnriched(ctx, id, "en-US", "English", "en ov", "", &poster, nil, now))
				row, err := reader.Get(ctx, id, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, row.Title)
				assert.Equal(t, "English", *row.Title)
			})

			t.Run("empty-poster requested row is skipped for a poster-bearing lang", func(t *testing.T) {
				id := mkMovie(1003)
				// ru-RU exists but with NO poster (canon-drop shape) → must be skipped.
				require.NoError(t, seeder.UpsertEnriched(ctx, id, "ru-RU", "РусскийБезПостера", "", "", nil, nil, now))
				require.NoError(t, seeder.UpsertEnriched(ctx, id, "en-US", "English", "", "", &poster, nil, now))
				row, err := reader.Get(ctx, id, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, row.Title)
				assert.Equal(t, "English", *row.Title, "empty-poster ru-RU row must not shadow the poster-bearing en-US row")
			})

			t.Run("no poster-bearing row anywhere returns ErrNotFound", func(t *testing.T) {
				id := mkMovie(1004)
				require.NoError(t, seeder.UpsertEnriched(ctx, id, "en-US", "NoPoster", "", "", nil, nil, now))
				_, err := reader.Get(ctx, id, "ru-RU")
				assert.ErrorIs(t, err, ports.ErrNotFound)
			})

			t.Run("poster-bearing ru-RU stub with EMPTY overview must NOT shadow en-US overview", func(t *testing.T) {
				id := mkMovie(1005)
				// en-US: full row (title + overview + tagline + poster).
				require.NoError(t, seeder.UpsertEnriched(ctx, id, "en-US", "English", "en overview", "en tagline", &poster, nil, now))
				// ru-RU STUB: title + poster present, overview/tagline EMPTY (→ SQL NULL).
				require.NoError(t, seeder.UpsertEnriched(ctx, id, "ru-RU", "Русский", "", "", &poster, nil, now))

				row, err := reader.Get(ctx, id, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, row.Title)
				assert.Equal(t, "Русский", *row.Title, "localized title still wins")
				require.NotNil(t, row.Overview, "overview must fall through to en-US, not the empty ru-RU stub")
				assert.Equal(t, "en overview", *row.Overview)
				require.NotNil(t, row.Tagline, "tagline must fall through to en-US per-field")
				assert.Equal(t, "en tagline", *row.Tagline)
			})

			t.Run("all-empty ru-RU except title: title=ru, overview=en, tagline=en", func(t *testing.T) {
				id := mkMovie(1006)
				require.NoError(t, seeder.UpsertEnriched(ctx, id, "en-US", "English", "en ov", "en tag", &poster, nil, now))
				require.NoError(t, seeder.UpsertEnriched(ctx, id, "ru-RU", "ТолькоЗаголовок", "", "", &poster, nil, now))

				row, err := reader.Get(ctx, id, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, row.Title)
				assert.Equal(t, "ТолькоЗаголовок", *row.Title)
				require.NotNil(t, row.Overview)
				assert.Equal(t, "en ov", *row.Overview)
				require.NotNil(t, row.Tagline)
				assert.Equal(t, "en tag", *row.Tagline)
			})
		})
	}
}

func TestMovieI18nRead_ListTitles_RuTitleWithEmptyOverview(t *testing.T) {
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

			tid := domain.TMDBID(2001)
			id, err := movieRepo.Upsert(ctx, movie.Canon{TMDBID: &tid, Hydration: movie.HydrationStub, Title: "canon"})
			require.NoError(t, err)
			poster := "/p.jpg"
			require.NoError(t, seeder.UpsertEnriched(ctx, id, "en-US", "English", "en ov", "", &poster, nil, now))
			// ru-RU stub: localized title present, overview empty.
			require.NoError(t, seeder.UpsertEnriched(ctx, id, "ru-RU", "Русский", "", "", &poster, nil, now))

			titles, err := reader.ListTitlesByTMDBIDsWithFallback(ctx, []int{2001}, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, "Русский", titles[2001], "list must show ru-RU title regardless of empty ru-RU overview")
		})
	}
}
