//go:build integration

// D-1-7a (story 460a) — verifies 000001..000011 apply cleanly on both
// backends, exercises CHECK constraints (role + avatar_mode), partial
// UNIQUE on oidc_subject, composite UNIQUE on (instance_name,
// sonarr_tag_label), and FK CASCADE in both directions for
// user_instance_tags.
package integration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// TestD17a_UsersMigrationRoundTrip applies 000001..000011 then exercises
// users insert + CHECK violations + partial UNIQUE on oidc_subject.
func TestD17a_UsersMigrationRoundTrip(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			alice := "alice-" + uuid.NewString()[:8]
			bob := "bob-" + uuid.NewString()[:8]

			// Happy path inserts (defaults applied).
			_, err := db.ExecContext(ctx, insertUserDefaultSQL(b.name), alice)
			require.NoError(t, err, "users default-row insert should succeed")
			_, err = db.ExecContext(ctx, insertUserRoleSQL(b.name), bob, "user", "monogram")
			require.NoError(t, err, "users role=user should succeed")

			// Read-back round-trip — defaults stuck.
			role, avatar := scanUserRoleAvatar(t, ctx, db, b.name, alice)
			require.Equal(t, "admin", role, "default role should be 'admin'")
			require.Equal(t, "auto", avatar, "default avatar_mode should be 'auto'")

			// CHECK violation: bogus role.
			_, err = db.ExecContext(ctx, insertUserRoleSQL(b.name),
				"moderator-"+uuid.NewString()[:8], "moderator", "auto")
			require.Error(t, err, "role='moderator' must hit CHECK")
			require.Truef(t, isCheckViolation(b.name, err),
				"expected CHECK violation on bad role, got %T: %v", err, err)

			// CHECK violation: bogus avatar_mode.
			_, err = db.ExecContext(ctx, insertUserRoleSQL(b.name),
				"avatar-"+uuid.NewString()[:8], "admin", "unknown")
			require.Error(t, err, "avatar_mode='unknown' must hit CHECK")
			require.Truef(t, isCheckViolation(b.name, err),
				"expected CHECK violation on bad avatar_mode, got %T: %v", err, err)

			// Partial UNIQUE: two NULL oidc_subject rows succeed.
			_, err = db.ExecContext(ctx, insertUserDefaultSQL(b.name),
				"null-oidc-1-"+uuid.NewString()[:8])
			require.NoError(t, err, "NULL oidc_subject #1 must succeed (partial UNIQUE)")
			_, err = db.ExecContext(ctx, insertUserDefaultSQL(b.name),
				"null-oidc-2-"+uuid.NewString()[:8])
			require.NoError(t, err, "NULL oidc_subject #2 must succeed (partial UNIQUE)")

			// Two same non-NULL oidc_subject: second fails.
			sub := "sub-" + uuid.NewString()[:8]
			_, err = db.ExecContext(ctx, insertUserOIDCSubjectSQL(b.name),
				"oidc-"+uuid.NewString()[:8], sub)
			require.NoError(t, err, "first non-NULL oidc_subject must succeed")
			_, err = db.ExecContext(ctx, insertUserOIDCSubjectSQL(b.name),
				"oidc-dup-"+uuid.NewString()[:8], sub)
			require.Error(t, err, "duplicate non-NULL oidc_subject must fail")
			require.Truef(t, isUniqueViolation(b.name, err),
				"expected UNIQUE violation on oidc_subject, got %T: %v", err, err)

			// DOWN — drops 000011, both tables gone.
			require.NoError(t, m.Down())
			for _, table := range []string{"users", "user_instance_tags"} {
				_, err = db.ExecContext(ctx, "SELECT 1 FROM "+table+" LIMIT 1")
				require.Errorf(t, err, "%s should be dropped after Down", table)
			}
		})
	}
}

// TestD17a_UserInstanceTagsCascade exercises both CASCADE directions
// and the composite UNIQUE (instance_name, sonarr_tag_label) guard.
func TestD17a_UserInstanceTagsCascade(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			instance1 := "inst1-" + uuid.NewString()[:8]
			instance2 := "inst2-" + uuid.NewString()[:8]
			alice := "alice-" + uuid.NewString()[:8]
			bobby := "bobby-" + uuid.NewString()[:8]

			// Seed prerequisites.
			_, err := db.ExecContext(ctx, insertSonarrInstanceSQL(b.name),
				instance1, "https://sonarr1.example.com")
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, insertSonarrInstanceSQL(b.name),
				instance2, "https://sonarr2.example.com")
			require.NoError(t, err)

			aliceID := insertUserAndScanID(t, ctx, db, b.name, alice)
			bobbyID := insertUserAndScanID(t, ctx, db, b.name, bobby)
			require.Greater(t, aliceID, int64(0))
			require.Greater(t, bobbyID, int64(0))

			// Happy path: 3 tags total — alice on both instances, bobby on inst1.
			require.NoError(t, insertUserInstanceTag(ctx, db, b.name,
				aliceID, instance1, 101, "sf-alice"))
			require.NoError(t, insertUserInstanceTag(ctx, db, b.name,
				aliceID, instance2, 201, "sf-alice"))
			require.NoError(t, insertUserInstanceTag(ctx, db, b.name,
				bobbyID, instance1, 102, "sf-bobby"))

			// UNIQUE composite: bobby tries to reuse alice's label on inst1.
			err = insertUserInstanceTag(ctx, db, b.name,
				bobbyID, instance1, 999, "sf-alice")
			require.Error(t, err, "duplicate (instance_name, sonarr_tag_label) must fail")
			require.Truef(t, isUniqueViolation(b.name, err),
				"expected UNIQUE violation on (instance_name, label), got %T: %v", err, err)

			// FK violation: orphan user_id.
			err = insertUserInstanceTag(ctx, db, b.name,
				9999999, instance1, 500, "sf-orphan")
			require.Error(t, err, "orphan user_id must fail FK")

			// FK violation: orphan instance_name.
			err = insertUserInstanceTag(ctx, db, b.name,
				aliceID, "orphan-"+uuid.NewString()[:8], 500, "sf-orphan")
			require.Error(t, err, "orphan instance_name must fail FK")

			// CASCADE from user: delete alice → her 2 rows vanish.
			require.Equal(t, 3, countAllUserInstanceTags(t, ctx, db))
			_, err = db.ExecContext(ctx, deleteUserByIDSQL(b.name), aliceID)
			require.NoError(t, err, "delete user should succeed")
			require.Equal(t, 1, countAllUserInstanceTags(t, ctx, db),
				"alice's 2 tag rows should CASCADE on user delete")

			// CASCADE from instance: delete inst1 → bobby's remaining tag goes.
			_, err = db.ExecContext(ctx, deleteSonarrInstanceSQL(b.name), instance1)
			require.NoError(t, err, "delete instance should succeed")
			require.Equal(t, 0, countAllUserInstanceTags(t, ctx, db),
				"bobby's last tag should CASCADE on instance delete")
		})
	}
}

