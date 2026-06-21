// Command loader marshals the seasonfill declarative schema (defined in
// the parent infrastructure/database/schema package) into SQL DDL for
// the requested dialect. It is invoked by atlas.hcl's
// `data "external_schema"` block so that Atlas can consume our pure-Go
// schema definition without depending on the (non-existent) generic
// ariga.io/atlas-provider-go binary.
//
// Atlas's external_schema contract expects the program to print a series
// of SQL DDL statements terminated by ";" — Atlas then replays them
// against its dev database to materialize the desired schema state and
// diffs that against the migration directory. See
//
//	https://atlasgo.io/atlas-schema/external
//
// for details.
//
// Usage:
//
//	go run ./infrastructure/database/schema/cmd/loader --dialect=postgres
//	go run ./infrastructure/database/schema/cmd/loader --dialect=sqlite
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/postgres"
	atlasschema "ariga.io/atlas/sql/schema"
	"ariga.io/atlas/sql/sqlite"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

func main() {
	var dialect string
	flag.StringVar(&dialect, "dialect", "", "target dialect (postgres|sqlite)")
	flag.Parse()

	if dialect == "" {
		fmt.Fprintln(os.Stderr, "loader: --dialect is required (postgres|sqlite)")
		os.Exit(2)
	}

	d := schema.Dialect(dialect)
	target := schema.Schema(d)

	// SEASONFILL_DROP_INDEX is a dev/test-only hook used by the D-1-8
	// atlas-diff regression test (tests/integration/d1_acceptance_diff_test.go).
	// When set, the loader removes every index whose name equals the env
	// value from every table in the target schema BEFORE emitting DDL.
	// Atlas then sees the index as "missing" and includes a DROP INDEX in
	// the generated diff, proving the diff path detects drift. Production
	// loader invocations leave this env unset.
	if dropIdx := os.Getenv("SEASONFILL_DROP_INDEX"); dropIdx != "" {
		for _, tbl := range target.Tables {
			kept := tbl.Indexes[:0]
			for _, ix := range tbl.Indexes {
				if ix.Name != dropIdx {
					kept = append(kept, ix)
				}
			}
			tbl.Indexes = kept
		}
	}

	plan, err := emitDDL(d, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loader: emit DDL: %v\n", err)
		os.Exit(1)
	}

	for _, c := range plan.Changes {
		// Atlas's external_schema expects ";" terminated statements.
		if _, err := fmt.Fprintln(os.Stdout, strings.TrimRight(c.Cmd, "\n")+";"); err != nil {
			fmt.Fprintf(os.Stderr, "loader: write stdout: %v\n", err)
			os.Exit(1)
		}
	}
}

// emitDDL converts every table in target into a sequence of
// CREATE TABLE / CREATE INDEX statements via Atlas's dialect-specific
// PlanApplier.DefaultPlan, which works without a live database
// connection (it uses sqlx.NoRows as its ExecQuerier).
func emitDDL(d schema.Dialect, target *atlasschema.Schema) (*migrate.Plan, error) {
	changes := make([]atlasschema.Change, 0, len(target.Tables))
	for _, t := range target.Tables {
		changes = append(changes, &atlasschema.AddTable{T: t})
	}

	ctx := context.Background()

	switch d {
	case schema.DialectPostgres:
		return postgres.DefaultPlan.PlanChanges(ctx, "load", changes)
	case schema.DialectSQLite:
		return sqlite.DefaultPlan.PlanChanges(ctx, "load", changes)
	}
	return nil, fmt.Errorf("unknown dialect %q", d)
}
