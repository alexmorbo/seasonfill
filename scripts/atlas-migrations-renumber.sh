#!/usr/bin/env bash
# atlas-migrations-renumber.sh — rename Atlas-emitted migration files
# from the default Unix-timestamp prefix to a zero-padded sequential
# index that matches the legacy migrations under
# internal/shared/db/migrations/{postgres,sqlite}.
#
# Atlas emits "20260620234111_core_series.up.sql" / ".down.sql" pairs
# (timestamp prefix from migrate.GolangMigrateFormatter, hard-coded in
# ariga.io/atlas v0.31.0 sql/sqltool/tool.go). golang-migrate accepts
# both numeric and timestamp prefixes, but mixing styles within one
# project hurts readability and makes manual reasoning about migration
# order brittle. This script normalizes everything to 6-digit padded
# sequence.
#
# Usage:
#   ./scripts/atlas-migrations-renumber.sh infrastructure/database/migrations/postgres
#
# Idempotent: files already named with a 6-digit prefix are left alone.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "Usage: $0 <migrations-dir>" >&2
  exit 2
fi

dir=$1
if [[ ! -d "$dir" ]]; then
  echo "$0: not a directory: $dir" >&2
  exit 1
fi

cd "$dir"

# Find the highest existing 6-digit-prefixed migration. Initialise to 0
# so the first newly added migration becomes 000001_*.sql.
highest=0
for f in [0-9][0-9][0-9][0-9][0-9][0-9]_*.up.sql; do
  [[ -e "$f" ]] || continue
  n=$(printf '%s' "$f" | sed -E 's/^([0-9]{6})_.*/\1/')
  if (( 10#$n > highest )); then
    highest=$((10#$n))
  fi
done

# Each timestamp-prefixed up.sql gets the next sequence; its .down.sql
# pair inherits the same index. Iterate sorted by timestamp so older
# migrations get smaller indices.
for up in $(ls 2>/dev/null | grep -E '^[0-9]{14}_.+\.up\.sql$' | sort); do
  base=${up%.up.sql}
  ts=${base%%_*}
  name=${base#*_}
  highest=$((highest + 1))
  newseq=$(printf '%06d' "$highest")
  new_up="${newseq}_${name}.up.sql"
  new_down="${newseq}_${name}.down.sql"

  echo "renaming ${up} -> ${new_up}"
  mv "$up" "$new_up"

  down="${ts}_${name}.down.sql"
  if [[ -e "$down" ]]; then
    echo "renaming ${down} -> ${new_down}"
    mv "$down" "$new_down"
  fi
done
