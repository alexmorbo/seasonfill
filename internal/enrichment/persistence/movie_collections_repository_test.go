package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedCollectionPart upserts a stub movie carrying collection_id via the real
// COALESCE MovieRepository.Upsert path (no raw-gorm shortcut).
func seedCollectionPart(t *testing.T, repo *MovieRepository, tmdbID, collectionID int, title string) domain.MovieID {
	t.Helper()
	id, err := repo.Upsert(context.Background(), movie.Canon{
		TMDBID:       new(domain.TMDBID(tmdbID)),
		Hydration:    movie.HydrationStub,
		Title:        title,
		CollectionID: new(collectionID),
	})
	require.NoError(t, err)
	return id
}

func TestMovieCollectionsRepository_UpsertCollection_CoalesceAndFlagPreserve(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieCollectionsRepository(db)
			ctx := context.Background()

			// 1. First enrichment write: full TMDB payload.
			require.NoError(t, repo.UpsertCollection(ctx, movie.CollectionCanon{
				TMDBCollectionID: 726871,
				Name:             "Dune Collection",
				Overview:         new("Epic saga."),
				PosterAsset:      new("/coll_p.jpg"),
				BackdropAsset:    new("/coll_b.jpg"),
			}))

			// 2. Operator/Radarr flip both flags out-of-band (simulates R-6 button /
			//    the radarr-monitor usecase).
			require.NoError(t, repo.SetRadarrMonitored(ctx, 726871, true))
			require.NoError(t, db.Table("collections").
				Where("tmdb_collection_id = ?", 726871).
				Update("monitored", true).Error)

			// 3. Second enrichment write with a language-poor payload: overview +
			//    backdrop nil. COALESCE must preserve them; flags must survive.
			require.NoError(t, repo.UpsertCollection(ctx, movie.CollectionCanon{
				TMDBCollectionID: 726871,
				Name:             "Dune Collection",
				Overview:         nil,
				PosterAsset:      new("/coll_p2.jpg"), // richer poster overwrites
				BackdropAsset:    nil,
			}))

			got, err := repo.GetByTMDBCollectionID(ctx, 726871)
			require.NoError(t, err)
			require.NotNil(t, got.Overview)
			assert.Equal(t, "Epic saga.", *got.Overview, "nil overview must not blank")
			require.NotNil(t, got.BackdropAsset)
			assert.Equal(t, "/coll_b.jpg", *got.BackdropAsset, "nil backdrop must not blank")
			require.NotNil(t, got.PosterAsset)
			assert.Equal(t, "/coll_p2.jpg", *got.PosterAsset, "non-nil poster overwrites")
			assert.True(t, got.Monitored, "operator monitored flag preserved")
			assert.True(t, got.RadarrMonitored, "radarr_monitored flag preserved")
		})
	}
}

func TestMovieCollectionsRepository_SetRadarrMonitored_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewMovieCollectionsRepository(backend.NewDB(t))
			err := repo.SetRadarrMonitored(context.Background(), 999999, true)
			require.Error(t, err)
		})
	}
}

