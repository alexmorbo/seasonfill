package repositories

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// Story 314 (B-6) — repository test fixture speedup.
//
// Background: setupTestDB used to call database.Migrate(db) on every test,
// running all 39 sqlite migrations through golang-migrate end-to-end per call.
// With ~300 tests in this package × 39 migrations, that was the dominant
// CI Unit-job cost (~13 min wall clock under -race).
//
// New approach: at TestMain we open one template :memory: DB, run all
// migrations once, snapshot the resulting schema as a flat slice of
// CREATE statements (sqlite_master), and also snapshot any seed rows
// migrations inserted (e.g. the singleton app_settings row from 000036).
// Cache both under sync.Once. Every subsequent setupTestDB(t) call opens
// a fresh :memory: GORM instance, replays the cached DDL in a single
// batch, then replays the seed rows — no migrate library overhead, no
// schema_migrations bookkeeping, no driver re-instantiation. Tests still
// get fully isolated databases (each :memory: handle is its own
// connection), so t.Parallel() stays safe with no further isolation
// work.
//
// Production code (cmd/server/main.go bootstrap calling database.Migrate)
// is untouched.
// seedRowGroup is one table's worth of seed data captured from the template
// DB. We carry column ordering with the values so the replay INSERT lines
// columns up by position, not by relying on map iteration order.
type seedRowGroup struct {
	table   string
	columns []string
	rows    [][]any
}

var (
	schemaCacheOnce  sync.Once
	schemaCacheDDL   []string
	schemaCacheSeeds []seedRowGroup
	schemaCacheErr   error
)

