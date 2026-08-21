package torrentsync

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ADR-0023 B1.1 — the movie side of the torrent bridge. Mirrors
// MapRepo / MapRow / LookupRepo (reconciler.go, ports.go) for Radarr.
// Foundation only: B1.2 (grab webhook) and B1.3 (queue/history
// reconciler) are the first writers, B1.4 the first reader.

// MovieMapSource discriminates the path that produced the row.
// Values match the strings persisted to torrent_movie_map.source.
type MovieMapSource string

const (
	MovieMapSourceWebhook       MovieMapSource = "webhook"
	MovieMapSourceRadarrQueue   MovieMapSource = "radarr_queue"
	MovieMapSourceRadarrHistory MovieMapSource = "radarr_history"
)

// MovieProvenance records HOW the download came to exist — orthogonal
// to MovieMapSource (which records how WE learned about it). No series
// equivalent: the Sonarr flow only ever produces the search path.
// Values match torrent_movie_map.provenance.
type MovieProvenance string

const (
	MovieProvenanceRadarrSearch MovieProvenance = "radarr_search"
	MovieProvenanceManualImport MovieProvenance = "manual_import"
)

// MovieMapRow is the row payload for MovieMapRepo.Upsert.
// No SeasonNumber — movies have no seasons.
type MovieMapRow struct {
	Instance      domain.InstanceName
	Hash          string
	RadarrMovieID domain.RadarrMovieID
	Source        MovieMapSource
	Provenance    MovieProvenance
	CreatedAt     time.Time
}

// MovieMapRepo is the narrow write port over torrent_movie_map.
// Implemented in production by
// catalogpersistence.TorrentMovieMapRepository.
type MovieMapRepo interface {
	// Upsert writes one (instance, hash) row. First-source-wins: a row
	// that already exists is NOT overwritten — radarr_movie_id, source
	// and provenance stay as they were on first insert. Returns nil for
	// "row already exists" (successful no-op).
	Upsert(ctx context.Context, row MovieMapRow) error

	// UpsertTx is Upsert joined to an existing tx scope on ctx. Used by
	// the webhook path (B1.2) so the bridge write lands in the same tx
	// as the rest of the webhook side effects.
	UpsertTx(ctx context.Context, row MovieMapRow) error
}

// MovieMapEntry is the (hash, source, provenance) projection the B1.4 read
// path needs. HashesForMovie answers "which hashes"; EntriesForMovie answers
// "which hashes AND how each download came to exist" — GET
// /movies/:tmdb_id/torrents surfaces provenance per row so the UI can badge a
// manual import differently from a Radarr search grab. No series twin exists:
// torrent_series_map carries no provenance column.
type MovieMapEntry struct {
	Hash       string
	Source     MovieMapSource
	Provenance MovieProvenance
}

// MovieLookupRepo is the narrow read port over torrent_movie_map —
// every hash ever mapped to (instance, radarr_movie_id), regardless of
// source. Twin of LookupRepo.HashesForSeries.
type MovieLookupRepo interface {
	HashesForMovie(ctx context.Context, instance domain.InstanceName, radarrMovieID domain.RadarrMovieID) ([]string, error)
	// EntriesForMovie is HashesForMovie plus the source/provenance columns.
	// Deterministic order (torrent_hash ASC) so a caller that logs the set
	// gets a stable line; the read path re-sorts by added_on anyway.
	EntriesForMovie(ctx context.Context, instance domain.InstanceName, radarrMovieID domain.RadarrMovieID) ([]MovieMapEntry, error)
}
