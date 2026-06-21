package externalservices

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestMergeWithSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		svc       Service
		db        Settings
		env       map[string]string
		wantKey   string
		wantSrc   SourceMap
		wantEnabl bool
	}{
		{
			name:      "fresh install (db empty, env empty)",
			svc:       ServiceTMDB,
			wantSrc:   SourceMap{},
			wantEnabl: false,
		},
		{
			name:      "fresh install + env token (fallback)",
			svc:       ServiceTMDB,
			env:       map[string]string{"SEASONFILL_TMDB_TOKEN": "env-key"},
			wantKey:   "env-key",
			wantSrc:   SourceMap{APIKey: FieldSourceEnv},
			wantEnabl: true,
		},
		{
			name:      "db has key, env empty (db wins)",
			svc:       ServiceOMDB,
			db:        Settings{APIKey: "db-key", APIKeyLast4: "-key", Enabled: true},
			wantKey:   "db-key",
			wantSrc:   SourceMap{APIKey: FieldSourceDB},
			wantEnabl: true,
		},
		{
			name:      "db + env both set (env overrides per PRD §10.4.4)",
			svc:       ServiceOMDB,
			db:        Settings{APIKey: "db-key", APIKeyLast4: "-key", Enabled: true},
			env:       map[string]string{"SEASONFILL_OMDB_TOKEN": "env-key"},
			wantKey:   "env-key",
			wantSrc:   SourceMap{APIKey: FieldSourceEnv},
			wantEnabl: true,
		},
		{
			name:    "proxy fields from db, token from env",
			svc:     ServiceTMDB,
			db:      Settings{ProxyURL: "http://db:1", ProxyUsername: "u", ProxyPassword: "p"},
			env:     map[string]string{"SEASONFILL_TMDB_TOKEN": "env-key"},
			wantKey: "env-key",
			wantSrc: SourceMap{
				APIKey:        FieldSourceEnv,
				ProxyURL:      FieldSourceDB,
				ProxyUsername: FieldSourceDB,
				ProxyPassword: FieldSourceDB,
			},
			wantEnabl: true,
		},
		{
			name: "all env, no db",
			svc:  ServiceTVDB,
			env: map[string]string{
				"SEASONFILL_TVDB_TOKEN":      "k",
				"SEASONFILL_TVDB_PROXY_URL":  "http://env:2",
				"SEASONFILL_TVDB_PROXY_USER": "eu",
				"SEASONFILL_TVDB_PROXY_PASS": "ep",
			},
			wantKey: "k",
			wantSrc: SourceMap{
				APIKey:        FieldSourceEnv,
				ProxyURL:      FieldSourceEnv,
				ProxyUsername: FieldSourceEnv,
				ProxyPassword: FieldSourceEnv,
			},
			wantEnabl: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := func(name string) string { return tt.env[name] }
			got, src := MergeWithSource(tt.svc, tt.db, env)
			if got.APIKey != tt.wantKey {
				t.Fatalf("APIKey = %q want %q", got.APIKey, tt.wantKey)
			}
			if got.Enabled != tt.wantEnabl {
				t.Fatalf("Enabled = %v want %v", got.Enabled, tt.wantEnabl)
			}
			if src != tt.wantSrc {
				t.Fatalf("SourceMap = %+v want %+v", src, tt.wantSrc)
			}
		})
	}
}

// TestMerge_BackwardCompatibility guards the public Merge signature
// against drift. The thin wrapper must produce byte-identical Settings
// to MergeWithSource for every input.
func TestMerge_BackwardCompatibility(t *testing.T) {
	t.Parallel()
	env := func(string) string { return "x" }
	a := Merge(ServiceTMDB, Settings{APIKey: "db"}, env)
	b, _ := MergeWithSource(ServiceTMDB, Settings{APIKey: "db"}, env)
	if a != b {
		t.Fatalf("Merge drift: %+v != %+v", a, b)
	}
}