// TestMain owns the one-shot template-schema build. We pay the migration
// cost exactly once per `go test` invocation of this package. If schema
// extraction fails we fail-fast — no point running tests against an
// unknown schema.
func TestMain(m *testing.M) {
	buildSchemaCache()
	if schemaCacheErr != nil {
		fmt.Fprintf(os.Stderr, "repositories test fixture: build schema cache failed: %v\n", schemaCacheErr)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// buildSchemaCache opens a throwaway template DB, runs all migrations
// once, then snapshots the resulting schema as a slice of CREATE
// statements ordered for replay (tables first, then indexes; sqlite
// itself enforces the table-must-exist-before-index rule).
//
// We deliberately exclude:
//   - sqlite_sequence (auto-created by AUTOINCREMENT, do NOT recreate)
//   - sqlite_* internal tables
//   - schema_migrations (golang-migrate bookkeeping; tests don't need it
//     and re-running Migrate() against the replayed schema is not a goal)
//   - any index/trigger/view whose tbl_name is schema_migrations (e.g. the
//     version_unique index golang-migrate creates) — they reference the
//     excluded table and would fail on replay.
//
// The cached slice is read-only after this function returns, so it's
// safe to share across parallel goroutines without further locking.
func buildSchemaCache() {
	schemaCacheOnce.Do(func() {
		template, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		if err != nil {
			schemaCacheErr = fmt.Errorf("open template db: %w", err)
			return
		}
		sqlDB, err := template.DB()
		if err != nil {
			schemaCacheErr = fmt.Errorf("get sql.DB from template: %w", err)
			return
		}
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		defer sqlDB.Close()

		if err := database.Migrate(template); err != nil {
			schemaCacheErr = fmt.Errorf("migrate template: %w", err)
			return
		}

		// Extract every user-defined schema object. We rely on sqlite_master
		// being authoritative: type IN ('table','index','trigger','view').
		// Filter out sqlite_* internal entries and the migrate bookkeeping
		// table.
		type row struct {
			Type string `gorm:"column:type"`
			Name string `gorm:"column:name"`
			SQL  string `gorm:"column:sql"`
		}
		var rows []row
		if err := template.Raw(`
			SELECT type, name, sql
			FROM sqlite_master
			WHERE sql IS NOT NULL
			  AND name NOT LIKE 'sqlite_%'
			  AND name != 'schema_migrations'
			  AND (tbl_name IS NULL OR tbl_name != 'schema_migrations')
		`).Scan(&rows).Error; err != nil {
			schemaCacheErr = fmt.Errorf("scan sqlite_master: %w", err)
			return
		}

		// Order: tables before indexes/triggers/views. Within each type,
		// stable name sort for deterministic replay (helps when a test
		// suite needs to diff schemas).
		typeRank := map[string]int{"table": 0, "view": 1, "index": 2, "trigger": 3}
		sort.SliceStable(rows, func(i, j int) bool {
			if typeRank[rows[i].Type] != typeRank[rows[j].Type] {
				return typeRank[rows[i].Type] < typeRank[rows[j].Type]
			}
			return rows[i].Name < rows[j].Name
		})

		ddl := make([]string, 0, len(rows))
		for _, r := range rows {
			ddl = append(ddl, r.SQL)
		}
		schemaCacheDDL = ddl

		// Snapshot seed data. A few migrations (e.g. 000036_app_settings)
		// INSERT singleton rows that tests rely on. We capture every row
		// from every user table in the template DB so the replayed fixture
		// is data-identical to a freshly migrated DB. Tables with no rows
		// (the common case) contribute nothing.
		tableNames := make([]string, 0, len(rows))
		for _, r := range rows {
			if r.Type == "table" {
				tableNames = append(tableNames, r.Name)
			}
		}
		sort.Strings(tableNames)

		seeds := make([]seedRowGroup, 0)
		for _, tbl := range tableNames {
			// Stable column order via PRAGMA table_info(<tbl>).
			type colInfo struct {
				Name string `gorm:"column:name"`
				Cid  int    `gorm:"column:cid"`
			}
			var cols []colInfo
			if err := template.Raw(fmt.Sprintf(`PRAGMA table_info(%q)`, tbl)).Scan(&cols).Error; err != nil {
				schemaCacheErr = fmt.Errorf("pragma table_info(%s): %w", tbl, err)
				return
			}
			if len(cols) == 0 {
				continue
			}
			sort.SliceStable(cols, func(i, j int) bool { return cols[i].Cid < cols[j].Cid })
			colNames := make([]string, len(cols))
			for i, c := range cols {
				colNames[i] = c.Name
			}

			var data []map[string]any
			if err := template.Raw(fmt.Sprintf(`SELECT * FROM %q`, tbl)).Scan(&data).Error; err != nil {
				schemaCacheErr = fmt.Errorf("snapshot rows from %s: %w", tbl, err)
				return
			}
			if len(data) == 0 {
				continue
			}
			grp := seedRowGroup{table: tbl, columns: colNames, rows: make([][]any, 0, len(data))}
			for _, row := range data {
				vals := make([]any, len(colNames))
				for i, name := range colNames {
					vals[i] = row[name]
				}
				grp.rows = append(grp.rows, vals)
			}
			seeds = append(seeds, grp)
		}
		schemaCacheSeeds = seeds
	})
}

// setupTestDB returns a fresh :memory: GORM DB with the full migrated
// schema and any migration-seeded rows, replayed from the cache built
// once in TestMain.
//
// Contract is identical to the pre-B-6 helper: each call returns a
// brand-new DB handle in the same state a freshly migrated DB would be
// in. Tests can use t.Parallel() freely because each :memory: handle is
// fully isolated.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	if schemaCacheErr != nil {
		t.Fatalf("schema cache build failed: %v", schemaCacheErr)
	}
	if len(schemaCacheDDL) == 0 {
		t.Fatalf("schema cache is empty; TestMain may not have run")
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	// Single connection: same constraint as the pre-B-6 helper — needed so
	// every query lands on the same in-memory database instance (each
	// connection in :memory: mode otherwise gets its own private DB).
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	for _, stmt := range schemaCacheDDL {
		if err := db.Exec(stmt).Error; err != nil {
			require.NoError(t, fmt.Errorf("replay ddl %q: %w", firstLine(stmt), err))
		}
	}

	// Replay seed data captured from the template DB so tests see the
	// exact same starting state a freshly migrated DB would.
	for _, grp := range schemaCacheSeeds {
		quoted := make([]string, len(grp.columns))
		placeholders := make([]string, len(grp.columns))
		for i, c := range grp.columns {
			quoted[i] = fmt.Sprintf("%q", c)
			placeholders[i] = "?"
		}
		stmt := fmt.Sprintf(
			"INSERT INTO %q (%s) VALUES (%s)",
			grp.table,
			joinComma(quoted),
			joinComma(placeholders),
		)
		for _, vals := range grp.rows {
			if err := db.Exec(stmt, vals...).Error; err != nil {
				require.NoError(t, fmt.Errorf("replay seed row into %s: %w", grp.table, err))
			}
		}
	}

	return db
}

func joinComma(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	n := len(parts) - 1
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for i, p := range parts {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, p...)
	}
	return string(out)
}

