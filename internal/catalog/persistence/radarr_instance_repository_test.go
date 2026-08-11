package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestRadarrInstanceRepository_GetSettings(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRadarrInstanceRepository(db)
			ctx := context.Background()
			now := time.Now().UTC()

			// Seed a type='radarr' arr_instance + its settings row.
			require.NoError(t, db.Create(&database.SonarrInstanceModel{
				Name: "films", URL: "http://radarr.local:7878", Mode: "auto",
				Type: "radarr", Health: "ok", CreatedAt: now, UpdatedAt: now,
			}).Error)
			require.NoError(t, db.Create(&database.RadarrInstanceSettingsModel{
				InstanceName: "films", TimeoutSeconds: 15, SearchTimeoutSeconds: 90,
				RateLimitRPM: 40, RateLimitBurst: 12, TagsMode: "any",
				WebhookInstallEnabled: true, ParseOnGrabEnabled: true,
				ScanSkipHandledSeasons: true, UpdatedAt: now,
			}).Error)

			got, err := repo.GetSettings(ctx, "films")
			require.NoError(t, err)
			assert.Equal(t, "films", got.InstanceName)
			assert.Equal(t, 15, got.TimeoutSeconds)
			assert.Equal(t, 90, got.SearchTimeoutSeconds)
			assert.Equal(t, 40, got.RateLimitRPM)
			assert.Equal(t, 12, got.RateLimitBurst)
		})
	}
}

func TestRadarrInstanceRepository_GetSettings_SonarrTypeIsNotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRadarrInstanceRepository(db)
			ctx := context.Background()
			now := time.Now().UTC()

			// A type='sonarr' row must NOT resolve via the radarr discriminator.
			require.NoError(t, db.Create(&database.SonarrInstanceModel{
				Name: "tv", URL: "http://sonarr.local:8989", Mode: "auto",
				Type: "sonarr", Health: "ok", CreatedAt: now, UpdatedAt: now,
			}).Error)

			_, err := repo.GetSettings(ctx, "tv")
			require.Error(t, err)
			assert.ErrorIs(t, err, ports.ErrNotFound)
			var nf *sharedErrors.InstanceNotFoundError
			assert.ErrorAs(t, err, &nf)
		})
	}
}

func TestRadarrInstanceRepository_GetSettings_MissingSettingsRowIsZeroValue(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRadarrInstanceRepository(db)
			ctx := context.Background()
			now := time.Now().UTC()

			// arr_instance present, settings row absent → zero-value defaults, no error.
			require.NoError(t, db.Create(&database.SonarrInstanceModel{
				Name: "films2", URL: "http://radarr.local:7878", Mode: "auto",
				Type: "radarr", Health: "ok", CreatedAt: now, UpdatedAt: now,
			}).Error)

			got, err := repo.GetSettings(ctx, "films2")
			require.NoError(t, err)
			assert.Equal(t, "films2", got.InstanceName)
			assert.Equal(t, 0, got.TimeoutSeconds)
		})
	}
}

func TestRadarrInstanceRepository_GetSettings_UnknownInstance(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRadarrInstanceRepository(db)

			_, err := repo.GetSettings(context.Background(), "ghost")
			require.Error(t, err)
			assert.ErrorIs(t, err, ports.ErrNotFound)
		})
	}
}
