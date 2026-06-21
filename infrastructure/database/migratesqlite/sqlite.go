// Package migratesqlite vendors the golang-migrate sqlite database driver
// without the upstream's blank `_ "modernc.org/sqlite"` import. We already
// register the "sqlite" driver through github.com/glebarez/go-sqlite (pulled
// by github.com/glebarez/sqlite, the GORM dialector); pulling modernc.org/sqlite
// again would call database/sql.Register("sqlite", ...) a second time and
// panic at process init. Source: github.com/golang-migrate/migrate/v4 v4.19.1
// database/sqlite/sqlite.go.
package migratesqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/golang-migrate/migrate/v4/database"
)

var DefaultMigrationsTable = "schema_migrations"

var ErrNilConfig = errors.New("no config")

type Config struct {
	MigrationsTable string
	NoTxWrap        bool
}

type Sqlite struct {
	db       *sql.DB
	isLocked atomic.Bool

	config *Config
}

func WithInstance(instance *sql.DB, config *Config) (database.Driver, error) {
	if config == nil {
		return nil, ErrNilConfig
	}
	ctx := context.Background()
	if err := instance.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	if len(config.MigrationsTable) == 0 {
		config.MigrationsTable = DefaultMigrationsTable
	}
	mx := &Sqlite{db: instance, config: config}
	if err := mx.ensureVersionTable(ctx); err != nil {
		return nil, err
	}
	return mx, nil
}

func (m *Sqlite) ensureVersionTable(ctx context.Context) (err error) {
	if err = m.Lock(); err != nil {
		return err
	}
	defer func() {
		if e := m.Unlock(); e != nil {
			err = errors.Join(err, e)
		}
	}()
	query := fmt.Sprintf(`
	CREATE TABLE IF NOT EXISTS %s (version uint64,dirty bool);
  CREATE UNIQUE INDEX IF NOT EXISTS version_unique ON %s (version);
  `, m.config.MigrationsTable, m.config.MigrationsTable)
	if _, err := m.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("ensure version table: %w", err)
	}
	return nil
}

func (m *Sqlite) Open(string) (database.Driver, error) {
	return nil, errors.New("migratesqlite: Open not supported; use WithInstance")
}

func (m *Sqlite) Close() error { return m.db.Close() }

func (m *Sqlite) Drop() (err error) {
	ctx := context.Background()
	query := `SELECT name FROM sqlite_master WHERE type = 'table';`
	tables, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return &database.Error{OrigErr: err, Query: []byte(query)}
	}
	defer func() {
		if errClose := tables.Close(); errClose != nil {
			err = errors.Join(err, errClose)
		}
	}()
	tableNames := make([]string, 0)
	for tables.Next() {
		var tableName string
		if scanErr := tables.Scan(&tableName); scanErr != nil {
			return fmt.Errorf("scan table name: %w", scanErr)
		}
		if len(tableName) > 0 {
			tableNames = append(tableNames, tableName)
		}
	}
	if err := tables.Err(); err != nil {
		return &database.Error{OrigErr: err, Query: []byte(query)}
	}
	for _, t := range tableNames {
		q := "DROP TABLE " + t
		if err := m.executeQuery(ctx, q); err != nil {
			return &database.Error{OrigErr: err, Query: []byte(q)}
		}
	}
	if len(tableNames) > 0 {
		if _, err := m.db.ExecContext(ctx, "VACUUM"); err != nil {
			return &database.Error{OrigErr: err, Query: []byte("VACUUM")}
		}
	}
	return nil
}

func (m *Sqlite) Lock() error {
	if !m.isLocked.CompareAndSwap(false, true) {
		return database.ErrLocked
	}
	return nil
}

func (m *Sqlite) Unlock() error {
	if !m.isLocked.CompareAndSwap(true, false) {
		return database.ErrNotLocked
	}
	return nil
}

func (m *Sqlite) Run(migration io.Reader) error {
	migr, err := io.ReadAll(migration)
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	ctx := context.Background()
	query := string(migr)
	if m.config.NoTxWrap {
		return m.executeQueryNoTx(ctx, query)
	}
	return m.executeQuery(ctx, query)
}

func (m *Sqlite) executeQuery(ctx context.Context, query string) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return &database.Error{OrigErr: err, Err: "transaction start failed"}
	}
	if _, err := tx.ExecContext(ctx, query); err != nil {
		if errRollback := tx.Rollback(); errRollback != nil {
			err = errors.Join(err, errRollback)
		}
		return &database.Error{OrigErr: err, Query: []byte(query)}
	}
	if err := tx.Commit(); err != nil {
		return &database.Error{OrigErr: err, Err: "transaction commit failed"}
	}
	return nil
}

func (m *Sqlite) executeQueryNoTx(ctx context.Context, query string) error {
	if _, err := m.db.ExecContext(ctx, query); err != nil {
		return &database.Error{OrigErr: err, Query: []byte(query)}
	}
	return nil
}

func (m *Sqlite) SetVersion(version int, dirty bool) error {
	ctx := context.Background()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return &database.Error{OrigErr: err, Err: "transaction start failed"}
	}
	query := "DELETE FROM " + m.config.MigrationsTable
	if _, err := tx.ExecContext(ctx, query); err != nil {
		return &database.Error{OrigErr: err, Query: []byte(query)}
	}
	if version >= 0 || (version == database.NilVersion && dirty) {
		q := fmt.Sprintf(`INSERT INTO %s (version, dirty) VALUES (?, ?)`, m.config.MigrationsTable)
		if _, err := tx.ExecContext(ctx, q, version, dirty); err != nil {
			if errRollback := tx.Rollback(); errRollback != nil {
				err = errors.Join(err, errRollback)
			}
			return &database.Error{OrigErr: err, Query: []byte(q)}
		}
	}
	if err := tx.Commit(); err != nil {
		return &database.Error{OrigErr: err, Err: "transaction commit failed"}
	}
	return nil
}

// Version follows the upstream golang-migrate sqlite driver: a missing or
// empty schema_migrations row maps to (NilVersion, false, nil) so the
// migrator treats the schema as "no version recorded" rather than a hard
// error. Bubbling the scan error here would stop Up() before any baseline
// can run.
func (m *Sqlite) Version() (version int, dirty bool, err error) {
	query := "SELECT version, dirty FROM " + m.config.MigrationsTable + " LIMIT 1"
	scanErr := m.db.QueryRowContext(context.Background(), query).Scan(&version, &dirty)
	if scanErr != nil {
		return database.NilVersion, false, nil //nolint:nilerr // upstream contract
	}
	return version, dirty, nil
}