// ----------------------------------------------------------------------
// D-5 (466c) BYTEA FK wiring tests — Repository against real schema.
// ----------------------------------------------------------------------
//
// D-0 quality bar:
//   - testcontainers Postgres + SQLite via testhelpers.AllBackends
//   - non-shared service ids per test (use AllServices canonical ones
//     scoped per-DB; each test gets a fresh :memory: / per-test pg DB)
//   - error-pair coverage (Get missing row + Upsert empty clears FK)

func newTestCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New("test-master-key-466c")
	require.NoError(t, err)
	return c
}

// TestExternalServicesRepository_Get_NotFound covers the missing-row
// error contract: ports.ErrNotFound must surface so the use case can
// return a zero-value Settings to the UI.
func TestExternalServicesRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewRepository(backend.NewDB(t), newTestCipher(t))
			_, err := repo.Get(context.Background(), ServiceTMDB)
			require.ErrorIs(t, err, ports.ErrNotFound)
		})
	}
}

// TestExternalServicesRepository_Get_InvalidService rejects unknown
// service names at the boundary.
func TestExternalServicesRepository_Get_InvalidService(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewRepository(backend.NewDB(t), newTestCipher(t))
			_, err := repo.Get(context.Background(), Service("bogus"))
			require.ErrorIs(t, err, ErrInvalidService)
		})
	}
}

// TestExternalServicesRepository_Upsert_RoundTrip_BYTEAFKWiring
// covers the headline D-5 466c invariant: an Upsert with non-empty
// secrets lands TWO app_secret rows + ONE external_service_config
// row with FK ids pointing at them; Get round-trips the plaintext.
func TestExternalServicesRepository_Upsert_RoundTrip_BYTEAFKWiring(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRepository(db, newTestCipher(t))
			ctx := context.Background()

			in := Settings{
				Service:       ServiceTMDB,
				Enabled:       true,
				APIKey:        "plaintext-tmdb-key-xyz",
				APIKeyLast4:   "-xyz",
				ProxyURL:      "http://proxy.local:8080",
				ProxyUsername: "puser",
				ProxyPassword: "ppass",
			}
			require.NoError(t, repo.Upsert(ctx, in))

			// app_secret should hold two rows for tmdb: api_key + proxy_pass.
			var secrets []database.AppSecretModel
			require.NoError(t, db.Where(
				"secret_name IN ?",
				[]string{
					secretNameAPIKey(ServiceTMDB),
					secretNameProxyPass(ServiceTMDB),
				}).Find(&secrets).Error)
			assert.Len(t, secrets, 2,
				"Upsert with non-empty api_key+proxy_pass writes 2 app_secret rows")

			// external_service_config row carries the FK ids, plaintext
			// proxy fields, and last4.
			var cfg database.ExternalServiceConfigModel
			require.NoError(t, db.Where(
				"service_name = ?", string(ServiceTMDB),
			).First(&cfg).Error)
			require.NotNil(t, cfg.APIKeySecretID)
			require.NotNil(t, cfg.ProxyPassSecretID)
			require.NotNil(t, cfg.ProxyURL)
			require.NotNil(t, cfg.ProxyUser)
			require.NotNil(t, cfg.Last4)
			assert.True(t, cfg.Enabled)
			assert.Equal(t, "http://proxy.local:8080", *cfg.ProxyURL)
			assert.Equal(t, "puser", *cfg.ProxyUser)
			assert.Equal(t, "-xyz", *cfg.Last4)

			// Get round-trips plaintext through cipher.
			got, err := repo.Get(ctx, ServiceTMDB)
			require.NoError(t, err)
			assert.Equal(t, "plaintext-tmdb-key-xyz", got.APIKey)
			assert.Equal(t, "ppass", got.ProxyPassword)
			assert.Equal(t, "http://proxy.local:8080", got.ProxyURL)
			assert.Equal(t, "puser", got.ProxyUsername)
			assert.Equal(t, "-xyz", got.APIKeyLast4)
			assert.True(t, got.Enabled)
		})
	}
}

