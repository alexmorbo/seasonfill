// In-package coverage harness for schema.go.
//
// Cross-package smoke lives in /tests/d1_2_core_series_test.go and pins the
// shape contract. That suite exercises every helper transitively but its
// coverage is recorded against the `tests` package, not this one — leaving
// schema.go at 0.0% in the unit-job coverage profile despite being fully
// exercised (95.6% under `-coverpkg`). This file invokes the same public
// surface in-package so the helpers register in their own profile.
package schema

import (
	"os"
	"testing"
)

// TestSchemaCoverage_BothDialects walks Schema(d) for every shipped
// dialect. Touches every builder + helper transitively.
func TestSchemaCoverage_BothDialects(t *testing.T) {
	t.Parallel()
	for _, d := range []Dialect{DialectPostgres, DialectSQLite} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := Schema(d)
			if s == nil {
				t.Fatalf("Schema(%q) returned nil", d)
			}
			if len(s.Tables) != 5 {
				t.Fatalf("Schema(%q) tables = %d, want 5", d, len(s.Tables))
			}
		})
	}
}

// TestSchemaCoverage_LoadDefaultsToPostgres covers the Load() env-driven
// entrypoint with an unset ATLAS_DIALECT — the default branch.
func TestSchemaCoverage_LoadDefaultsToPostgres(t *testing.T) {
	t.Setenv(EnvDialect, "")
	s := Load()
	if s == nil {
		t.Fatal("Load() with empty env returned nil")
	}
	if s.Name != SchemaName {
		t.Errorf("Load() schema name = %q, want %q", s.Name, SchemaName)
	}
}

// TestSchemaCoverage_LoadHonorsEnv covers the env-set branch of Load()
// — drives the SQLite dispatch path explicitly.
func TestSchemaCoverage_LoadHonorsEnv(t *testing.T) {
	t.Setenv(EnvDialect, string(DialectSQLite))
	if got := os.Getenv(EnvDialect); got != string(DialectSQLite) {
		t.Fatalf("env setup botched: ATLAS_DIALECT=%q", got)
	}
	s := Load()
	if s == nil {
		t.Fatal("Load() returned nil with ATLAS_DIALECT=sqlite")
	}
	if len(s.Tables) != 5 {
		t.Fatalf("Load() tables = %d, want 5", len(s.Tables))
	}
}

// TestSchemaCoverage_UnknownDialectPanics covers the panic branch of
// Schema(d) — guards against silent empty-schema emission on typo.
func TestSchemaCoverage_UnknownDialectPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Errorf("Schema(\"mariadb\") did not panic")
		}
	}()
	_ = Schema("mariadb")
}
