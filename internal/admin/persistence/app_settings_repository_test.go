package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestAppSettingsRepository_GetTimezone_SeededNull(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewAppSettingsRepository(db)

			tz, err := repo.GetTimezone(context.Background())
			require.NoError(t, err)
			assert.Equal(t, "", tz, "fresh DB: seeded row has NULL timezone → empty string")
		})
	}
}

func TestAppSettingsRepository_SetThenGet(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewAppSettingsRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.SetTimezone(ctx, "Europe/Moscow"))

			got, err := repo.GetTimezone(ctx)
			require.NoError(t, err)
			assert.Equal(t, "Europe/Moscow", got)
		})
	}
}

func TestAppSettingsRepository_SetEmpty_ClearsToNull(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewAppSettingsRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.SetTimezone(ctx, "America/New_York"))
			require.NoError(t, repo.SetTimezone(ctx, ""))

			got, err := repo.GetTimezone(ctx)
			require.NoError(t, err)
			assert.Equal(t, "", got, "empty SetTimezone should clear column to NULL")
		})
	}
}

func TestAppSettingsRepository_GetTimezone_NoRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewAppSettingsRepository(db)
			ctx := context.Background()

			// The v36 migration seeds id=1 — wipe the singleton row to
			// exercise the (defensive) ErrNotFound branch.
			require.NoError(t, db.WithContext(ctx).
				Where("id = ?", appSettingsID).
				Delete(&database.AppSettingsModel{}).Error)

			_, err := repo.GetTimezone(ctx)
			require.Error(t, err)

			var typed *sharedErrors.AppSettingsNotFoundError
			require.True(t, errors.As(err, &typed),
				"GetTimezone NotFound must expose typed AppSettingsNotFoundError via errors.As")
		})
	}
}

func TestAppSettingsRepository_SetTimezone_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewAppSettingsRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.SetTimezone(ctx, "UTC"))
			require.NoError(t, repo.SetTimezone(ctx, "UTC"))
			require.NoError(t, repo.SetTimezone(ctx, "UTC"))

			got, err := repo.GetTimezone(ctx)
			require.NoError(t, err)
			assert.Equal(t, "UTC", got)
		})
	}
}