// TestExternalServicesRepository_Upsert_EmptyKey_ClearsSecretRow
// covers operator clearing the api_key field: the corresponding
// app_secret row is deleted and the FK is left NULL.
func TestExternalServicesRepository_Upsert_EmptyKey_ClearsSecretRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRepository(db, newTestCipher(t))
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, Settings{
				Service:     ServiceOMDB,
				Enabled:     true,
				APIKey:      "to-be-cleared",
				APIKeyLast4: "ared",
			}))
			// Pre-condition: secret exists.
			var pre []database.AppSecretModel
			require.NoError(t, db.Where(
				"secret_name = ?", secretNameAPIKey(ServiceOMDB),
			).Find(&pre).Error)
			require.Len(t, pre, 1)

			// Now clear by upserting with empty APIKey.
			require.NoError(t, repo.Upsert(ctx, Settings{
				Service: ServiceOMDB,
				Enabled: false,
			}))

			// Post-condition: app_secret row gone, FK NULL.
			var post []database.AppSecretModel
			require.NoError(t, db.Where(
				"secret_name = ?", secretNameAPIKey(ServiceOMDB),
			).Find(&post).Error)
			assert.Empty(t, post,
				"empty plaintext must DELETE the app_secret row")

			var cfg database.ExternalServiceConfigModel
			require.NoError(t, db.Where(
				"service_name = ?", string(ServiceOMDB),
			).First(&cfg).Error)
			assert.Nil(t, cfg.APIKeySecretID,
				"empty plaintext must leave the FK NULL")
		})
	}
}

// TestExternalServicesRepository_List_ReturnsAllServices verifies the
// canonical AllServices iteration order: missing rows return as
// zero-value Settings (Service field populated).
func TestExternalServicesRepository_List_ReturnsAllServices(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRepository(db, newTestCipher(t))
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, Settings{
				Service: ServiceTMDB,
				Enabled: true,
				APIKey:  "tmdb-key",
			}))

			out, err := repo.List(ctx)
			require.NoError(t, err)
			require.Len(t, out, len(AllServices))

			// Order matches AllServices canonical order.
			for i, svc := range AllServices {
				assert.Equal(t, svc, out[i].Service,
					"List output position %d must match AllServices order", i)
			}
			// tmdb row populated, omdb + tvdb zero.
			assert.Equal(t, "tmdb-key", out[0].APIKey)
			assert.True(t, out[0].Enabled)
			assert.Empty(t, out[1].APIKey)
			assert.Empty(t, out[2].APIKey)
		})
	}
}

// TestExternalServicesRepository_MarkTest_NoOp covers the D-5
// Decision B contract: MarkTest must NOT write to the DB. We assert
// by reading the config row before + after — UpdatedAt must not move.
func TestExternalServicesRepository_MarkTest_NoOp(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewRepository(db, newTestCipher(t))
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, Settings{
				Service: ServiceTVDB,
				Enabled: true,
				APIKey:  "tvdb-key",
			}))
			var before database.ExternalServiceConfigModel
			require.NoError(t, db.Where(
				"service_name = ?", string(ServiceTVDB),
			).First(&before).Error)

			// MarkTest must NOT touch the row.
			require.NoError(t, repo.MarkTest(ctx, ServiceTVDB,
				before.UpdatedAt, OutcomeOK, "ok"))

			var after database.ExternalServiceConfigModel
			require.NoError(t, db.Where(
				"service_name = ?", string(ServiceTVDB),
			).First(&after).Error)
			assert.True(t, after.UpdatedAt.Equal(before.UpdatedAt),
				"MarkTest must be a no-op (D-5 Decision B)")
		})
	}
}

// TestExternalServicesRepository_MarkTest_InvalidService rejects
// unknown service names.
func TestExternalServicesRepository_MarkTest_InvalidService(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewRepository(backend.NewDB(t), newTestCipher(t))
			err := repo.MarkTest(context.Background(),
				Service("nope"), time.Now().UTC(), OutcomeOK, "ok")
			require.True(t, errors.Is(err, ErrInvalidService))
		})
	}
}
