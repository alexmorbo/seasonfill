package testhelpers

import (
	"fmt"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// SQLite schema-cache fast path (story A-4-3b-7).
//
// Background: newSQLiteDB used to call database.Migrate(db) on every
// AllBackends invocation, running all 44 SQLite migrations through
// golang-migrate end-to-end. With ~300 tests in the repositories
// package × 44 migrations × the parallel dispatch fan-out under
// `-race`, the migration cost dominated wall-clock and (under -race)
// occasionally tripped the package timeout.
//
// New approach (lifted from the pre-A-4 scan_repository_test.go
// schema-cache, story 314): at the first newSQLiteDB call, open one
// template :memory: DB, run all migrations once, snapshot the
// resulting schema as a flat slice of CREATE statements
// (sqlite_master), and also snapshot any seed rows migrations
// inserted (e.g. the singleton app_settings row from 000036). Cache
// both under sync.Once. Every subsequent newSQLiteDB call opens a
// fresh :memory: GORM instance, replays the cached DDL in a single
// batch, then replays the seed rows — no migrate library overhead,
// no schema_migrations bookkeeping, no driver re-instantiation.
//
// Tests still get fully isolated databases (each :memory: handle is
// its own connection), so t.Parallel() stays safe with no further
// isolation work.
//
// Production code (cmd/server/main.go bootstrap calling
// database.Migrate) is untouched.

// seedRowGroup is one table's worth of seed data captured from the
// template DB. We carry column ordering with the values so the replay
// INSERT lines columns up by position, not by relying on map iteration
// order.
type seedRowGroup struct {
	table   string
	columns []string
	rows    [][]any
}

var (
	sqliteSchemaCacheOnce  sync.Once
	sqliteSchemaCacheDDL   []string
	sqliteSchemaCacheSeeds []seedRowGroup
	sqliteSchemaCacheErr   error
)

// buildSQLiteSchemaCache opens a throwaway template DB, runs all
// migrations once, then snapshots the resulting schema as a slice of
// CREATE statements ordered for replay (tables first, then indexes;
// sqlite itself enforces the table-must-exist-before-index rule).
//
// We deliberately exclude sqlite_sequence (auto-managed) and
// schema_migrations (rebuilt by migrate library itself).
func buildSQLiteSchemaCache() {
	sqliteSchemaCacheOnce.Do(func() {
		tmpl, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		if err != nil {
			sqliteSchemaCacheErr = fmt.Errorf("open template sqlite: %w", err)
			return
		}
		sqlDB, err := tmpl.DB()
		if err != nil {
			sqliteSchemaCacheErr = fmt.Errorf("template sql.DB: %w", err)
			return
		}
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		defer func() { _ = sqlDB.Close() }()

		if err := database.Migrate(tmpl); err != nil {
			sqliteSchemaCacheErr = fmt.Errorf("migrate template: %w", err)
			return
		}

		var rows []struct {
			Type    string
			Name    string
			TblName string
			SQL     string
		}
		// Exclude:
		//   - sqlite_% — auto-managed (sqlite_sequence, sqlite_autoindex_*)
		//   - schema_migrations TABLE — golang-migrate bookkeeping, rebuilt
		//     by the migrate library when (and if) it's called against this
		//     handle; tests never hit it
		//   - any index whose tbl_name is schema_migrations — would otherwise
		//     fail to replay because the table itself is gone
		if err := tmpl.Raw(`SELECT type, name, tbl_name, sql FROM sqlite_master
			WHERE sql IS NOT NULL
			  AND name NOT LIKE 'sqlite_%'
			  AND tbl_name <> 'schema_migrations'
			ORDER BY CASE type WHEN 'table' THEN 0 WHEN 'index' THEN 1 ELSE 2 END, name`).
			Scan(&rows).Error; err != nil {
			sqliteSchemaCacheErr = fmt.Errorf("scan sqlite_master: %w", err)
			return
		}
		ddl := make([]string, 0, len(rows))
		for _, r := range rows {
			ddl = append(ddl, r.SQL)
		}
		sqliteSchemaCacheDDL = ddl

		// Snapshot seed rows for tables migrations inserted into. We
		// query every non-schema_migrations table; tables that came
		// out empty contribute nothing.
		var tables []string
		if err := tmpl.Raw(`SELECT name FROM sqlite_master
			WHERE type = 'table'
			  AND name NOT LIKE 'sqlite_%'
			  AND name <> 'schema_migrations'
			ORDER BY name`).Scan(&tables).Error; err != nil {
			sqliteSchemaCacheErr = fmt.Errorf("list tables: %w", err)
			return
		}
		var seeds []seedRowGroup
		for _, table := range tables {
			var cols []struct {
				Name string
			}
			if err := tmpl.Raw(fmt.Sprintf("PRAGMA table_info(%q)", table)).Scan(&cols).Error; err != nil {
				sqliteSchemaCacheErr = fmt.Errorf("table_info(%s): %w", table, err)
				return
			}
			if len(cols) == 0 {
				continue
			}
			colNames := make([]string, len(cols))
			for i, c := range cols {
				colNames[i] = c.Name
			}
			var rawRows []map[string]any
			if err := tmpl.Table(table).Find(&rawRows).Error; err != nil {
				sqliteSchemaCacheErr = fmt.Errorf("select %s: %w", table, err)
				return
			}
			if len(rawRows) == 0 {
				continue
			}
			grp := seedRowGroup{table: table, columns: colNames, rows: make([][]any, 0, len(rawRows))}
			for _, r := range rawRows {
				vals := make([]any, len(colNames))
				for i, c := range colNames {
					vals[i] = r[c]
				}
				grp.rows = append(grp.rows, vals)
			}
			seeds = append(seeds, grp)
		}
		sqliteSchemaCacheSeeds = seeds
	})
}

// newSQLiteDBFromCache returns a fresh `:memory:` GORM handle whose
// schema is replayed from the per-process template cache. Identical to
// the legacy setupTestDB behavior in repositories/scan_repository_test.go
// (story 314), now lifted into testhelpers so AllBackends consumers
// share the speedup.
func newSQLiteDBFromCache(t testing.TB) *gorm.DB {
	t.Helper()
	buildSQLiteSchemaCache()
	if sqliteSchemaCacheErr != nil {
		t.Fatalf("sqlite schema cache: %v", sqliteSchemaCacheErr)
	}
	if len(sqliteSchemaCacheDDL) == 0 {
		t.Fatalf("sqlite schema cache is empty")
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	for _, stmt := range sqliteSchemaCacheDDL {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("replay ddl %q: %v", firstLine(stmt), err)
		}
	}

	for _, grp := range sqliteSchemaCacheSeeds {
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
				t.Fatalf("replay seed row into %s: %v", grp.table, err)
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
