# infrastructure/database/migrations/postgres

Generated SQL migrations for the Postgres dialect. **Do not edit by hand.**

Files in this directory are produced by:

```
make migrations-diff NAME=<short_name>
```

which runs `atlas migrate diff` against the target schema declared in
`infrastructure/database/schema/schema.go`. Atlas emits a
`NNNNNN_<short_name>.up.sql` / `NNNNNN_<short_name>.down.sql` pair on each
invocation.

Runtime migration is performed by `golang-migrate` (not Atlas itself) —
see `internal/shared/db/migrations.go` for the runtime path. Atlas is a
dev-time codegen tool only; production Docker images do not require the
atlas binary.

To add a new migration:

1. Edit `infrastructure/database/schema/schema.go` (add column, table, index).
2. Run `make migrations-diff NAME=add_foo_column`.
3. Review the generated SQL in this directory.
4. Run `make migrations-lint` to catch destructive ops, missing down,
   integrity hash drift.
5. Commit both the schema.go change AND the generated SQL together.

D-1 ships 13 generated migrations (000001..000013). The PRD §D-1
originally proposed a 14th migration for cross-table indexes; Atlas
codegen inlines indexes per-table, so the 14th file would be empty (and
would fail `atlas migrate lint`). The CI job `migrations-diff-check`
proves the schema is fully expressed in 13 migrations on both dialects
— see story 461 (D-1-8) for the acceptance gate and the
`tests/integration/d1_acceptance_*` regression surface.
