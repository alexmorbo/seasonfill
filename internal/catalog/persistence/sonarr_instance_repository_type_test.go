package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSonarrInstanceRepository_TypeRoundTrip proves the Ф6-R-6a write-side gap is
// closed: Create with type='radarr' round-trips through GetByName, and an empty
// type falls back to the DB default 'sonarr'.
func TestSonarrInstanceRepository_TypeRoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSonarrInstanceRepository(db)
			ctx := context.Background()

			cipher, err := crypto.New("test-master-key-12345")
			require.NoError(t, err)

			base := func(name, typ string) runtime.InstanceSnapshot {
				return runtime.InstanceSnapshot{
					Name:          name,
					Type:          typ,
					URL:           "http://arr.local:7878",
					APIKey:        "secret-api-key",
					Mode:          "auto",
					Timeout:       10 * time.Second,
					SearchTimeout: 60 * time.Second,
				}
			}

			_, err = repo.Create(ctx, base("movies", "radarr"), cipher)
			require.NoError(t, err)
			gotRadarr, err := repo.GetByName(ctx, "movies", cipher)
			require.NoError(t, err)
			assert.Equal(t, "radarr", gotRadarr.Type, "type='radarr' must round-trip")

			// Empty type on the wire → DB default 'sonarr'.
			_, err = repo.Create(ctx, base("shows", ""), cipher)
			require.NoError(t, err)
			gotDefault, err := repo.GetByName(ctx, "shows", cipher)
			require.NoError(t, err)
			assert.Equal(t, "sonarr", gotDefault.Type, "empty type must default to 'sonarr'")
		})
	}
}