// firstLine returns the first non-empty line of s, trimmed. Used only
// for error messages when DDL replay fails — gives the operator a hint
// which CREATE statement blew up without dumping the whole blob.
func firstLine(s string) string {
	for _, line := range splitLines(s) {
		if t := trimSpace(line); t != "" {
			return t
		}
	}
	return s
}

func splitLines(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}

func newScanRecord(id uuid.UUID) ports.ScanRecord {
	now := time.Now().UTC().Truncate(time.Second)
	return ports.ScanRecord{
		ID:           id,
		InstanceName: "main",
		Trigger:      "manual",
		StartedAt:    now,
		Status:       "running",
		DryRun:       true,
	}
}

func TestScanRepository_Create_Then_GetByID(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	id := uuid.New()
	rec := newScanRecord(id)
	require.NoError(t, repo.Create(ctx, rec))

	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, domain.InstanceName("main"), got.InstanceName)
	assert.Equal(t, "running", got.Status)
	assert.True(t, got.DryRun)
}

func TestScanRepository_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)

	missing := uuid.New()
	_, err := repo.GetByID(context.Background(), missing)
	require.Error(t, err)

	var typedErr *sharedErrors.ScanRunNotFoundError
	require.True(t, errors.As(err, &typedErr),
		"GetByID NotFound must expose typed ScanRunNotFoundError via errors.As")
	assert.Equal(t, missing, typedErr.ID)
}

func TestScanRepository_Update(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	id := uuid.New()
	rec := newScanRecord(id)
	require.NoError(t, repo.Create(ctx, rec))

	finished := time.Now().UTC().Truncate(time.Second)
	rec.Status = "completed"
	rec.SeriesScanned = 12
	rec.CandidatesFound = 5
	rec.FinishedAt = &finished

	require.NoError(t, repo.Update(ctx, rec))

	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "completed", got.Status)
	assert.Equal(t, 12, got.SeriesScanned)
	assert.Equal(t, 5, got.CandidatesFound)
	require.NotNil(t, got.FinishedAt)
}

// TestScanRepository_Update_PreservesCreatedAt is the Part B regression:
// the completion Update used to do a full-row Save that zeroed the
// GORM-managed created_at (toScanModel leaves it at the zero value). The
// fix Omits CreatedAt from the Save so the original Create timestamp
// survives. Assert created_at is non-zero AND unchanged across the update.
func TestScanRepository_Update_PreservesCreatedAt(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	id := uuid.New()
	require.NoError(t, repo.Create(ctx, newScanRecord(id)))

	// Read the created_at GORM auto-set on Create.
	var beforeCreatedAt time.Time
	require.NoError(t, db.Table("scan_runs").
		Where("id = ?", id.String()).
		Select("created_at").Scan(&beforeCreatedAt).Error)
	require.False(t, beforeCreatedAt.IsZero(), "Create must auto-set created_at")

	// Complete the scan via the full-row Update path.
	finished := time.Now().UTC().Truncate(time.Second)
	rec := newScanRecord(id)
	rec.Status = "completed"
	rec.SeriesScanned = 7
	rec.FinishedAt = &finished
	require.NoError(t, repo.Update(ctx, rec))

	// created_at must survive the update.
	var afterCreatedAt time.Time
	require.NoError(t, db.Table("scan_runs").
		Where("id = ?", id.String()).
		Select("created_at").Scan(&afterCreatedAt).Error)
	assert.False(t, afterCreatedAt.IsZero(),
		"Update must not zero created_at")
	assert.WithinDuration(t, beforeCreatedAt, afterCreatedAt, time.Second,
		"Update must preserve the original created_at")

	// And the mutable fields did get written.
	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "completed", got.Status)
	assert.Equal(t, 7, got.SeriesScanned)
	require.NotNil(t, got.FinishedAt)
}

