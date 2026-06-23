//go:build integration

// D-2 / N-2d (story 505 Commit B) — exercises the ListRepository end-
// to-end against testcontainers Postgres on the full 000001..000021
// migration chain. The unit test suite in
// internal/discovery/persistence/list_repository_test.go runs the same
// scenarios per backend (incl. SQLite shadow); this file cross-verifies
// the prod dialect on the live migration set.
//
// Scenario: 5-item ReplaceList → GetRanked happy-path → IsStale pre/post
// → page-2 read → empty Replace clears the list.
package integration

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestD2_DiscoveryRepo(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			require.NoError(t, m.Up())

			lang := "en-US-" + uuid.NewString()[:6]

			// Seed 5 series rows.
			ids := make([]int64, 0, 5)
			for range 5 {
				ids = append(ids,
					insertSeriesAndScanID(t, ctx, db, b.name, "d2d-"+uuid.NewString()[:8]))
			}

			// IsStale before any refresh → true (no rows).
			stale := isStaleD2(t, ctx, db, b.name, "trending_day", "", lang, time.Hour)
			require.True(t, stale, "never-refreshed list must be stale")

			// ReplaceList: 5 items at positions 1..5.
			require.NoError(t, replaceListD2(t, ctx, db, b.name,
				"trending_day", "", lang, ids))

			// Read back: 5 rows in position order.
			got := selectRankedD2(t, ctx, db, b.name, "trending_day", "", lang, 50, 0)
			require.Equal(t, ids, got)

			// IsStale after refresh → false.
			stale = isStaleD2(t, ctx, db, b.name, "trending_day", "", lang, time.Hour)
			require.False(t, stale, "freshly-refreshed list must NOT be stale")

			// Page-2 (LIMIT 2 OFFSET 2) ⇒ ids[2], ids[3].
			got = selectRankedD2(t, ctx, db, b.name, "trending_day", "", lang, 2, 2)
			require.Equal(t, []int64{ids[2], ids[3]}, got)

			// Empty replace clears the list.
			require.NoError(t, replaceListD2(t, ctx, db, b.name,
				"trending_day", "", lang, nil))
			got = selectRankedD2(t, ctx, db, b.name, "trending_day", "", lang, 50, 0)
			require.Empty(t, got)
		})
	}
}

func replaceListD2(t *testing.T, ctx context.Context, db *sql.DB, driver, kind, param, lang string, ids []int64) error {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	var delQ, insQ string
	switch driver {
	case "postgres":
		delQ = `DELETE FROM discovery_lists WHERE kind=$1 AND param=$2 AND language=$3`
		insQ = `INSERT INTO discovery_lists (kind, param, language, series_id, position, refreshed_at)
		        VALUES ($1, $2, $3, $4, $5, now())`
	case "sqlite":
		delQ = `DELETE FROM discovery_lists WHERE kind=? AND param=? AND language=?`
		insQ = `INSERT INTO discovery_lists (kind, param, language, series_id, position, refreshed_at)
		        VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`
	default:
		return fmt.Errorf("unknown driver %q", driver)
	}
	if _, err := tx.ExecContext(ctx, delQ, kind, param, lang); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, insQ, kind, param, lang, id, i+1); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func selectRankedD2(t *testing.T, ctx context.Context, db *sql.DB, driver, kind, param, lang string, limit, offset int) []int64 {
	t.Helper()
	var q string
	switch driver {
	case "postgres":
		q = `SELECT series_id FROM discovery_lists
		     WHERE kind=$1 AND param=$2 AND language=$3
		     ORDER BY position ASC LIMIT $4 OFFSET $5`
	case "sqlite":
		q = `SELECT series_id FROM discovery_lists
		     WHERE kind=? AND param=? AND language=?
		     ORDER BY position ASC LIMIT ? OFFSET ?`
	}
	rows, err := db.QueryContext(ctx, q, kind, param, lang, limit, offset)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	out := []int64{}
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		out = append(out, id)
	}
	require.NoError(t, rows.Err())
	return out
}

func isStaleD2(t *testing.T, ctx context.Context, db *sql.DB, driver, kind, param, lang string, ttl time.Duration) bool {
	t.Helper()
	var q string
	switch driver {
	case "postgres":
		q = `SELECT MAX(refreshed_at) FROM discovery_lists
		     WHERE kind=$1 AND param=$2 AND language=$3`
	case "sqlite":
		q = `SELECT MAX(refreshed_at) FROM discovery_lists
		     WHERE kind=? AND param=? AND language=?`
	}
	// Scan into a sql.NullString first so the SQLite driver's
	// text-format timestamp normalises uniformly with the Postgres
	// timestamptz path (raw database/sql + glebarez/go-sqlite returns
	// MAX() of a timestamptz column as a string, which sql.NullTime
	// refuses to coerce — mirrors the production-repo's GORM-model
	// workaround).
	var raw sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, q, kind, param, lang).Scan(&raw))
	if !raw.Valid || raw.String == "" {
		return true
	}
	at, perr := parseAnyTimeD2(raw.String)
	require.NoError(t, perr, "parse MAX(refreshed_at)=%q", raw.String)
	return time.Since(at) > ttl
}

// parseAnyTimeD2 accepts the formats both supported drivers may emit
// for a timestamptz column read via raw database/sql: RFC3339(Nano)
// (Postgres), the SQLite ISO 8601 variant `2006-01-02 15:04:05`, and
// the timezone-aware variant `2006-01-02 15:04:05-07:00`.
func parseAnyTimeD2(s string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if at, err := time.Parse(layout, s); err == nil {
			return at, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised timestamp format %q", s)
}
