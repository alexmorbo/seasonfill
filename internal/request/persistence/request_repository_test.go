package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	reqdomain "github.com/alexmorbo/seasonfill/internal/request/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func seedRequestUser(t *testing.T, db *gorm.DB, id uint, username string) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.UserModel{
		ID:         id,
		Username:   username,
		Role:       admin.RoleUser,
		AvatarMode: admin.AvatarModeAuto,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error)
}

func tvRequest(userID uint, tmdb int64, seasons *[]int) reqdomain.Request {
	return reqdomain.Request{
		UserID:    userID,
		MediaType: reqdomain.MediaTypeTV,
		TMDBID:    tmdb,
		Seasons:   seasons,
		Spec: reqdomain.AddSpec{
			MediaType: reqdomain.MediaTypeTV, ExternalID: tmdb,
			InstanceName: "main", QualityProfileID: 6, RootFolderPath: "/tv",
			Monitored: true, MonitorMode: "all", Seasons: seasons,
		},
		Status: reqdomain.StatusPending,
	}
}

func TestRequestRepository_InsertPending_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedRequestUser(t, db, 1, "alice")
			repo := NewRequestRepository(db)
			ctx := context.Background()

			id1, existed1, err := repo.InsertPending(ctx, tvRequest(1, 1399, nil))
			require.NoError(t, err)
			assert.False(t, existed1)
			assert.Positive(t, id1)

			// Same (user, media_type, tmdb_id) → existing id, existed=true.
			id2, existed2, err := repo.InsertPending(ctx, tvRequest(1, 1399, nil))
			require.NoError(t, err)
			assert.True(t, existed2)
			assert.Equal(t, id1, id2)

			// Different tmdb_id → new row.
			id3, existed3, err := repo.InsertPending(ctx, tvRequest(1, 1400, nil))
			require.NoError(t, err)
			assert.False(t, existed3)
			assert.NotEqual(t, id1, id3)

			// Different media_type (movie) same tmdb → new row.
			movie := reqdomain.Request{
				UserID: 1, MediaType: reqdomain.MediaTypeMovie, TMDBID: 1399,
				Spec:   reqdomain.AddSpec{MediaType: reqdomain.MediaTypeMovie, ExternalID: 1399},
				Status: reqdomain.StatusPending,
			}
			id4, existed4, err := repo.InsertPending(ctx, movie)
			require.NoError(t, err)
			assert.False(t, existed4)
			assert.NotEqual(t, id1, id4)
		})
	}
}

func TestRequestRepository_ListScoping(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedRequestUser(t, db, 1, "alice")
			seedRequestUser(t, db, 2, "bob")
			repo := NewRequestRepository(db)
			ctx := context.Background()

			_, _, err := repo.InsertPending(ctx, tvRequest(1, 10, nil))
			require.NoError(t, err)
			_, _, err = repo.InsertPending(ctx, tvRequest(2, 20, nil))
			require.NoError(t, err)

			own, err := repo.ListByUser(ctx, 1)
			require.NoError(t, err)
			require.Len(t, own, 1)
			assert.Equal(t, uint(1), own[0].UserID)

			all, err := repo.ListAll(ctx)
			require.NoError(t, err)
			assert.Len(t, all, 2)
		})
	}
}

func TestRequestRepository_SetStatus_And_PendingKeyFrees(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedRequestUser(t, db, 1, "alice")
			seedRequestUser(t, db, 9, "approver")
			repo := NewRequestRepository(db)
			ctx := context.Background()

			id, _, err := repo.InsertPending(ctx, tvRequest(1, 55, nil))
			require.NoError(t, err)

			require.NoError(t, repo.SetStatus(ctx, id, reqdomain.StatusDenied, 9))

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, reqdomain.StatusDenied, got.Status)
			require.NotNil(t, got.ApproverID)
			assert.Equal(t, uint(9), *got.ApproverID)

			// Denied row's key no longer blocks a new pending insert
			// (partial-unique is WHERE status='pending').
			id2, existed, err := repo.InsertPending(ctx, tvRequest(1, 55, nil))
			require.NoError(t, err)
			assert.False(t, existed)
			assert.NotEqual(t, id, id2)
		})
	}
}

func TestRequestRepository_SetStatus_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRequestRepository(db)
			err := repo.SetStatus(context.Background(), 9999, reqdomain.StatusApproved, 1)
			require.ErrorIs(t, err, ports.ErrNotFound)
		})
	}
}

func TestRequestRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRequestRepository(db)
			_, err := repo.Get(context.Background(), 9999)
			require.ErrorIs(t, err, ports.ErrNotFound)
		})
	}
}

func TestRequestRepository_RoundTrip_SeasonsAndSpec(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedRequestUser(t, db, 1, "alice")
			repo := NewRequestRepository(db)
			ctx := context.Background()

			seasons := []int{1, 2, 5}
			id, _, err := repo.InsertPending(ctx, tvRequest(1, 777, &seasons))
			require.NoError(t, err)

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, got.Seasons)
			assert.Equal(t, []int{1, 2, 5}, *got.Seasons)
			assert.Equal(t, reqdomain.MediaTypeTV, got.Spec.MediaType)
			assert.Equal(t, int64(777), got.Spec.ExternalID)
			assert.Equal(t, "main", got.Spec.InstanceName)
			assert.Equal(t, 6, got.Spec.QualityProfileID)
			assert.Equal(t, "/tv", got.Spec.RootFolderPath)
			require.NotNil(t, got.Spec.Seasons)
			assert.Equal(t, []int{1, 2, 5}, *got.Spec.Seasons)

			// movie: nil seasons round-trips as nil.
			movie := reqdomain.Request{
				UserID: 1, MediaType: reqdomain.MediaTypeMovie, TMDBID: 888,
				Spec:   reqdomain.AddSpec{MediaType: reqdomain.MediaTypeMovie, ExternalID: 888},
				Status: reqdomain.StatusPending,
			}
			mid, _, err := repo.InsertPending(ctx, movie)
			require.NoError(t, err)
			mgot, err := repo.Get(ctx, mid)
			require.NoError(t, err)
			assert.Nil(t, mgot.Seasons)
		})
	}
}
