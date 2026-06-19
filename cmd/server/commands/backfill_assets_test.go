package commands

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// quietLogger discards log output for tests that only care about the
// returned row counts.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// newBackfillTestDB stands up an in-memory sqlite with a minimal
// `series` table mirroring the production schema's columns this CLI
// touches: id, tmdb_id (nullable), hydration ('full' | 'partial' |
// 'stub'), poster_asset (nullable), backdrop_asset (nullable). The
// MaxOpenConns=1 constraint mirrors setupTestDB in
// infrastructure/database/repositories — without it every connection
// gets its own private :memory: DB and the test would see an empty
// table.
func newBackfillTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	require.NoError(t, db.Exec(`
		CREATE TABLE series (
			id INTEGER PRIMARY KEY,
			tmdb_id INTEGER,
			hydration TEXT NOT NULL,
			poster_asset TEXT,
			backdrop_asset TEXT
		)
	`).Error)
	return db
}

// seedRow is a compact insert helper.
func seedRow(t *testing.T, db *gorm.DB, id int, tmdbID *int, hydration string, poster, backdrop *string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO series (id, tmdb_id, hydration, poster_asset, backdrop_asset) VALUES (?, ?, ?, ?, ?)`,
		id, tmdbID, hydration, poster, backdrop,
	).Error)
}

func hydrationOf(t *testing.T, db *gorm.DB, id int) string {
	t.Helper()
	var hyd string
	require.NoError(t, db.Raw("SELECT hydration FROM series WHERE id = ?", id).Scan(&hyd).Error)
	return hyd
}

// TestRunBackfillAssets_DemotesBackdropNullRowsOnly — the rescue CLI
// must demote only the rows that are (a) tmdb_id NOT NULL, (b) hydration
// = 'full', (c) the named asset column IS NULL. Rows missing any of
// those conditions are left alone.
func TestRunBackfillAssets_DemotesBackdropNullRowsOnly(t *testing.T) {
	t.Parallel()
	db := newBackfillTestDB(t)
	// Row 1: full hydration, no backdrop → SHOULD demote.
	seedRow(t, db, 1, new(100), "full", new("/p.jpg"), nil)
	// Row 2: full hydration, no backdrop → SHOULD demote.
	seedRow(t, db, 2, new(101), "full", new("/p.jpg"), nil)
	// Row 3: full hydration, has backdrop → stays.
	seedRow(t, db, 3, new(102), "full", new("/p.jpg"), new("/b.jpg"))
	// Row 4: partial hydration, no backdrop → stays (not the
	// recovery sweep's population).
	seedRow(t, db, 4, new(103), "partial", nil, nil)
	// Row 5: no tmdb_id (stub) → stays.
	seedRow(t, db, 5, nil, "full", nil, nil)

	n, err := runBackfillAssets(context.Background(), db, AssetKindBackdrop, false, quietLogger())
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	assert.Equal(t, "partial", hydrationOf(t, db, 1))
	assert.Equal(t, "partial", hydrationOf(t, db, 2))
	assert.Equal(t, "full", hydrationOf(t, db, 3), "row with backdrop must NOT be demoted")
	assert.Equal(t, "partial", hydrationOf(t, db, 4), "pre-existing partial untouched")
	assert.Equal(t, "full", hydrationOf(t, db, 5), "row missing tmdb_id must NOT be demoted")
}

// TestRunBackfillAssets_DryRunNoMutation — --dry-run counts but never
// modifies the DB.
func TestRunBackfillAssets_DryRunNoMutation(t *testing.T) {
	t.Parallel()
	db := newBackfillTestDB(t)
	seedRow(t, db, 1, new(100), "full", new("/p.jpg"), nil)
	seedRow(t, db, 2, new(101), "full", new("/p.jpg"), nil)
	seedRow(t, db, 3, new(102), "full", new("/p.jpg"), new("/b.jpg"))

	n, err := runBackfillAssets(context.Background(), db, AssetKindBackdrop, true, quietLogger())
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "dry-run must report the count it WOULD demote")

	// Verify nothing changed.
	assert.Equal(t, "full", hydrationOf(t, db, 1))
	assert.Equal(t, "full", hydrationOf(t, db, 2))
	assert.Equal(t, "full", hydrationOf(t, db, 3))
}

// TestRunBackfillAssets_PosterKind — verify --kind=poster targets the
// poster_asset column. Mirrors the backdrop test for the symmetric
// path.
func TestRunBackfillAssets_PosterKind(t *testing.T) {
	t.Parallel()
	db := newBackfillTestDB(t)
	seedRow(t, db, 1, new(100), "full", nil, new("/b.jpg"))
	seedRow(t, db, 2, new(101), "full", new("/p.jpg"), nil)

	n, err := runBackfillAssets(context.Background(), db, AssetKindPoster, false, quietLogger())
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.Equal(t, "partial", hydrationOf(t, db, 1), "poster-null row must be demoted")
	assert.Equal(t, "full", hydrationOf(t, db, 2), "row with poster but no backdrop must NOT be demoted under --kind=poster")
}

// TestBackfillAssets_RejectsInvalidKind — argparse rejects --kind=logo
// before any DB call lands.
func TestBackfillAssets_RejectsInvalidKind(t *testing.T) {
	t.Parallel()
	err := BackfillAssets([]string{"--kind", "logo"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --kind")
}

// TestBackfillAssets_RejectsMissingKind — --kind is mandatory.
func TestBackfillAssets_RejectsMissingKind(t *testing.T) {
	t.Parallel()
	err := BackfillAssets([]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--kind")
}
