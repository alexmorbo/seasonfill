//go:build integration

// pickers_test.go — testcontainers Postgres coverage for the genre /
// network picker readers (story 507 N-2f). Per-test fresh DB via
// testhelpers.StartPostgres + the canonical migration chain applied
// inline (mirrors tests/integration/d1_helpers_test.go.openD1Postgres).
//
// Coverage:
//   - Genres: 3 inserted, 1 with NULL tmdb_id (filtered), 1 with both
//     en-US + ru-RU i18n, 1 with only en-US. Request ru-RU →
//     Russian for #1, English fallback for #2; #3 dropped.
//   - Empty catalog → empty slice (never nil).
//   - Networks: 3 inserted, 1 with NULL tmdb_id (filtered), sorted
//     by name.
package persistence_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	discopersistence "github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestGenresPickerRepo_List_RU_FallsBackToEN(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	db, raw := openPickerDB(t)
	defer raw.Close()

	suffix := uuid.NewString()[:8]
	// Genre 1 — tmdb_id 1000, "Drama" en + "Драма" ru.
	id1 := insertGenre(t, ctx, raw, ptrInt(1000))
	insertGenreI18n(t, ctx, raw, id1, "en-US", "Drama-"+suffix)
	insertGenreI18n(t, ctx, raw, id1, "ru-RU", "Драма-"+suffix)
	// Genre 2 — tmdb_id 1001, en only.
	id2 := insertGenre(t, ctx, raw, ptrInt(1001))
	insertGenreI18n(t, ctx, raw, id2, "en-US", "Comedy-"+suffix)
	// Genre 3 — NULL tmdb_id → filtered out at SQL level.
	id3 := insertGenre(t, ctx, raw, nil)
	insertGenreI18n(t, ctx, raw, id3, "en-US", "Orphan-"+suffix)

	r := discopersistence.NewGenresPickerRepo(db)
	got, err := r.List(ctx, "ru-RU")
	require.NoError(t, err)

	want := []discopersistence.GenrePickItem{
		{ID: 1001, Name: "Comedy-" + suffix},
		{ID: 1000, Name: "Драма-" + suffix},
	}
	require.Equal(t, want, got)
}

func TestGenresPickerRepo_List_Empty(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	db, raw := openPickerDB(t)
	defer raw.Close()

	r := discopersistence.NewGenresPickerRepo(db)
	got, err := r.List(ctx, "en-US")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestNetworksPickerRepo_List(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	db, raw := openPickerDB(t)
	defer raw.Close()

	suffix := uuid.NewString()[:8]
	insertNetwork(t, ctx, raw, ptrInt(2000), "ZZZ-Netflix-"+suffix)
	insertNetwork(t, ctx, raw, ptrInt(2001), "AAA-HBO-"+suffix)
	insertNetwork(t, ctx, raw, nil, "Orphan-"+suffix) // filtered

	r := discopersistence.NewNetworksPickerRepo(db)
	got, err := r.List(ctx, "en-US")
	require.NoError(t, err)
	require.Equal(t, []discopersistence.NetworkPickItem{
		{ID: 2001, Name: "AAA-HBO-" + suffix},
		{ID: 2000, Name: "ZZZ-Netflix-" + suffix},
	}, got)
}

// openPickerDB returns a *gorm.DB + raw *sql.DB pointing at a fresh
// testcontainers Postgres database with the full migration chain
// applied. Mirrors tests/integration/d1_helpers_test.go.openD1Postgres
// inlined (the helper lives in the integration package which is not
// importable from here).
func openPickerDB(t *testing.T) (*gorm.DB, *sql.DB) {
	t.Helper()
	pc := testhelpers.StartPostgres(t)

	var raw [8]byte
	_, err := rand.Read(raw[:])
	require.NoError(t, err)
	dbName := "seasonfill_pickers_" + hex.EncodeToString(raw[:])

	admin, err := sql.Open("pgx", pc.DSN)
	require.NoError(t, err)
	defer func() { _ = admin.Close() }()

	_, err = admin.ExecContext(context.Background(),
		fmt.Sprintf("CREATE DATABASE %q", dbName))
	require.NoError(t, err)

	dsn := swapPGDBName(pc.DSN, dbName)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingCancel()
	require.NoError(t, db.PingContext(pingCtx))

	driver, err := migratepg.WithInstance(db, &migratepg.Config{
		MigrationsTable: "schema_migrations",
	})
	require.NoError(t, err)

	src, err := (&file.File{}).Open("file://" + migrationsDir(t, "postgres"))
	require.NoError(t, err)

	m, err := migrate.NewWithInstance("file", src, "postgres", driver)
	require.NoError(t, err)
	require.NoError(t, m.Up())

	t.Cleanup(func() {
		_, _ = m.Close()
		_ = db.Close()
		drop, derr := sql.Open("pgx", pc.DSN)
		if derr != nil {
			return
		}
		defer func() { _ = drop.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = drop.ExecContext(ctx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			   WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName)
		_, _ = drop.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q", dbName))
	})

	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	require.NoError(t, err)
	return gdb, db
}

// migrationsDir resolves the absolute path to
// infrastructure/database/migrations/{postgres,sqlite}/.
// runtime.Caller(0) gives this test file; we walk up to the repo root.
func migrationsDir(t *testing.T, dialect string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// this file: internal/discovery/persistence/pickers_test.go
	// Dir(file) → internal/discovery/persistence
	// repo root: ../../..
	repoRoot := filepath.Join(filepath.Dir(file), "..", "..", "..")
	return filepath.Join(repoRoot, "infrastructure", "database", "migrations", dialect)
}

// swapPGDBName mirrors d1SwapPGDBName from tests/integration helpers.
func swapPGDBName(dsn, dbName string) string {
	scheme := "://"
	idx := strings.Index(dsn, scheme)
	if idx < 0 {
		return dsn
	}
	rest := dsn[idx+len(scheme):]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		sep := "?"
		if strings.Index(dsn, "?") >= 0 {
			sep = "&"
		}
		return dsn + sep + "dbname=" + dbName
	}
	pathStart := idx + len(scheme) + slash + 1
	tail := dsn[pathStart:]
	q := strings.Index(tail, "?")
	if q < 0 {
		return dsn[:pathStart] + dbName
	}
	return dsn[:pathStart] + dbName + tail[q:]
}

func ptrInt(v int) *int { return &v }

func insertGenre(t *testing.T, ctx context.Context, db *sql.DB, tmdbID *int) int64 {
	t.Helper()
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO genres (tmdb_id, created_at, updated_at) VALUES ($1, now(), now()) RETURNING id`,
		tmdbID).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertGenreI18n(t *testing.T, ctx context.Context, db *sql.DB, genreID int64, lang, name string) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO genres_i18n (genre_id, language, name, updated_at) VALUES ($1, $2, $3, now())`,
		genreID, lang, name)
	require.NoError(t, err)
}

func insertNetwork(t *testing.T, ctx context.Context, db *sql.DB, tmdbID *int, name string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO networks (tmdb_id, name, created_at, updated_at) VALUES ($1, $2, now(), now()) RETURNING id`,
		tmdbID, name).Scan(&id)
	require.NoError(t, err)
	return id
}
