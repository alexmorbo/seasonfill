package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestSeriesTextsRepository_InsertBaseLangIfAbsent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			seed := func(t *testing.T) (domain.SeriesID, *SeriesTextsRepository, *SeriesRepository) {
				t.Helper()
				gdb := backend.NewDB(t)
				sid, err := NewSeriesRepository(gdb).Upsert(ctx, sampleCanon("Base Lang Show"))
				require.NoError(t, err)
				return sid, NewSeriesTextsRepository(gdb), NewSeriesRepository(gdb)
			}

			t.Run("inserts when absent", func(t *testing.T) {
				t.Parallel()
				sid, repo, _ := seed(t)
				sonarrTitle := "Sonarr Latin Title"
				require.NoError(t, repo.InsertBaseLangIfAbsent(ctx, series.SeriesText{
					SeriesID: sid, Language: "en-US", Title: &sonarrTitle,
				}))
				got, err := repo.Get(ctx, sid, "en-US")
				require.NoError(t, err)
				require.NotNil(t, got.Title)
				assert.Equal(t, sonarrTitle, *got.Title)
			})

			t.Run("does NOT overwrite an existing TMDB row", func(t *testing.T) {
				t.Parallel()
				sid, repo, _ := seed(t)
				tmdbTitle := "TMDB Authoritative Title"
				tmdbOverview := "rich tmdb overview"
				// Simulate the TMDB worker having written the authoritative row.
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{
					SeriesID: sid, Language: "en-US",
					Title: &tmdbTitle, Overview: &tmdbOverview,
				}))
				// Sonarr sync tries to seed the gap — must be a no-op.
				sonarrTitle := "Sonarr Would-Clobber Title"
				require.NoError(t, repo.InsertBaseLangIfAbsent(ctx, series.SeriesText{
					SeriesID: sid, Language: "en-US", Title: &sonarrTitle,
				}))
				got, err := repo.Get(ctx, sid, "en-US")
				require.NoError(t, err)
				require.NotNil(t, got.Title)
				assert.Equal(t, tmdbTitle, *got.Title, "TMDB title must survive")
				require.NotNil(t, got.Overview)
				assert.Equal(t, tmdbOverview, *got.Overview, "TMDB overview must survive")
			})

			t.Run("idempotent re-run", func(t *testing.T) {
				t.Parallel()
				sid, repo, _ := seed(t)
				title := "Once"
				require.NoError(t, repo.InsertBaseLangIfAbsent(ctx, series.SeriesText{
					SeriesID: sid, Language: "en-US", Title: &title,
				}))
				// Second call with a different title is a no-op (row exists).
				title2 := "Twice"
				require.NoError(t, repo.InsertBaseLangIfAbsent(ctx, series.SeriesText{
					SeriesID: sid, Language: "en-US", Title: &title2,
				}))
				got, err := repo.Get(ctx, sid, "en-US")
				require.NoError(t, err)
				assert.Equal(t, "Once", *got.Title)
			})

			t.Run("validation error pairs", func(t *testing.T) {
				t.Parallel()
				_, repo, _ := seed(t)
				assert.Error(t, repo.InsertBaseLangIfAbsent(ctx, series.SeriesText{
					SeriesID: 0, Language: "en-US",
				}))
				assert.Error(t, repo.InsertBaseLangIfAbsent(ctx, series.SeriesText{
					SeriesID: 1, Language: "",
				}))
			})
		})
	}
}
