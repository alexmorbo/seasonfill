//go:build integration

// D-1-6b (story 459b) — verifies 000001..000010 apply cleanly on both
// backends, exercises FK chain app_secret ← external_service_config and
// SET NULL behavior.
package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestD16b_AdminServicesMigrationRoundTrip(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			require.NoError(t, m.Up())

			// app_secret happy path.
			tmdbBytes := []byte{0x10, 0x20, 0xee}
			tmdbSecretID := insertAppSecretAndScanID(t, ctx, db, b.name,
				"tmdb_api_key", tmdbBytes)
			require.Greater(t, tmdbSecretID, int64(0))

			// UNIQUE on secret_name.
			_, err := db.ExecContext(ctx, insertAppSecretSQL(b.name),
				"tmdb_api_key", []byte{0x99})
			require.Error(t, err, "duplicate app_secret.secret_name should fail")

			// Different name → OK.
			proxyPassSecretID := insertAppSecretAndScanID(t, ctx, db, b.name,
				"tmdb_proxy_password", []byte{0xaa, 0xbb})
			require.Greater(t, proxyPassSecretID, int64(0))

			// external_service_config insert with both FK refs.
			_, err = db.ExecContext(ctx, insertExternalServiceConfigSQL(b.name),
				"tmdb", tmdbSecretID, proxyPassSecretID, "ab12")
			require.NoError(t, err, "external_service_config insert should succeed")

			// PK violation.
			_, err = db.ExecContext(ctx, insertExternalServiceConfigSQL(b.name),
				"tmdb", tmdbSecretID, proxyPassSecretID, "cd34")
			require.Error(t, err, "duplicate service_name should fail")

			// FK violation: orphan api_key_secret_id.
			_, err = db.ExecContext(ctx, insertExternalServiceConfigSQL(b.name),
				"omdb", int64(9999999), nil, "ef56")
			require.Error(t, err, "orphan api_key_secret_id should fail FK")

			// SET NULL: drop the api_key app_secret, config row's
			// api_key_secret_id becomes NULL but the config row stays.
			_, err = db.ExecContext(ctx, deleteAppSecretSQL(b.name), tmdbSecretID)
			require.NoError(t, err)
			var apiKeyID sql.NullInt64
			row := db.QueryRowContext(ctx, selectAPIKeySecretIDSQL(b.name), "tmdb")
			require.NoError(t, row.Scan(&apiKeyID))
			require.False(t, apiKeyID.Valid, "api_key_secret_id should be NULL after app_secret hard-delete (SET NULL)")

			// external_service_quota_state — composite-PK happy path.
			now := time.Now().UTC().Truncate(time.Hour)
			_, err = db.ExecContext(ctx, insertQuotaStateSQL(b.name),
				"tmdb", now, 5, 1000)
			require.NoError(t, err, "quota_state insert should succeed")

			// Different window → OK.
			_, err = db.ExecContext(ctx, insertQuotaStateSQL(b.name),
				"tmdb", now.Add(time.Hour), 1, 1000)
			require.NoError(t, err, "different window_start should succeed")

			// DOWN — rolls back 000010.
			require.NoError(t, m.Down())
			for _, table := range []string{"app_secret", "external_service_config", "external_service_quota_state"} {
				_, err = db.ExecContext(ctx, "SELECT 1 FROM "+table+" LIMIT 1")
				require.Errorf(t, err, "%s should be dropped after Down", table)
			}
		})
	}
}

func insertAppSecretSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO app_secret (secret_name, encrypted_value, updated_at)
		        VALUES ($1, $2, now())`
	case "sqlite":
		return `INSERT INTO app_secret (secret_name, encrypted_value, updated_at)
		        VALUES (?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func insertAppSecretAndScanID(t *testing.T, ctx context.Context, db *sql.DB, driver, name string, value []byte) int64 {
	t.Helper()
	var id int64
	switch driver {
	case "postgres":
		stmt := `INSERT INTO app_secret (secret_name, encrypted_value, updated_at)
		         VALUES ($1, $2, now()) RETURNING id`
		row := db.QueryRowContext(ctx, stmt, name, value)
		require.NoError(t, row.Scan(&id))
	case "sqlite":
		stmt := `INSERT INTO app_secret (secret_name, encrypted_value, updated_at)
		         VALUES (?, ?, CURRENT_TIMESTAMP)`
		res, err := db.ExecContext(ctx, stmt, name, value)
		require.NoError(t, err)
		id, err = res.LastInsertId()
		require.NoError(t, err)
	default:
		t.Fatalf("unknown driver %q", driver)
	}
	return id
}

func insertExternalServiceConfigSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO external_service_config (service_name, api_key_secret_id, proxy_pass_secret_id, last4, updated_at)
		        VALUES ($1, $2, $3, $4, now())`
	case "sqlite":
		return `INSERT INTO external_service_config (service_name, api_key_secret_id, proxy_pass_secret_id, last4, updated_at)
		        VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func deleteAppSecretSQL(driver string) string {
	switch driver {
	case "postgres":
		return `DELETE FROM app_secret WHERE id = $1`
	case "sqlite":
		return `DELETE FROM app_secret WHERE id = ?`
	}
	panic("unknown driver " + driver)
}

func selectAPIKeySecretIDSQL(driver string) string {
	switch driver {
	case "postgres":
		return `SELECT api_key_secret_id FROM external_service_config WHERE service_name = $1`
	case "sqlite":
		return `SELECT api_key_secret_id FROM external_service_config WHERE service_name = ?`
	}
	panic("unknown driver " + driver)
}

func insertQuotaStateSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO external_service_quota_state (service_name, window_start, requests_made, requests_quota, updated_at)
		        VALUES ($1, $2, $3, $4, now())`
	case "sqlite":
		return `INSERT INTO external_service_quota_state (service_name, window_start, requests_made, requests_quota, updated_at)
		        VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}
