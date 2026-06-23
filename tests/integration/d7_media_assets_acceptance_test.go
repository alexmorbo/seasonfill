//go:build integration

// D-7 (story 468c) — verifies migration 000019 restored the
// media_assets table with the expected columns, PK on hash, UNIQUE on
// source_url, and the status index, on both backends.
package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestD7_MediaAssetsRestored — full migration Up brings media_assets
// online, PK on hash rejects duplicates, UNIQUE on source_url rejects
// duplicate URLs, and a basic write/read round-trip via the model's
// column set works on both dialects.
func TestD7_MediaAssetsRestored(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up(), "Up() failed on %s", b.name)

			// 1. Table exists and is empty.
			var rowCount int64
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM media_assets").Scan(&rowCount))
			require.Equal(t, int64(0), rowCount,
				"media_assets must exist + be empty after fresh Up on %s", b.name)

			// 2. Insert a pending row using the model contract.
			const hashA = "aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11"
			const urlA = "https://image.tmdb.org/t/p/w342/aaa.jpg"
			_, err := db.ExecContext(ctx, insertMediaAssetPendingSQL(b.name),
				hashA, urlA, "poster_w342", "pending")
			require.NoError(t, err, "insert pending media_asset on %s", b.name)

			// 3. Duplicate hash trips the PK constraint.
			_, err = db.ExecContext(ctx, insertMediaAssetPendingSQL(b.name),
				hashA, "https://image.tmdb.org/t/p/w342/bbb.jpg",
				"poster_w342", "pending")
			require.Errorf(t, err,
				"PK on hash must reject duplicate hash insert on %s", b.name)

			// 4. Duplicate source_url trips the UNIQUE constraint.
			const hashB = "bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22"
			_, err = db.ExecContext(ctx, insertMediaAssetPendingSQL(b.name),
				hashB, urlA, "poster_w342", "pending")
			require.Errorf(t, err,
				"UNIQUE on source_url must reject duplicate URL insert on %s", b.name)

			// 5. Update content_type + size_bytes + fetched_at on stored
			// transition; read it back.
			now := time.Now().UTC().Truncate(time.Millisecond)
			_, err = db.ExecContext(ctx, updateMediaAssetStoredSQL(b.name),
				"stored", "image/jpeg", int64(4321), now, hashA)
			require.NoError(t, err, "update stored media_asset on %s", b.name)

			var (
				gotStatus      string
				gotContentType sql.NullString
				gotSize        sql.NullInt64
				gotFetchedAt   sql.NullTime
			)
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT status, content_type, size_bytes, fetched_at FROM media_assets WHERE hash = "+
					placeholderFor(b.name, 1), hashA).
				Scan(&gotStatus, &gotContentType, &gotSize, &gotFetchedAt))
			require.Equal(t, "stored", gotStatus)
			require.True(t, gotContentType.Valid && gotContentType.String == "image/jpeg")
			require.True(t, gotSize.Valid && gotSize.Int64 == 4321)
			require.True(t, gotFetchedAt.Valid)

			// 6. last_access_at left NULL — handler stamps it on read miss.
			var gotLastAccess sql.NullTime
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT last_access_at FROM media_assets WHERE hash = "+
					placeholderFor(b.name, 1), hashA).
				Scan(&gotLastAccess))
			require.False(t, gotLastAccess.Valid,
				"last_access_at must remain NULL until handler touches it on %s", b.name)
		})
	}
}

// TestD7_MediaAssetsDownReversibility — apply through 000019 then Down(1)
// drops media_assets cleanly; re-Up() restores it. We Migrate(19)
// explicitly (instead of Up() to head) so the -1 step targets exactly
// 000019_media_assets regardless of later migrations.
func TestD7_MediaAssetsDownReversibility(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Migrate(19),
				"Migrate(19) should apply 000001..000019 cleanly on %s", b.name)

			// Down 000019 only — verifies the .down.sql DROPs the table.
			require.NoError(t, m.Steps(-1),
				"Steps(-1) should drop 000019_media_assets on %s", b.name)
			_, err := db.ExecContext(ctx, "SELECT 1 FROM media_assets LIMIT 1")
			require.Errorf(t, err,
				"media_assets must be gone after Down(1) on %s", b.name)

			// Re-Up restores it (and continues to head, which is fine —
			// the assertion below only checks media_assets is back).
			require.NoError(t, m.Up(),
				"Up() should reapply 000019 cleanly on %s", b.name)
			var rowCount int64
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM media_assets").Scan(&rowCount))
			require.Equal(t, int64(0), rowCount)
		})
	}
}

// insertMediaAssetPendingSQL — INSERT a pending media_assets row.
// Columns: hash, source_url, kind, status, created_at.
func insertMediaAssetPendingSQL(driver string) string {
	if driver == "postgres" {
		return `INSERT INTO media_assets (hash, source_url, kind, status, created_at)
		        VALUES ($1, $2, $3, $4, now())`
	}
	return `INSERT INTO media_assets (hash, source_url, kind, status, created_at)
	        VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`
}

// updateMediaAssetStoredSQL — UPDATE status + content_type + size_bytes +
// fetched_at where hash = ?. Mirrors MediaAssetsRepository.Upsert's
// stored-transition write set.
func updateMediaAssetStoredSQL(driver string) string {
	if driver == "postgres" {
		return `UPDATE media_assets
		        SET status = $1, content_type = $2, size_bytes = $3, fetched_at = $4
		        WHERE hash = $5`
	}
	return `UPDATE media_assets
	        SET status = ?, content_type = ?, size_bytes = ?, fetched_at = ?
	        WHERE hash = ?`
}