// ---------- SQL builders / helpers ----------

func insertUserDefaultSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO users (username, updated_at) VALUES ($1, now())`
	case "sqlite":
		return `INSERT INTO users (username, updated_at) VALUES (?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func insertUserRoleSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO users (username, role, avatar_mode, updated_at)
		        VALUES ($1, $2, $3, now())`
	case "sqlite":
		return `INSERT INTO users (username, role, avatar_mode, updated_at)
		        VALUES (?, ?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func insertUserOIDCSubjectSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO users (username, oidc_subject, updated_at)
		        VALUES ($1, $2, now())`
	case "sqlite":
		return `INSERT INTO users (username, oidc_subject, updated_at)
		        VALUES (?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func scanUserRoleAvatar(t *testing.T, ctx context.Context, db *sql.DB, driver, username string) (string, string) {
	t.Helper()
	var q string
	switch driver {
	case "postgres":
		q = `SELECT role, avatar_mode FROM users WHERE username = $1`
	case "sqlite":
		q = `SELECT role, avatar_mode FROM users WHERE username = ?`
	default:
		t.Fatalf("unknown driver %q", driver)
	}
	var role, avatar string
	require.NoError(t, db.QueryRowContext(ctx, q, username).Scan(&role, &avatar))
	return role, avatar
}

func insertUserAndScanID(t *testing.T, ctx context.Context, db *sql.DB, driver, username string) int64 {
	t.Helper()
	var id int64
	switch driver {
	case "postgres":
		stmt := `INSERT INTO users (username, updated_at) VALUES ($1, now()) RETURNING id`
		require.NoError(t, db.QueryRowContext(ctx, stmt, username).Scan(&id))
	case "sqlite":
		stmt := `INSERT INTO users (username, updated_at) VALUES (?, CURRENT_TIMESTAMP)`
		res, err := db.ExecContext(ctx, stmt, username)
		require.NoError(t, err)
		id, err = res.LastInsertId()
		require.NoError(t, err)
	default:
		t.Fatalf("unknown driver %q", driver)
	}
	return id
}

func insertUserInstanceTag(ctx context.Context, db *sql.DB, driver string,
	userID int64, instanceName string, tagID int, tagLabel string,
) error {
	var stmt string
	switch driver {
	case "postgres":
		stmt = `INSERT INTO user_instance_tags
		        (user_id, instance_name, sonarr_tag_id, sonarr_tag_label, updated_at)
		        VALUES ($1, $2, $3, $4, now())`
	case "sqlite":
		stmt = `INSERT INTO user_instance_tags
		        (user_id, instance_name, sonarr_tag_id, sonarr_tag_label, updated_at)
		        VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`
	default:
		panic("unknown driver " + driver)
	}
	_, err := db.ExecContext(ctx, stmt, userID, instanceName, tagID, tagLabel)
	return err
}

func deleteUserByIDSQL(driver string) string {
	switch driver {
	case "postgres":
		return `DELETE FROM users WHERE id = $1`
	case "sqlite":
		return `DELETE FROM users WHERE id = ?`
	}
	panic("unknown driver " + driver)
}

func countAllUserInstanceTags(t *testing.T, ctx context.Context, db *sql.DB) int {
	t.Helper()
	var cnt int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_instance_tags`).Scan(&cnt))
	return cnt
}

// isCheckViolation reports whether err is a CHECK constraint violation
// on the active backend. Postgres: SQLSTATE 23514. SQLite: modernc/glebarez
// returns the constraint name in the error message.
func isCheckViolation(driver string, err error) bool {
	if err == nil {
		return false
	}
	switch driver {
	case "postgres":
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return pgErr.Code == "23514"
		}
		return false
	case "sqlite":
		msg := err.Error()
		return strings.Contains(msg, "CHECK constraint failed")
	}
	return false
}

// isUniqueViolation reports whether err is a UNIQUE constraint violation
// on the active backend. Postgres: SQLSTATE 23505. SQLite: "UNIQUE
// constraint failed".
func isUniqueViolation(driver string, err error) bool {
	if err == nil {
		return false
	}
	switch driver {
	case "postgres":
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return pgErr.Code == "23505"
		}
		return false
	case "sqlite":
		msg := err.Error()
		return strings.Contains(msg, "UNIQUE constraint failed")
	}
	return false
}
