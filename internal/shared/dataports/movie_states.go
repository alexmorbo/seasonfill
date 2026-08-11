package dataports

import (
	"context"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

//go:generate moq -out movie_states_mock.go . MovieStatesRepository

// MovieStatesRepository persists the per-instance Radarr library-membership
// projection (Ф6-R-4b, movie_states table). Composite PK
// (instance_name, radarr_movie_id). Written by BOTH the radarr-sync loop
// (rich Upsert) and the radarr-webhook handler (thin UpsertStub), exactly as
// series_cache is written by sonarr_sync + the webhook. The COALESCE
// enrichment guard lives on the `movies` canon table (movieUpsertAssignments);
// movie_states carries only library-facing fields, so the rich writer
// straight-assigns and the thin writer omit-preserves the stat columns.
type MovieStatesRepository interface {
	// Get returns the row for (instance_name, radarr_movie_id) regardless of
	// soft-delete state. ports.ErrNotFound on miss.
	Get(ctx context.Context, instanceName domain.InstanceName, radarrMovieID int) (movie.StateEntry, error)

	// Upsert is the RICH writer (radarr-sync): full conflict-update set incl.
	// availability + size_on_disk_bytes. Resurrects a soft-deleted row
	// (deleted_at -> NULL). Requires MovieID != 0.
	Upsert(ctx context.Context, entry movie.StateEntry) error

	// UpsertStub is the THIN writer (radarr-webhook): its conflict-update set
	// is a STRICT SUBSET of Upsert's — it OMITS availability + size_on_disk_bytes
	// so a stat-less webhook write can never zero a real cached stat written by
	// the sync. On INSERT the zero stats land (self-heal on the next sync).
	UpsertStub(ctx context.Context, entry movie.StateEntry) error

	// SoftDelete stamps deleted_at on the active row (MovieDelete webhook).
	// Idempotent: a missing/already-deleted row returns ports.ErrNotFound so
	// the caller can swallow it (mirror series_cache.SoftDelete).
	SoftDelete(ctx context.Context, instanceName domain.InstanceName, radarrMovieID int) error

	// ListActiveByInstance returns every non-soft-deleted row for the instance.
	ListActiveByInstance(ctx context.Context, instanceName domain.InstanceName) ([]movie.StateEntry, error)
}
