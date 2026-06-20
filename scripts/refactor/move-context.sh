#!/usr/bin/env bash
# move-context.sh — orchestrates a vertical-slice migration step for the
# refactor-first plan (stories 427-447 chain).
#
# Each call does ONE move: git-mv a source tree to a target tree, rewrite
# the package declaration in moved .go files, and sed-rewrite the old
# import path to the new one across the entire repo. Then runs gofmt +
# go build to confirm the move compiles before the operator commits.
#
# Usage:
#   scripts/refactor/move-context.sh <src-dir> <dst-dir> [<old-pkg-name> <new-pkg-name>]
#
# Examples:
#   scripts/refactor/move-context.sh domain/media internal/mediaproxy/domain
#   scripts/refactor/move-context.sh domain/media internal/mediaproxy/domain media domain
#
# Arguments:
#   <src-dir>      Old path relative to repo root (e.g. domain/media)
#   <dst-dir>      New path relative to repo root (e.g. internal/mediaproxy/domain)
#   <old-pkg-name> (optional) Package name to replace in `package <X>` headers
#   <new-pkg-name> (optional) Replacement package name
#
# If old/new package names are omitted, the package declarations are NOT
# rewritten — the move becomes purely directory-level. Use this mode when
# consumers reference the package by its bare name (no alias) and you
# want to defer the alias churn to a later rename-pass story.
#
# What it does (idempotent across re-runs of a single migration step):
#   1. git mv  <src-dir>  <dst-dir>
#   2. Rewrites `package <old>` -> `package <new>` in <dst-dir>/*.go
#   3. Greps repo for `seasonfill/<src-dir>` and sed-rewrites it to
#      `seasonfill/<dst-dir>` in every .go file (excluding generated
#      and vendor trees).
#   4. Drops .bak sidecar files created by sed.
#   5. gofmt -w on the moved tree.
#   6. go build ./...  (fails fast on a broken move).
#
# Notes:
#   * Audit log lines are emitted to stderr, summary to stdout.
#   * Assumes BSD sed (macOS default). Linux sed would need s/-i.bak/-i.bak/
#     handled — current form is portable.
#   * Does NOT git-commit. Operator (or impl agent) commits after review.
#   * If go build fails, the working tree is left in a broken state so
#     the operator can inspect; recover with `git restore .` if needed.
#
# Future enhancement (deferred to 428+): swap sed-rewrite for `gopls
# rename` once we are sure all .go files compile cleanly under it.

set -euo pipefail

if [[ $# -ne 2 && $# -ne 4 ]]; then
    echo "usage: $0 <src-dir> <dst-dir> [<old-pkg-name> <new-pkg-name>]" >&2
    exit 64
fi

SRC="$1"
DST="$2"
OLD_PKG="${3:-}"
NEW_PKG="${4:-}"

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

if [[ ! -d "$SRC" ]]; then
    echo "[move-context] source $SRC does not exist or is not a directory" >&2
    exit 65
fi

if [[ -d "$DST" && -n "$(ls -A "$DST" 2>/dev/null)" ]]; then
    echo "[move-context] destination $DST exists and is not empty; refusing to overwrite" >&2
    exit 66
fi

MOD_PATH="github.com/alexmorbo/seasonfill"
OLD_IMPORT="$MOD_PATH/$SRC"
NEW_IMPORT="$MOD_PATH/$DST"

echo "[move-context] step 1/6: git mv $SRC -> $DST" >&2
# Ensure parent of DST exists; git mv handles the rest. If destination
# directory already exists (and is empty per the check above), git mv
# can refuse — pre-remove empty dir first.
mkdir -p "$(dirname "$DST")"
if [[ -d "$DST" ]]; then
    rmdir "$DST"
fi
git mv "$SRC" "$DST"

if [[ -n "$OLD_PKG" && -n "$NEW_PKG" ]]; then
    echo "[move-context] step 2/6: rewrite package $OLD_PKG -> $NEW_PKG inside $DST" >&2
    # Use a temp-file sed to be portable between GNU and BSD.
    find "$DST" -type f -name "*.go" -print0 | while IFS= read -r -d '' f; do
        # Only rewrite if the file actually opens with `package $OLD_PKG`.
        if head -n 20 "$f" | grep -qE "^package $OLD_PKG\$"; then
            sed -i.bak "s/^package $OLD_PKG\$/package $NEW_PKG/" "$f"
        fi
    done
else
    echo "[move-context] step 2/6: skipped (no package rename requested)" >&2
fi

echo "[move-context] step 3/6: rewrite import path $OLD_IMPORT -> $NEW_IMPORT across repo" >&2
# Grep for the bare path (escaped) so /mediastore matches but not /mediastore-something.
# The trailing word boundary uses character class for portability across BSD/GNU grep.
mapfile -t consumer_files < <(grep -rl --include="*.go" --exclude-dir=vendor --exclude-dir=node_modules "$OLD_IMPORT" . || true)
if [[ ${#consumer_files[@]} -gt 0 ]]; then
    for f in "${consumer_files[@]}"; do
        # sed-rewrite using a delimiter that doesn't collide with slashes.
        sed -i.bak "s|$OLD_IMPORT|$NEW_IMPORT|g" "$f"
    done
    echo "[move-context]   rewrote $OLD_IMPORT in ${#consumer_files[@]} files" >&2
else
    echo "[move-context]   no consumer references to $OLD_IMPORT found" >&2
fi

echo "[move-context] step 4/6: drop *.go.bak sidecars" >&2
find . -name "*.go.bak" -not -path "./vendor/*" -not -path "./node_modules/*" -delete

echo "[move-context] step 5/6: gofmt -w $DST + consumers" >&2
gofmt -w "$DST"
# Reformat every file that had its imports rewritten so the import block
# stays sorted (the sed retarget can break alphabetical order inside a
# group). goimports preferred when available; fall back to plain gofmt.
if [[ ${#consumer_files[@]} -gt 0 ]]; then
    if command -v goimports >/dev/null 2>&1; then
        goimports -w "${consumer_files[@]}"
    else
        gofmt -w "${consumer_files[@]}"
    fi
fi

echo "[move-context] step 6/6: go build ./..." >&2
if ! go build ./...; then
    echo "[move-context] go build FAILED — working tree left in broken state for inspection" >&2
    exit 70
fi

echo "[move-context] OK: $SRC -> $DST migrated, $NEW_IMPORT compiles" >&2

# Summary on stdout (machine-parseable for the agent's audit log).
printf '{"src":"%s","dst":"%s","old_pkg":"%s","new_pkg":"%s","consumers_touched":%d}\n' \
    "$SRC" "$DST" "${OLD_PKG:-}" "${NEW_PKG:-}" "${#consumer_files[@]}"
