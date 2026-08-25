package commands

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

type fakeBackfillTMDB struct {
	byID  map[int64]*tmdb.CollectionResponse
	calls int
}

func (f *fakeBackfillTMDB) GetCollection(_ context.Context, id int64, _ string) (*tmdb.CollectionResponse, error) {
	f.calls++
	return f.byID[id], nil
}

func seedBackfillCollection(t *testing.T, db *gorm.DB, tmdbID int) {
	t.Helper()
	require.NoError(t, enrichpersistence.NewMovieCollectionsRepository(db).
		UpsertCollection(context.Background(), movie.CollectionCanon{
			TMDBCollectionID: tmdbID, Name: "Dune Collection",
		}))
}

func TestRunBackfillCollectionI18n_IdempotentAndGuards(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedBackfillCollection(t, db, 726871) // has ru
			seedBackfillCollection(t, db, 111)    // no ru translation
			ctx := context.Background()
			log := newDiscardLogger()

			fetcher := &fakeBackfillTMDB{byID: map[int64]*tmdb.CollectionResponse{
				726871: {
					ID: 726871, Name: "Dune Collection", Overview: "Epic saga.",
					Translations: &tmdb.MovieTranslations{Translations: []tmdb.MovieTranslation{
						{ISO6391: "en", Data: tmdb.MovieTranslationData{Title: "Dune Collection", Overview: "Epic saga."}},
						{ISO6391: "ru", Data: tmdb.MovieTranslationData{Title: "Дюна: Коллекция", Overview: "Эпическая сага."}},
					}},
				},
				111: {ID: 111, Name: "Solo Franchise", Overview: "x"}, // no translations → only en-US
			}}

			// First run.
			res, err := runBackfillCollectionI18n(ctx, db, fetcher, false, 0, log)
			require.NoError(t, err)
			assert.Equal(t, int64(2), res.Collections)
			assert.Equal(t, int64(3), res.RowsWritten, "726871: en+ru (2) ; 111: en only (1)")

			// Second run → idempotent: same row count, no dup.
			_, err = runBackfillCollectionI18n(ctx, db, fetcher, false, 0, log)
			require.NoError(t, err)

			var total int64
			require.NoError(t, db.Table("collection_texts").Count(&total).Error)
			assert.Equal(t, int64(3), total, "no duplicate rows after re-run")

			// ru row landed for 726871 with the localized name.
			cid, err := enrichpersistence.NewCollectionTextsRepository(db).IDByTMDBCollectionID(ctx, 726871)
			require.NoError(t, err)
			var ruName *string
			require.NoError(t, db.Table("collection_texts").
				Select("name").Where("collection_id = ? AND language = ?", cid, "ru-RU").
				Scan(&ruName).Error)
			require.NotNil(t, ruName)
			assert.Equal(t, "Дюна: Коллекция", *ruName)
		})
	}
}

func TestRunBackfillCollectionI18n_DryRunWritesNothing(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedBackfillCollection(t, db, 726871)
			res, err := runBackfillCollectionI18n(context.Background(), db, nil, true, 0, newDiscardLogger())
			require.NoError(t, err)
			assert.Equal(t, int64(1), res.Collections)
			assert.Equal(t, int64(0), res.RowsWritten)
			var total int64
			require.NoError(t, db.Table("collection_texts").Count(&total).Error)
			assert.Zero(t, total)
		})
	}
}
