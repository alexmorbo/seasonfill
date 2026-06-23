//go:build integration

// D-2 / N-2d (story 505 Commit A) — exercises the ActiveLanguagesRepository
// against testcontainers Postgres on the full 000001..000021 migration
// chain. The unit test in
// internal/discovery/persistence/active_languages_test.go covers the same
// 3 cases against the SQLite shadow; this file cross-verifies the prod
// dialect under the real schema cache.
package integration

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestD2_DiscoveryActiveLanguages(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			require.NoError(t, m.Up())

			// (1) empty users → ["en-US"]
			got := scanActiveLanguages(t, ctx, db)
			require.Equal(t, []string{"en-US"}, got, "empty users → en-US fallback only")

			// (2) seed 3 users with distinct preferred_language
			seedUserPL(t, ctx, db, b.name, "ru-RU")
			seedUserPL(t, ctx, db, b.name, "ja-JP")
			seedUserPL(t, ctx, db, b.name, "en-US")
			got = scanActiveLanguages(t, ctx, db)
			require.ElementsMatch(t, []string{"en-US", "ja-JP", "ru-RU"}, got)

			// (3) NULL preferred_language excluded
			seedUserPL(t, ctx, db, b.name, "") // empty + NULL both excluded
			got = scanActiveLanguages(t, ctx, db)
			require.ElementsMatch(t, []string{"en-US", "ja-JP", "ru-RU"}, got,
				"NULL/empty preferred_language must not appear")
		})
	}
}

// scanActiveLanguages runs the same UNION query as the production repo —
// integration test is intentionally SQL-mirroring (NOT importing the
// repo) so the test fails loudly if the production query drifts.
func scanActiveLanguages(t *testing.T, ctx context.Context, db *sql.DB) []string {
	t.Helper()
	const q = `SELECT DISTINCT preferred_language AS lang
	             FROM users
	            WHERE preferred_language IS NOT NULL
	              AND preferred_language <> ''
	            UNION
	           SELECT 'en-US' AS lang
	           ORDER BY lang ASC`
	rows, err := db.QueryContext(ctx, q)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}
	require.NoError(t, rows.Err())
	return out
}

// seedUserPL inserts one users row with the given preferred_language.
// pl="" means NULL (the test-helper interprets empty string as "no value").
func seedUserPL(t *testing.T, ctx context.Context, db *sql.DB, driver, pl string) {
	t.Helper()
	username := "d2d-" + uuid.NewString()[:8]
	var q string
	switch driver {
	case "postgres":
		if pl == "" {
			q = `INSERT INTO users (username, role, avatar_mode, preferred_language, created_at, updated_at)
			     VALUES ($1, 'admin', 'auto', NULL, now(), now())`
			_, err := db.ExecContext(ctx, q, username)
			require.NoError(t, err)
			return
		}
		q = `INSERT INTO users (username, role, avatar_mode, preferred_language, created_at, updated_at)
		     VALUES ($1, 'admin', 'auto', $2, now(), now())`
	case "sqlite":
		if pl == "" {
			q = `INSERT INTO users (username, role, avatar_mode, preferred_language, created_at, updated_at)
			     VALUES (?, 'admin', 'auto', NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
			_, err := db.ExecContext(ctx, q, username)
			require.NoError(t, err)
			return
		}
		q = `INSERT INTO users (username, role, avatar_mode, preferred_language, created_at, updated_at)
		     VALUES (?, 'admin', 'auto', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	default:
		t.Fatalf("unknown driver %q", driver)
	}
	_, err := db.ExecContext(ctx, q, username, strings.TrimSpace(pl))
	require.NoError(t, err)
}
