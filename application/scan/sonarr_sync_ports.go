// Package scan — E-1 (Story 210) sync ports.
//
// Narrow repository port interfaces the new SyncSeriesFromSonarr
// depends on. Kept in this file (not application/ports) so the
// existing scan_usecase deps stay untouched — E-1 is additive.
package scan

import (
	"context"

	"github.com/alexmorbo/seasonfill/domain/series"
)

// SeriesCanonRepository is the subset of SeriesRepository E-1 needs.
type SeriesCanonRepository interface {
	FindByExternalIDs(ctx context.Context, tmdbID *int, tvdbID *int, imdbID *string) (series.Canon, error)
	Upsert(ctx context.Context, c series.Canon) (int64, error)
}

// SyncSeriesCacheRepository is the subset E-1 needs for the thin
// per-instance projection upsert + soft-delete.
type SyncSeriesCacheRepository interface {
	Upsert(ctx context.Context, e series.CacheEntry) error
	SoftDelete(ctx context.Context, instanceName string, sonarrSeriesID int) error
}

// EpisodesRepository — BatchUpsert is the E-1 fast path; one
// INSERT … ON CONFLICT per series instead of per episode.
type EpisodesRepository interface {
	BatchUpsert(ctx context.Context, episodes []series.CanonEpisode) ([]int64, error)
	ListBySeries(ctx context.Context, seriesID int64) ([]series.CanonEpisode, error)
}

// EpisodeStatesRepository — per-instance state writer. Idempotent
// upsert per (instance_name, episode_id).
type EpisodeStatesRepository interface {
	Upsert(ctx context.Context, s series.EpisodeState) error
}

// EpisodeTextsRepository — episode_texts(en-US) writer for the
// Sonarr-shipped title (TMDB enrichment later writes ru / overwrites
// en when TMDB has authority).
type EpisodeTextsRepository interface {
	Upsert(ctx context.Context, t series.EpisodeText) error
}

// GenresPort — ResolveByName (en-US, "Drama") + Upsert (create
// canon + i18n row) + Set (write series_genres join). Idempotent.
type GenresPort interface {
	ResolveByName(ctx context.Context, language, name string) (int64, error)
	Upsert(ctx context.Context, g GenreStub) (int64, error)
	UpsertI18n(ctx context.Context, genreID int64, language, name string) error
	Set(ctx context.Context, seriesID int64, genreIDs []int64) error
}

// GenreStub is the writable subset for the create-on-miss path.
// We do not import domain/taxonomy here to keep ports independent.
type GenreStub struct {
	TMDBID *int
}

// NetworksPort — name-keyed lookup + create-on-miss + join writer.
// Networks have no UQ on name in the schema; the port resolves by
// name (returning the first id) and creates a row on miss.
type NetworksPort interface {
	ResolveByName(ctx context.Context, name string) (int64, error)
	UpsertByName(ctx context.Context, name string) (int64, error)
	SetForSeries(ctx context.Context, seriesID int64, networkIDs []int64) error
}
