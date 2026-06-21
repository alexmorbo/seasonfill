//go:build integration

// D-1-6b (story 459b) — verifies 000001..000010 apply cleanly on both
// backends, exercises FK cascade between sonarr_instance and
// instance_secret (both directions) plus UNIQUE composite enforcement.
package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestD16b_SonarrInstanceMigrationRoundTrip(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			// UP — applies 000001..000010 in sequence.
			require.NoError(t, m.Up())

			instanceName := "inst-" + uuid.NewString()[:8]

			// sonarr_instance happy path (no token_secret_id yet).
			_, err := db.ExecContext(ctx, insertSonarrInstanceSQL(b.name),
				instanceName, "https://sonarr.example.com")
			require.NoError(t, err, "sonarr_instance insert should succeed")

			// Dup-PK violation.
			_, err = db.ExecContext(ctx, insertSonarrInstanceSQL(b.name),
				instanceName, "https://other.example.com")
			require.Error(t, err, "duplicate sonarr_instance.name should fail")

			// instance_secret happy path.
			tokenBytes := []byte{0x01, 0x02, 0xff, 0x03}
			secretID := insertInstanceSecretAndScanID(t, ctx, db, b.name,
				instanceName, "token", tokenBytes)
			require.Greater(t, secretID, int64(0))

			// Wire token_secret_id back-reference.
			_, err = db.ExecContext(ctx, updateSonarrInstanceTokenSecretSQL(b.name),
				secretID, instanceName)
			require.NoError(t, err, "wiring token_secret_id should succeed")

			// UNIQUE composite-2 violation on instance_secret: same
			// (instance_name, secret_name) different bytes.
			_, err = db.ExecContext(ctx, insertInstanceSecretSQL(b.name),
				instanceName, "token", []byte{0xde, 0xad})
			require.Error(t, err, "duplicate (instance_name, secret_name) should fail")

			// Different secret_name same instance → OK.
			_, err = db.ExecContext(ctx, insertInstanceSecretSQL(b.name),
				instanceName, "webhook_signing_key", []byte{0xbe, 0xef})
			require.NoError(t, err, "different secret_name should succeed")

			// FK violation on instance_secret: orphan instance_name.
			_, err = db.ExecContext(ctx, insertInstanceSecretSQL(b.name),
				"orphan-"+uuid.NewString()[:8], "token", []byte{0x11})
			require.Error(t, err, "orphan instance_name should fail FK")

			// SET NULL: delete the secret row, sonarr_instance.token_secret_id
			// should become NULL.
			_, err = db.ExecContext(ctx, deleteInstanceSecretByIDSQL(b.name), secretID)
			require.NoError(t, err, "delete instance_secret should succeed")

			var stillNull sql.NullInt64
			row := db.QueryRowContext(ctx, selectTokenSecretIDSQL(b.name), instanceName)
			require.NoError(t, row.Scan(&stillNull))
			require.False(t, stillNull.Valid, "token_secret_id should be NULL after secret deleted (SET NULL)")

			// CASCADE: delete the sonarr_instance, all remaining secrets
			// should go.
			_ = insertInstanceSecretAndScanID(t, ctx, db, b.name,
				instanceName, "fresh_token", []byte{0xaa, 0xbb})
			require.Equal(t, 2, countInstanceSecretsForName(t, ctx, db, b.name, instanceName),
				"instance has 2 secrets (webhook_signing_key + fresh_token)")

			_, err = db.ExecContext(ctx, deleteSonarrInstanceSQL(b.name), instanceName)
			require.NoError(t, err, "delete sonarr_instance should succeed")
			require.Equal(t, 0, countInstanceSecretsForName(t, ctx, db, b.name, instanceName),
				"instance_secret rows should CASCADE on sonarr_instance drop")

			// DOWN — rolls back 000010.
			require.NoError(t, m.Down())
			for _, table := range []string{"sonarr_instance", "instance_secret"} {
				_, err = db.ExecContext(ctx, "SELECT 1 FROM "+table+" LIMIT 1")
				require.Errorf(t, err, "%s should be dropped after Down", table)
			}
		})
	}
}

func insertSonarrInstanceSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO sonarr_instance (name, url, updated_at)
		        VALUES ($1, $2, now())`
	case "sqlite":
		return `INSERT INTO sonarr_instance (name, url, updated_at)
		        VALUES (?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func insertInstanceSecretSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO instance_secret (instance_name, secret_name, encrypted_value, updated_at)
		        VALUES ($1, $2, $3, now())`
	case "sqlite":
		return `INSERT INTO instance_secret (instance_name, secret_name, encrypted_value, updated_at)
		        VALUES (?, ?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func insertInstanceSecretAndScanID(t *testing.T, ctx context.Context, db *sql.DB, driver, instance, secretName string, value []byte) int64 {
	t.Helper()
	var id int64
	switch driver {
	case "postgres":
		stmt := `INSERT INTO instance_secret (instance_name, secret_name, encrypted_value, updated_at)
		         VALUES ($1, $2, $3, now()) RETURNING id`
		row := db.QueryRowContext(ctx, stmt, instance, secretName, value)
		require.NoError(t, row.Scan(&id))
	case "sqlite":
		stmt := `INSERT INTO instance_secret (instance_name, secret_name, encrypted_value, updated_at)
		         VALUES (?, ?, ?, CURRENT_TIMESTAMP)`
		res, err := db.ExecContext(ctx, stmt, instance, secretName, value)
		require.NoError(t, err)
		id, err = res.LastInsertId()
		require.NoError(t, err)
	default:
		t.Fatalf("unknown driver %q", driver)
	}
	return id
}

func updateSonarrInstanceTokenSecretSQL(driver string) string {
	switch driver {
	case "postgres":
		return `UPDATE sonarr_instance SET token_secret_id = $1, updated_at = now() WHERE name = $2`
	case "sqlite":
		return `UPDATE sonarr_instance SET token_secret_id = ?, updated_at = CURRENT_TIMESTAMP WHERE name = ?`
	}
	panic("unknown driver " + driver)
}

func deleteInstanceSecretByIDSQL(driver string) string {
	switch driver {
	case "postgres":
		return `DELETE FROM instance_secret WHERE id = $1`
	case "sqlite":
		return `DELETE FROM instance_secret WHERE id = ?`
	}
	panic("unknown driver " + driver)
}

func selectTokenSecretIDSQL(driver string) string {
	switch driver {
	case "postgres":
		return `SELECT token_secret_id FROM sonarr_instance WHERE name = $1`
	case "sqlite":
		return `SELECT token_secret_id FROM sonarr_instance WHERE name = ?`
	}
	panic("unknown driver " + driver)
}

func deleteSonarrInstanceSQL(driver string) string {
	switch driver {
	case "postgres":
		return `DELETE FROM sonarr_instance WHERE name = $1`
	case "sqlite":
		return `DELETE FROM sonarr_instance WHERE name = ?`
	}
	panic("unknown driver " + driver)
}

func countInstanceSecretsForName(t *testing.T, ctx context.Context, db *sql.DB, driver, instance string) int {
	t.Helper()
	var q string
	switch driver {
	case "postgres":
		q = `SELECT COUNT(*) FROM instance_secret WHERE instance_name = $1`
	case "sqlite":
		q = `SELECT COUNT(*) FROM instance_secret WHERE instance_name = ?`
	default:
		t.Fatalf("unknown driver %q", driver)
	}
	var cnt int
	row := db.QueryRowContext(ctx, q, instance)
	require.NoError(t, row.Scan(&cnt))
	return cnt
}
