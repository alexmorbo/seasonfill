package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// testUserID is the seed-admin owner used by the per-user followed_series
// tests (Ф8-U-5). seedUser inserts the matching users row so the FK holds.
const testUserID int64 = 1

func seedUser(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.UserModel{
		ID:         uint(testUserID),
		Username:   "admin",
		Role:       admin.RoleAdmin,
		AvatarMode: admin.AvatarModeAuto,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error)
}

func ptrTMDB(i int) *domain.TMDBID { v := domain.TMDBID(i); return &v }

// seedCanon inserts a canon series row (satisfies the followed_series FK) and
// returns its surrogate id.
func seedCanon(t *testing.T, db *gorm.DB, title string, tmdbID int) domain.SeriesID {
	t.Helper()
	repo := enrichpersistence.NewSeriesRepository(db)
	c := series.Canon{
		Hydration:     series.HydrationStub,
		TMDBID:        ptrTMDB(tmdbID),
		OriginalTitle: new("orig: " + title),
		Year:          new(2024),
	}
	id, err := repo.Upsert(context.Background(), c)
	require.NoError(t, err)
	return id
}

func seedSeriesText(t *testing.T, db *gorm.DB, id domain.SeriesID, lang, title string) {
	t.Helper()
	ttl := title
	row := database.SeriesTextModel{SeriesID: id, Language: lang, Title: &ttl, UpdatedAt: time.Now().UTC()}
	require.NoError(t, db.Create(&row).Error)
}

func seedMediaText(t *testing.T, db *gorm.DB, id domain.SeriesID, lang, poster string) {
	t.Helper()
	p := poster
	row := database.SeriesMediaTextModel{SeriesID: id, Language: lang, PosterAsset: &p, UpdatedAt: time.Now().UTC()}
	require.NoError(t, db.Create(&row).Error)
}

func countFollowed(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&database.FollowedSeriesModel{}).Count(&n).Error)
	return n
}

func TestFollowedSeriesRepository_Follow_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewFollowedSeriesRepository(db)
			seedUser(t, db)
			ctx := context.Background()

			id := seedCanon(t, db, "Foundation", 5001)
			require.NoError(t, repo.Follow(ctx, testUserID, id))
			require.NoError(t, repo.Follow(ctx, testUserID, id), "second follow is ON CONFLICT DO NOTHING")

			assert.Equal(t, int64(1), countFollowed(t, db), "exactly one followed_series row")
			items, err := repo.ListFollowed(ctx, testUserID, "en-US")
			require.NoError(t, err)
			require.Len(t, items, 1)
			assert.Equal(t, id, items[0].SeriesID)
		})
	}
}

func TestFollowedSeriesRepository_Unfollow_DeletesRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewFollowedSeriesRepository(db)
			seedUser(t, db)
			ctx := context.Background()

			id := seedCanon(t, db, "Severance", 5002)
			require.NoError(t, repo.Follow(ctx, testUserID, id))
			require.NoError(t, repo.Unfollow(ctx, testUserID, id))

			items, err := repo.ListFollowed(ctx, testUserID, "en-US")
			require.NoError(t, err)
			assert.Empty(t, items)
			// Second unfollow is an idempotent no-op.
			require.NoError(t, repo.Unfollow(ctx, testUserID, id))
			assert.Equal(t, int64(0), countFollowed(t, db))
		})
	}
}

func TestFollowedSeriesRepository_ListFollowed_TitlePosterFallback(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewFollowedSeriesRepository(db)
			seedUser(t, db)
			ctx := context.Background()

			// Series with ru title + en poster.
			idA := seedCanon(t, db, "A", 5101)
			seedSeriesText(t, db, idA, "ru-RU", "Игра престолов")
			seedMediaText(t, db, idA, "en-US", "/posters/a-en.jpg")
			require.NoError(t, repo.Follow(ctx, testUserID, idA))

			// Series with NO i18n rows → falls back to original_title + nil poster.
			idB := seedCanon(t, db, "B", 5102)
			require.NoError(t, repo.Follow(ctx, testUserID, idB))

			items, err := repo.ListFollowed(ctx, testUserID, "ru-RU")
			require.NoError(t, err)
			require.Len(t, items, 2)

			got := map[domain.SeriesID]struct {
				title  string
				poster *string
			}{}
			for _, it := range items {
				got[it.SeriesID] = struct {
					title  string
					poster *string
				}{it.Title, it.PosterAsset}
			}

			assert.Equal(t, "Игра престолов", got[idA].title, "ru title wins for the requested lang")
			require.NotNil(t, got[idA].poster)
			assert.Equal(t, "/posters/a-en.jpg", *got[idA].poster, "en poster fallback")

			assert.Equal(t, "orig: B", got[idB].title, "original_title fallback when no i18n text")
			assert.Nil(t, got[idB].poster, "nil poster when no media text")
		})
	}
}

func TestFollowedSeriesRepository_ListFollowed_OrderNewestFirst(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewFollowedSeriesRepository(db)
			seedUser(t, db)
			ctx := context.Background()

			idOld := seedCanon(t, db, "old", 5201)
			idNew := seedCanon(t, db, "new", 5202)

			// Insert explicit created_at so ordering is deterministic.
			require.NoError(t, db.Create(&database.FollowedSeriesModel{
				UserID: testUserID, SeriesID: int64(idOld), CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			}).Error)
			require.NoError(t, db.Create(&database.FollowedSeriesModel{
				UserID: testUserID, SeriesID: int64(idNew), CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			}).Error)

			items, err := repo.ListFollowed(ctx, testUserID, "en-US")
			require.NoError(t, err)
			require.Len(t, items, 2)
			assert.Equal(t, idNew, items[0].SeriesID, "newest followed first")
			assert.Equal(t, idOld, items[1].SeriesID)
		})
	}
}

// TestFollowedSeriesRepository_Follow_FKRequiresSeries asserts the FK to
// series(id) rejects a follow of a non-existent series. Postgres-only: the
// sqlite test harness opens :memory: without PRAGMA foreign_keys=ON, so FK
// enforcement is a no-op there.
func TestFollowedSeriesRepository_Follow_FKRequiresSeries(t *testing.T) {
	testhelpers.SkipIfNoPostgres(t)
	for _, backend := range testhelpers.AllBackends(t) {
		if backend.Name != "postgres" {
			continue
		}
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.NewDB(t)
			repo := NewFollowedSeriesRepository(db)
			seedUser(t, db)
			err := repo.Follow(context.Background(), testUserID, domain.SeriesID(9_999_999))
			require.Error(t, err, "FK violation must surface for a follow of a missing series")
		})
	}
}
