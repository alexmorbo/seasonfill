package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// otherUserID is a second owner used to prove followed_movies reads/writes are
// owner-scoped. seedOtherUser inserts the matching users row so the FK holds.
const otherUserID int64 = 2

func seedOtherUser(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.UserModel{
		ID:         uint(otherUserID),
		Username:   "ipad",
		Role:       admin.RoleUser,
		AvatarMode: admin.AvatarModeAuto,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error)
}

// seedMovieCanon inserts a canon movies row (satisfies the followed_movies FK)
// and returns its surrogate id.
func seedMovieCanon(t *testing.T, db *gorm.DB, title string, tmdbID int) domain.MovieID {
	t.Helper()
	repo := enrichpersistence.NewMovieRepository(db)
	id, err := repo.Upsert(context.Background(), movie.Canon{
		Hydration: movie.HydrationStub,
		TMDBID:    ptrTMDB(tmdbID),
		Title:     title,
		Year:      new(1999),
	})
	require.NoError(t, err)
	return id
}

func seedMovieI18n(t *testing.T, db *gorm.DB, id domain.MovieID, lang string, title, poster *string) {
	t.Helper()
	row := database.MovieI18nModel{
		MovieID:     id,
		Language:    lang,
		Title:       title,
		PosterAsset: poster,
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, db.Create(&row).Error)
}

func countFollowedMovies(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&database.FollowedMovieModel{}).Count(&n).Error)
	return n
}

func TestFollowedMoviesRepository_Follow_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewFollowedMoviesRepository(db)
			seedUser(t, db)
			ctx := context.Background()

			id := seedMovieCanon(t, db, "Fight Club", 6001)
			require.NoError(t, repo.Follow(ctx, testUserID, id))
			require.NoError(t, repo.Follow(ctx, testUserID, id), "second follow is ON CONFLICT DO NOTHING")

			assert.Equal(t, int64(1), countFollowedMovies(t, db), "exactly one followed_movies row")
			items, err := repo.ListFollowed(ctx, testUserID, "en-US")
			require.NoError(t, err)
			require.Len(t, items, 1)
			assert.Equal(t, id, items[0].MovieID)
			require.NotNil(t, items[0].TMDBID)
			assert.Equal(t, domain.TMDBID(6001), *items[0].TMDBID)
		})
	}
}

func TestFollowedMoviesRepository_Unfollow_DeletesRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewFollowedMoviesRepository(db)
			seedUser(t, db)
			ctx := context.Background()

			id := seedMovieCanon(t, db, "Dune", 6002)
			require.NoError(t, repo.Follow(ctx, testUserID, id))
			require.NoError(t, repo.Unfollow(ctx, testUserID, id))

			items, err := repo.ListFollowed(ctx, testUserID, "en-US")
			require.NoError(t, err)
			assert.Empty(t, items)
			// Second unfollow is an idempotent no-op.
			require.NoError(t, repo.Unfollow(ctx, testUserID, id))
			assert.Equal(t, int64(0), countFollowedMovies(t, db))
		})
	}
}

func TestFollowedMoviesRepository_ListFollowed_TitlePosterFallback(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewFollowedMoviesRepository(db)
			seedUser(t, db)
			ctx := context.Background()

			// Requested-lang title present, poster only on en-US.
			ruTitle := "Бойцовский клуб"
			enPoster := "/posters/a-en.jpg"
			idA := seedMovieCanon(t, db, "A", 6101)
			seedMovieI18n(t, db, idA, "ru-RU", &ruTitle, nil)
			seedMovieI18n(t, db, idA, "en-US", nil, &enPoster)
			require.NoError(t, repo.Follow(ctx, testUserID, idA))

			// No i18n rows at all → canon title, nil poster.
			idB := seedMovieCanon(t, db, "B", 6102)
			require.NoError(t, repo.Follow(ctx, testUserID, idB))

			items, err := repo.ListFollowed(ctx, testUserID, "ru-RU")
			require.NoError(t, err)
			require.Len(t, items, 2)

			byID := map[domain.MovieID]int{}
			for i, it := range items {
				byID[it.MovieID] = i
			}
			a := items[byID[idA]]
			assert.Equal(t, ruTitle, a.Title, "requested-lang title wins")
			require.NotNil(t, a.PosterAsset)
			assert.Equal(t, enPoster, *a.PosterAsset, "poster falls back to en-US")

			b := items[byID[idB]]
			assert.Equal(t, "B", b.Title, "canon title when no i18n row")
			assert.Nil(t, b.PosterAsset)
		})
	}
}

func TestFollowedMoviesRepository_ListFollowed_IsOwnerScoped(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewFollowedMoviesRepository(db)
			seedUser(t, db)
			seedOtherUser(t, db)
			ctx := context.Background()

			mine := seedMovieCanon(t, db, "Mine", 6201)
			theirs := seedMovieCanon(t, db, "Theirs", 6202)
			require.NoError(t, repo.Follow(ctx, testUserID, mine))
			require.NoError(t, repo.Follow(ctx, otherUserID, theirs))

			items, err := repo.ListFollowed(ctx, testUserID, "en-US")
			require.NoError(t, err)
			require.Len(t, items, 1)
			assert.Equal(t, mine, items[0].MovieID)

			// Unfollowing the other user's row from our id must not touch theirs.
			require.NoError(t, repo.Unfollow(ctx, testUserID, theirs))
			other, err := repo.ListFollowed(ctx, otherUserID, "en-US")
			require.NoError(t, err)
			require.Len(t, other, 1)
			assert.Equal(t, theirs, other[0].MovieID)
		})
	}
}

func TestFollowedMoviesRepository_ListFollowed_NewestFirst(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewFollowedMoviesRepository(db)
			seedUser(t, db)
			ctx := context.Background()

			older := seedMovieCanon(t, db, "Older", 6301)
			newer := seedMovieCanon(t, db, "Newer", 6302)
			now := time.Now().UTC()
			require.NoError(t, db.Create(&database.FollowedMovieModel{
				UserID: testUserID, MovieID: older, CreatedAt: now.Add(-2 * time.Hour),
			}).Error)
			require.NoError(t, db.Create(&database.FollowedMovieModel{
				UserID: testUserID, MovieID: newer, CreatedAt: now,
			}).Error)

			items, err := repo.ListFollowed(ctx, testUserID, "en-US")
			require.NoError(t, err)
			require.Len(t, items, 2)
			assert.Equal(t, newer, items[0].MovieID)
			assert.Equal(t, older, items[1].MovieID)
		})
	}
}
