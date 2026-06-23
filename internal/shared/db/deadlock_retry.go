// Package database — deadlock-retry helper.
//
// B-37 follow-up. The atomic CASE upsert in people_repository.Upsert
// closed the probe-then-insert race, but `INSERT ... ON CONFLICT DO
// UPDATE` still acquires row-level EXCLUSIVE locks in arrival order.
// Two concurrent producers (series_worker + person_worker, or two
// series_worker txes touching overlapping episode-guest tmdb_ids) can
// still deadlock if their per-tx iteration order disagrees on the same
// set of rows. The B-26 sort-by-tmdb_id discipline suppresses the
// cycle on the hot main path; this helper is the safety net for the
// remaining cases (applyEpisodeCredits walks season×episode×guest and
// cannot be cheaply pre-sorted).
//
// Postgres aborts the ENTIRE transaction on 40P01, so per-statement
// retry is impossible — the retry MUST be at the transaction boundary.
// The fn closure is re-run from scratch on each deadlock retry; callers
// must keep it idempotent (every repo write reachable from the closure
// is an UPSERT in this codebase, so re-running is safe).

package database

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// DefaultDeadlockRetryAttempts is the budget shared by GormTransactor
// and any callers that wrap `db.Transaction` directly. Backoff starts
// at 50ms with full jitter (each sleep is uniform-random over
// [0, backoff)) and doubles up to a 1s cap. The base is below the
// Postgres default deadlock_timeout of 1s; jitter scatters contending
// txes across the next detection window rather than re-bursting in
// lockstep. With BatchUpsert imposing globally-consistent lock order
// on the hot burst paths, the first retry succeeds in practice — the
// generous budget exists to absorb tail contention.
const DefaultDeadlockRetryAttempts = 5

// TransactWithDeadlockRetry runs fn inside a gorm transaction. When
// the inner tx returns a Postgres SQLSTATE 40P01 (deadlock_detected)
// the helper retries the WHOLE transaction up to maxAttempts times
// with exponential backoff + full jitter. Non-deadlock errors return
// immediately — we do not want to mask constraint violations or
// connection drops.
//
// Jitter: each backoff sleep is uniform-random over [0, currentBackoff)
// rather than a fixed sleep. Without jitter, all N goroutines that
// deadlocked together would sleep the same fixed interval and re-burst
// in lockstep, deadlocking again on the same rows. Jitter spreads the
// retry window so one tx wins the lock cycle and unblocks the others.
//
// SQLite never produces 40P01, so on the SQLite backend this helper
// is functionally equivalent to a single db.Transaction call.
func TransactWithDeadlockRetry(db *gorm.DB, maxAttempts int, fn func(tx *gorm.DB) error) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var lastErr error
	// Base 50ms with full jitter scatters contending txes across the
	// next deadlock-detector window. Cap at 1s so the worst-case sleep
	// stays bounded if the budget grows.
	backoff := 50 * time.Millisecond
	const backoffCap = time.Second
	for i := 0; i < maxAttempts; i++ {
		err := db.Transaction(fn)
		if err == nil {
			return nil
		}
		if !IsPgDeadlock(err) {
			return err
		}
		lastErr = err
		if i < maxAttempts-1 {
			//nolint:gosec // weak RNG is fine: jitter is a contention-break heuristic, not a security primitive.
			jitter := time.Duration(rand.Int63n(int64(backoff)))
			time.Sleep(jitter)
			backoff *= 2
			if backoff > backoffCap {
				backoff = backoffCap
			}
		}
	}
	return fmt.Errorf("transaction deadlock victim after %d attempts: %w", maxAttempts, lastErr)
}

// IsPgDeadlock reports whether err originated from Postgres SQLSTATE
// 40P01 (deadlock_detected). Tries the typed pgconn.PgError path first
// (preferred — survives error wrapping via errors.As); falls back to
// substring sniff for cases where the driver wraps the message before
// it reaches us. Exported for callers that need to make their own
// retry decisions (e.g. tests that exercise the retry budget).
func IsPgDeadlock(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "40P01" {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE 40P01") || strings.Contains(msg, "deadlock detected")
}
