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
			require.NoError(t, repo.UpsertCollectionTexts(ctx, cid, "ru-RU", "Дюна: Коллекция", "Эпическая сага.", now))
			// 2. Language-poor rewrite: blank name + blank overview must NOT blank
			//    the richer stored values (COALESCE preserves).
			require.NoError(t, repo.UpsertCollectionTexts(ctx, cid, "ru-RU", "", "", now.Add(time.Minute)))
			// 3. A third identical write (idempotency) — still one row, same values.
			require.NoError(t, repo.UpsertCollectionTexts(ctx, cid, "ru-RU", "Дюна: Коллекция", "Эпическая сага.", now.Add(2*time.Minute)))

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
