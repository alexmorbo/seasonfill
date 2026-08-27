package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedCollection writes a collections row via the real COALESCE upsert and
// returns its resolved local PK.
func seedCollection(t *testing.T, db *gorm.DB, tmdbID int) int64 {
	t.Helper()
	cRepo := NewMovieCollectionsRepository(db)
	require.NoError(t, cRepo.UpsertCollection(context.Background(), movie.CollectionCanon{
		TMDBCollectionID: tmdbID, Name: "Dune Collection", Overview: new("Epic saga."),
	}))
	id, err := NewCollectionTextsRepository(db).IDByTMDBCollectionID(context.Background(), tmdbID)
	require.NoError(t, err)
	return id
}

func TestCollectionTextsRepository_IDByTMDB_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewCollectionTextsRepository(backend.NewDB(t))
			_, err := repo.IDByTMDBCollectionID(context.Background(), 999999)
			require.ErrorIs(t, err, ports.ErrNotFound)
		})
	}
}

func TestCollectionTextsRepository_UpsertCoalesceIdempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			cid := seedCollection(t, db, 726871)
			repo := NewCollectionTextsRepository(db)
			ctx := context.Background()
			now := time.Now().UTC()

			// 1. Full write.
			require.NoError(t, repo.UpsertCollectionTexts(ctx, cid, "ru-RU", "Дюна: Коллекция", "Эпическая сага.", nil, now))
			// 2. Language-poor rewrite: blank name + blank overview must NOT blank
			//    the richer stored values (COALESCE preserves).
			require.NoError(t, repo.UpsertCollectionTexts(ctx, cid, "ru-RU", "", "", nil, now.Add(time.Minute)))
			// 3. A third identical write (idempotency) — still one row, same values.
			require.NoError(t, repo.UpsertCollectionTexts(ctx, cid, "ru-RU", "Дюна: Коллекция", "Эпическая сага.", nil, now.Add(2*time.Minute)))

			type row struct {
				Name     *string
				Overview *string
			}
			var rows []row
			require.NoError(t, db.Table("collection_texts").
				Select("name", "overview").
				Where("collection_id = ? AND language = ?", cid, "ru-RU").
				Scan(&rows).Error)
			require.Len(t, rows, 1, "exactly one (collection_id, language) row — no dup")
			require.NotNil(t, rows[0].Name)
			assert.Equal(t, "Дюна: Коллекция", *rows[0].Name, "blank rewrite must not blank name")
			require.NotNil(t, rows[0].Overview)
			assert.Equal(t, "Эпическая сага.", *rows[0].Overview, "blank rewrite must not blank overview")
		})
	}
}

func TestCollectionTexts_PosterCoalesceAndLadder(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			cid := seedCollection(t, db, 726871) // canon poster_asset is NULL in this fixture
			repo := NewCollectionTextsRepository(db)
			collRepo := NewMovieCollectionsRepository(db)
			ctx := context.Background()
			now := time.Now().UTC()

			ru := "/ru_top.jpg"
			en := "/en_poster.jpg"
			require.NoError(t, repo.UpsertCollectionTexts(ctx, cid, "en-US", "Dune Collection", "Epic saga.", &en, now))
			require.NoError(t, repo.UpsertCollectionTexts(ctx, cid, "ru-RU", "Дюна: Коллекция", "Эпическая сага.", &ru, now))

			// COALESCE: a poster-less rewrite must NOT blank the stored ru poster.
			require.NoError(t, repo.UpsertCollectionTexts(ctx, cid, "ru-RU", "", "", nil, now.Add(time.Minute)))

			var stored *string
			require.NoError(t, db.Table("collection_texts").
				Select("poster_asset").Where("collection_id = ? AND language = ?", cid, "ru-RU").
				Scan(&stored).Error)
			require.NotNil(t, stored)
			assert.Equal(t, "/ru_top.jpg", *stored, "nil-poster rewrite must not blank a stored poster")

			// Display ladder: ru request → ru poster; unknown lang → en-US tier.
			ruCanon, err := collRepo.GetByTMDBCollectionIDLocalized(ctx, 726871, "ru-RU")
			require.NoError(t, err)
			require.NotNil(t, ruCanon.PosterAsset)
			assert.Equal(t, "/ru_top.jpg", *ruCanon.PosterAsset, "ru request resolves ru poster")

			deCanon, err := collRepo.GetByTMDBCollectionIDLocalized(ctx, 726871, "de-DE")
			require.NoError(t, err)
			require.NotNil(t, deCanon.PosterAsset)
			assert.Equal(t, "/en_poster.jpg", *deCanon.PosterAsset, "unknown lang → en-US tier")
		})
	}
}

// Canon fallback: no collection_texts poster at all → GetByTMDBCollectionIDLocalized
// returns collections.poster_asset.
func TestCollectionTexts_PosterCanonFallback(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			cRepo := NewMovieCollectionsRepository(db)
			ctx := context.Background()
			require.NoError(t, cRepo.UpsertCollection(ctx, movie.CollectionCanon{
				TMDBCollectionID: 726871, Name: "Dune Collection", PosterAsset: new("/canon.jpg"),
			}))
			got, err := cRepo.GetByTMDBCollectionIDLocalized(ctx, 726871, "ru-RU")
			require.NoError(t, err)
			require.NotNil(t, got.PosterAsset)
			assert.Equal(t, "/canon.jpg", *got.PosterAsset, "no localized poster → canon fallback")
		})
	}
}
