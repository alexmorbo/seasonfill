// atlas.hcl — Atlas dev-time schema-as-code config.
//
// The schema source-of-truth lives in infrastructure/database/schema/schema.go
// (see PRD §6.6 Database Portability Contract). Runtime migrations are applied
// via golang-migrate from the generated SQL files in
// infrastructure/database/migrations/{postgres,sqlite}/. Atlas itself is a
// dev-time codegen tool — production runtime does NOT require the atlas binary.
//
// Usage:
//   make atlas-install              -- install pinned atlas CLI
//   make migrations-diff NAME=foo   -- generate next migration for both dialects
//   make migrations-lint            -- lint last migration (destructive ops, hash drift)
//   make migrations-apply-dev       -- apply via atlas to a local dev DB
//
// CI does NOT depend on atlas for the main test matrix; the
// migrations-diff-check job (added in story 461 / D-1-8) is the only CI
// surface that requires the atlas binary.

env "postgres" {
  src = "file://infrastructure/database/schema?format=go"
  url = getenv("SEASONFILL_DATABASE_DSN")
  dev = "docker://postgres/17/dev?search_path=public"

  migration {
    dir    = "file://infrastructure/database/migrations/postgres"
    format = golang-migrate
  }

  format {
    migrate {
      diff = "{{ sql . \"  \" }}"
    }
  }
}

env "sqlite" {
  src = "file://infrastructure/database/schema?format=go"
  url = "sqlite://./data/seasonfill.dev.sqlite"
  dev = "sqlite://?mode=memory&_fk=1"

  migration {
    dir    = "file://infrastructure/database/migrations/sqlite"
    format = golang-migrate
  }

  format {
    migrate {
      diff = "{{ sql . \"  \" }}"
    }
  }
}
