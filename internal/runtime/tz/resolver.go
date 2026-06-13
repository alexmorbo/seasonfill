// Package tz owns the operator-selected runtime timezone. Source
// precedence on Resolve: DB row override > TZ env var > UTC.
//
// The Resolver is constructed once at boot, queried by every
// component that needs a *time.Location (cron scheduler factory,
// server-side date formatters). Set() is called by the
// PATCH /settings/timezone handler; it atomically updates the
// in-memory cache + persists to the DB. Already-scheduled cron
// jobs do NOT pick up the new location until pod restart — see
// the story's known_limitations for context.
package tz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Source describes where the currently-loaded location came from.
type Source string

const (
	SourceDB      Source = "db"      // operator PATCH'd a value
	SourceEnv     Source = "env"     // TZ env var was honored
	SourceDefault Source = "default" // env unset / invalid; fell back to UTC
)

// Store is the persistence surface the resolver needs. Concretely
// implemented by AppSettingsRepository.
type Store interface {
	GetTimezone(ctx context.Context) (string, error)
	SetTimezone(ctx context.Context, name string) error
}

// ErrInvalidTimezone is returned by Set when the supplied IANA name
// fails time.LoadLocation. Callers (HTTP handler) map to 400.
var ErrInvalidTimezone = errors.New("invalid IANA timezone")

// Resolver holds the current *time.Location + a RWMutex for safe
// runtime swap. Multi-reader / single-writer pattern: Get() is on
// the hot path (called by cron factory + every date format site),
// Set() is rare.
type Resolver struct {
	mu     sync.RWMutex
	loc    *time.Location
	source Source
	name   string

	store  Store
	logger *slog.Logger
}

// New constructs a Resolver and primes it from the store + env.
// Precedence: DB row override > TZ env > UTC. A non-empty store
// value that fails LoadLocation is treated as if NULL (log + fall
// back to env); same for an invalid TZ env. Never returns an
// error — the Resolver always boots with a valid location.
func New(ctx context.Context, store Store, logger *slog.Logger) *Resolver {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Resolver{store: store, logger: logger}

	// 1. DB override.
	if store != nil {
		dbName, err := store.GetTimezone(ctx)
		if err != nil {
			logger.WarnContext(ctx, "tz.resolver.db_read_failed",
				slog.String("error", err.Error()))
		} else if dbName != "" {
			if loc, err := time.LoadLocation(dbName); err == nil {
				r.loc = loc
				r.source = SourceDB
				r.name = dbName
				return r
			}
			logger.WarnContext(ctx, "tz.resolver.db_value_invalid",
				slog.String("name", dbName))
		}
	}

	// 2. TZ env.
	if envName := os.Getenv("TZ"); envName != "" {
		if loc, err := time.LoadLocation(envName); err == nil {
			r.loc = loc
			r.source = SourceEnv
			r.name = envName
			return r
		}
		logger.WarnContext(ctx, "tz.resolver.env_value_invalid",
			slog.String("name", envName))
	}

	// 3. UTC fallback.
	r.loc = time.UTC
	r.source = SourceDefault
	r.name = "UTC"
	return r
}

// Get returns the current *time.Location. Never nil — falls back
// to time.UTC if somehow swapped to nil concurrently.
func (r *Resolver) Get() *time.Location {
	r.mu.RLock()
	loc := r.loc
	r.mu.RUnlock()
	if loc == nil {
		return time.UTC
	}
	return loc
}

// Source returns where the current location was sourced from.
func (r *Resolver) Source() Source {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.source
}

// Name returns the IANA name of the current location (or "UTC"
// on the default-fallback path).
func (r *Resolver) Name() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.name
}

// Set validates name as IANA, persists it (or clears the
// override on empty name), and updates the in-memory cache. The
// store write happens BEFORE the in-memory swap so a DB error
// leaves both views consistent.
//
// Passing "" clears the DB override and re-runs the env/default
// precedence in memory.
func (r *Resolver) Set(ctx context.Context, name string) error {
	// Clear path.
	if name == "" {
		if r.store != nil {
			if err := r.store.SetTimezone(ctx, ""); err != nil {
				return fmt.Errorf("clear timezone: %w", err)
			}
		}
		r.swapToEnvOrDefault(ctx)
		return nil
	}

	// Validate IANA.
	loc, err := time.LoadLocation(name)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidTimezone, name)
	}
	if r.store != nil {
		if err := r.store.SetTimezone(ctx, name); err != nil {
			return fmt.Errorf("persist timezone: %w", err)
		}
	}
	r.mu.Lock()
	r.loc = loc
	r.source = SourceDB
	r.name = name
	r.mu.Unlock()
	r.logger.InfoContext(ctx, "tz.resolver.updated",
		slog.String("name", name), slog.String("source", string(SourceDB)))
	return nil
}

// swapToEnvOrDefault re-derives the location from env / UTC
// fallback. Holds the write lock for the swap so a concurrent
// Get sees a consistent (loc, source, name) triple.
func (r *Resolver) swapToEnvOrDefault(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if envName := os.Getenv("TZ"); envName != "" {
		if loc, err := time.LoadLocation(envName); err == nil {
			r.loc = loc
			r.source = SourceEnv
			r.name = envName
			r.logger.InfoContext(ctx, "tz.resolver.cleared_to_env",
				slog.String("name", envName))
			return
		}
	}
	r.loc = time.UTC
	r.source = SourceDefault
	r.name = "UTC"
	r.logger.InfoContext(ctx, "tz.resolver.cleared_to_default")
}
