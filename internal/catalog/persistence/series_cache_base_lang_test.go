package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// tmdbLessEntry builds a CacheEntry that mirrors the real W15-1 bug
// target (e.g. series 9 Silicon Valley): a Sonarr series with a stable
// TVDB natural key but NO TMDB id, so it never rides a TMDB enrichment
// pass and depends entirely on the base-lang seed to carry a title.
func tmdbLessEntry(instance domain.InstanceName, id domain.SonarrSeriesID, title string) series.CacheEntry {
	tvdb := domain.TVDBID(700000 + int(id))
	return series.CacheEntry{
		InstanceName:   instance,
		SonarrSeriesID: id,
		Title:          title,
		TitleSlug:      "tmdb-less-series",
		Year:           new(2014),
		TVDBID:         &tvdb,
		Status:         new("continuing"),
		Monitored:      true,
	}
}

// canonIDForCache reads the resolved series_id off the persisted cache row.
func canonIDForCache(t *testing.T, db *gorm.DB, instance domain.InstanceName, sonarrID domain.SonarrSeriesID) domain.SeriesID {
	t.Helper()
	var sc database.SeriesCacheModel
	require.NoError(t, db.Where(
		"instance_name = ? AND sonarr_series_id = ?", instance, sonarrID,
	).First(&sc).Error)
	require.NotNil(t, sc.SeriesID, "series_cache row must have a resolved series_id")
	return *sc.SeriesID
}

// seriesTextRows returns every series_texts row for the canon at the given
// language, so the tests can assert exact counts + titles.
func seriesTextRows(t *testing.T, db *gorm.DB, seriesID domain.SeriesID, lang string) []database.SeriesTextModel {
	t.Helper()
	var rows []database.SeriesTextModel
	require.NoError(t, db.Where("series_id = ? AND language = ?", seriesID, lang).Find(&rows).Error)
	return rows
}

// TestSeriesCacheRepository_Upsert_SeedsBaseLangText proves W15-1: the
// scan path (Upsert) seeds series_texts{en-US} from the Sonarr title for
// tmdb-less series, is only-if-absent (never clobbers a TMDB-authoritative
// row), and is idempotent across re-scans.
func TestSeriesCacheRepository_Upsert_SeedsBaseLangText(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db)).
				WithSeriesTexts(NewSeriesTextsRepository(db))

			t.Run("seeds en-US from sonarr title", func(t *testing.T) {
				const instance = domain.InstanceName("seed")
				const sonarrID = domain.SonarrSeriesID(9)
				require.NoError(t, repo.Upsert(ctx, tmdbLessEntry(instance, sonarrID, "Silicon Valley")))

				sid := canonIDForCache(t, db, instance, sonarrID)
				rows := seriesTextRows(t, db, sid, "en-US")
				require.Len(t, rows, 1, "exactly one en-US series_texts row must be seeded")
				require.NotNil(t, rows[0].Title)
				assert.Equal(t, "Silicon Valley", *rows[0].Title)
			})

			t.Run("only-if-absent does not clobber TMDB-authoritative row", func(t *testing.T) {
				const instance = domain.InstanceName("absent")
				const sonarrID = domain.SonarrSeriesID(21)
				// First Upsert resolves/creates the canon so we can lay a
				// pre-existing TMDB-authoritative en-US row on it.
				require.NoError(t, repo.Upsert(ctx, tmdbLessEntry(instance, sonarrID, "Sonarr Title")))
				sid := canonIDForCache(t, db, instance, sonarrID)

				tmdbTitle := "TMDB Authoritative Title"
				enriched := time.Now().UTC()
				texts := NewSeriesTextsRepository(db)
				require.NoError(t, texts.Upsert(ctx, series.SeriesText{
					SeriesID:   sid,
					Language:   "en-US",
					Title:      &tmdbTitle,
					EnrichedAt: &enriched,
					UpdatedAt:  enriched,
				}))

				// Re-scan with a DIFFERENT Sonarr title must NOT overwrite.
				require.NoError(t, repo.Upsert(ctx, tmdbLessEntry(instance, sonarrID, "Different Sonarr Title")))

				rows := seriesTextRows(t, db, sid, "en-US")
				require.Len(t, rows, 1, "must not duplicate the en-US row")
				require.NotNil(t, rows[0].Title)
				assert.Equal(t, tmdbTitle, *rows[0].Title, "TMDB-authoritative title must survive")
			})

			t.Run("re-scan idempotency keeps exactly one row", func(t *testing.T) {
				const instance = domain.InstanceName("idem")
				const sonarrID = domain.SonarrSeriesID(42)
				entry := tmdbLessEntry(instance, sonarrID, "Idempotent Show")
				require.NoError(t, repo.Upsert(ctx, entry))
				require.NoError(t, repo.Upsert(ctx, entry))

				sid := canonIDForCache(t, db, instance, sonarrID)
				rows := seriesTextRows(t, db, sid, "en-US")
				require.Len(t, rows, 1, "re-scan must not create a second en-US row")
				require.NotNil(t, rows[0].Title)
				assert.Equal(t, "Idempotent Show", *rows[0].Title)
			})
		})
	}
}