func TestScanRepository_MarkAborted(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	id := uuid.New()
	rec := newScanRecord(id)
	require.NoError(t, repo.Create(ctx, rec))

	require.NoError(t, repo.MarkAborted(ctx, id, "shutdown timeout"))

	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "aborted", got.Status)
	assert.Equal(t, "shutdown timeout", got.ErrorMessage)
}

func TestScanRepository_MarkAborted_UnknownID_Succeeds(t *testing.T) {
	t.Parallel()
	// GORM Updates with no matching row returns no error and 0 rows affected.
	db := setupTestDB(t)
	repo := NewScanRepository(db)

	err := repo.MarkAborted(context.Background(), uuid.New(), "noop")
	assert.NoError(t, err)
}

func TestScanRepository_Create_ClosedDB_ReturnsError(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	repo := NewScanRepository(db)
	err = repo.Create(context.Background(), newScanRecord(uuid.New()))
	require.Error(t, err)
}

func TestScanRepository_Update_ClosedDB_ReturnsError(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	require.NoError(t, repo.Create(context.Background(), newScanRecord(uuid.New())))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	err = repo.Update(context.Background(), newScanRecord(uuid.New()))
	require.Error(t, err)
}

// TestScanRepository_TxRollback_OnForcedError is the 008a regression
// canary for D-3.A.3 applied to ScanRepository. It seeds a scan row
// (auto-committed outside the tx), then opens a Transactor.Transaction
// that calls scanRepo.Update inside it, then forces the function to
// return an error. After the tx rolls back, the row must be observable
// in its ORIGINAL pre-tx state — the Update must not have escaped the
// rollback envelope.
//
// With the pre-fix code (Update using r.db.WithContext bypassing
// dbFromContext) the Update would auto-commit on Postgres before the
// surrounding tx rolled back. On SQLite this test would also fail
// because the row's status would reflect the in-tx Update rather than
// the pre-tx state.
func TestScanRepository_TxRollback_OnForcedError(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	tx := NewGormTransactor(db)
	ctx := context.Background()

	// Seed: one scan in "running" state, persisted outside the tx.
	id := uuid.New()
	original := newScanRecord(id)
	original.Status = "running"
	require.NoError(t, repo.Create(ctx, original))

	// Compose a tx that updates the scan to "completed", then forces
	// the function to return an error so the tx rolls back.
	forced := errors.New("forced tx rollback")
	txErr := tx.Transaction(ctx, func(txCtx context.Context) error {
		modified := original
		modified.Status = "completed"
		modified.SeriesScanned = 99
		if err := repo.Update(txCtx, modified); err != nil {
			return err
		}
		return forced
	})
	require.Error(t, txErr, "transaction must propagate the forced error")
	assert.True(t, errors.Is(txErr, forced), "error must wrap the forced sentinel")

	// Assert the scan row is unchanged — Update was rolled back.
	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "running", got.Status,
		"Update inside a rolled-back tx must NOT persist — dbFromContext must route the write through the tx session")
	assert.Equal(t, 0, got.SeriesScanned,
		"Update inside a rolled-back tx must NOT persist field changes")
}

func TestScanRepository_IncrementSeriesScanned_Atomic(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	id := uuid.New()
	require.NoError(t, repo.Create(ctx, newScanRecord(id)))

	// Two increments must compose to the sum — atomic update, not read-modify-write.
	require.NoError(t, repo.IncrementSeriesScanned(ctx, id, 5))
	require.NoError(t, repo.IncrementSeriesScanned(ctx, id, 3))

	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 8, got.SeriesScanned)
}

func TestScanRepository_IncrementSeriesScanned_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	err := repo.IncrementSeriesScanned(context.Background(), uuid.New(), 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound), "got %v", err)
}
