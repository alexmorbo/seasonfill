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

D-1-1 ships this directory empty; subsequent sub-stories (D-1-2..D-1-7)
land the 14 target migrations.