func TestMovieCollectionsRepository_ListPartsWithMembership(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			movieRepo := NewMovieRepository(db)
			repo := NewMovieCollectionsRepository(db)
			ctx := context.Background()

			const cid = 726871
			p1 := seedCollectionPart(t, movieRepo, 438631, cid, "Dune")
			p2 := seedCollectionPart(t, movieRepo, 693134, cid, "Dune: Part Two")
			// A movie in a DIFFERENT collection must not leak into the projection.
			_ = seedCollectionPart(t, movieRepo, 155, 999, "The Dark Knight")

			// p1 is in library on instance "r1" (active); p2 is not.
			require.NoError(t, db.Create(&database.MovieStateModel{
				InstanceName: "r1", RadarrMovieID: 10, MovieID: p1,
				TitleSlug: "dune-438631", Monitored: true, HasFile: true,
				AddedToRadarr: true, UpdatedAt: time.Now().UTC(),
			}).Error)
			// A soft-deleted row for p2 must NOT count as in-library.
			deleted := time.Now().UTC()
			require.NoError(t, db.Create(&database.MovieStateModel{
				InstanceName: "r1", RadarrMovieID: 11, MovieID: p2,
				TitleSlug: "dune-two-693134", UpdatedAt: deleted, DeletedAt: &deleted,
			}).Error)

			rows, err := repo.ListPartsWithMembership(ctx, cid, "r1", "")
			require.NoError(t, err)
			require.Len(t, rows, 2, "only this collection's parts")

			byID := map[int64]bool{}
			for _, r := range rows {
				byID[r.MovieID] = r.InLibrary
			}
			assert.True(t, byID[int64(p1)], "p1 active membership → in library")
			assert.False(t, byID[int64(p2)], "p2 only soft-deleted → not in library")

			// tmdb id + title projected.
			assert.Equal(t, 438631, rows[0].TMDBID)
			assert.Equal(t, "Dune", rows[0].Title)

			// A different instance sees zero membership.
			rows2, err := repo.ListPartsWithMembership(ctx, cid, "r2", "")
			require.NoError(t, err)
			require.Len(t, rows2, 2)
			for _, r := range rows2 {
				assert.False(t, r.InLibrary, "instance r2 has no rows")
			}
		})
	}
}

func TestMovieCollectionsRepository_ListPartsWithMembership_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewMovieCollectionsRepository(backend.NewDB(t))
			rows, err := repo.ListPartsWithMembership(context.Background(), 111, "r1", "")
			require.NoError(t, err)
			assert.Nil(t, rows)
		})
	}
}

func TestMovieCollectionsRepository_ListPartsWithMembership_LocalizedTitleAndPoster(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			movieRepo := NewMovieRepository(db)
			repo := NewMovieCollectionsRepository(db)
			ctx := context.Background()

			const cid = 726871
			// p1: canon poster + a ru-RU localized title.
			p1, err := movieRepo.Upsert(ctx, movie.Canon{
				TMDBID:       new(domain.TMDBID(438631)),
				Hydration:    movie.HydrationStub,
				Title:        "Dune",
				Year:         new(2021),
				CollectionID: new(cid),
				PosterAsset:  new("/dune_p1.jpg"),
			})
			require.NoError(t, err)
			// p2: canon poster, NO localized row → canon title fallback.
			p2, err := movieRepo.Upsert(ctx, movie.Canon{
				TMDBID:       new(domain.TMDBID(693134)),
				Hydration:    movie.HydrationStub,
				Title:        "Dune: Part Two",
				CollectionID: new(cid),
				PosterAsset:  new("/dune_p2.jpg"),
			})
			require.NoError(t, err)

			require.NoError(t, db.Create(&database.MovieI18nModel{
				MovieID: p1, Language: "ru-RU", Title: new("Дюна"),
				UpdatedAt: time.Now().UTC(),
			}).Error)

			rows, err := repo.ListPartsWithMembership(ctx, cid, "r1", "ru-RU")
			require.NoError(t, err)
			require.Len(t, rows, 2)

			byID := map[int64]ports.MovieCollectionPart{}
			for _, r := range rows {
				byID[r.MovieID] = r
			}

			// p1 → localized title + raw canon poster.
			g1 := byID[int64(p1)]
			assert.Equal(t, "Дюна", g1.Title, "localized ru-RU title wins")
			require.NotNil(t, g1.Poster)
			assert.Equal(t, "/dune_p1.jpg", *g1.Poster, "raw canon poster path")

			// p2 → canon title fallback (no localized row).
			g2 := byID[int64(p2)]
			assert.Equal(t, "Dune: Part Two", g2.Title, "canon title fallback")
			require.NotNil(t, g2.Poster)
			assert.Equal(t, "/dune_p2.jpg", *g2.Poster)

			// Empty lang → canon title for everyone (no localized override).
			rowsNoLang, err := repo.ListPartsWithMembership(ctx, cid, "r1", "")
			require.NoError(t, err)
			require.Len(t, rowsNoLang, 2)
			for _, r := range rowsNoLang {
				if r.MovieID == int64(p1) {
					assert.Equal(t, "Dune", r.Title, "empty lang → canon title, not localized")
				}
			}
		})
	}
}
