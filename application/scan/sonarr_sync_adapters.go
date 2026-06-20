// Package scan — E-1 syncer adapters wiring the typed infrastructure
// repositories to the SyncDeps ports.
package scan

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type genresAdapter struct {
	genres *persistence.GenresRepository
	i18n   *persistence.GenresI18nRepository
}

// NewGenresAdapter wires GenresRepository + GenresI18nRepository as a
// GenresPort implementation. Façade; no behaviour of its own.
func NewGenresAdapter(g *persistence.GenresRepository, i *persistence.GenresI18nRepository) GenresPort {
	return &genresAdapter{genres: g, i18n: i}
}

func (a *genresAdapter) ResolveByName(ctx context.Context, language, name string) (int64, error) {
	return a.genres.ResolveByName(ctx, language, name)
}

func (a *genresAdapter) Upsert(ctx context.Context, g GenreStub) (int64, error) {
	return a.genres.Upsert(ctx, taxonomy.Genre{TMDBID: g.TMDBID})
}

func (a *genresAdapter) UpsertI18n(ctx context.Context, genreID int64, language, name string) error {
	return a.i18n.Upsert(ctx, taxonomy.GenreI18n{
		GenreID:   genreID,
		Language:  language,
		Name:      name,
		UpdatedAt: time.Now().UTC(),
	})
}

func (a *genresAdapter) Set(ctx context.Context, seriesID domain.SeriesID, ids []int64) error {
	return a.genres.Set(ctx, seriesID, ids)
}

type networksAdapter struct {
	networks *persistence.NetworksRepository
}

// NewNetworksAdapter wires NetworksRepository as a NetworksPort.
func NewNetworksAdapter(n *persistence.NetworksRepository) NetworksPort {
	return &networksAdapter{networks: n}
}

func (a *networksAdapter) ResolveByName(ctx context.Context, name string) (int64, error) {
	return a.networks.ResolveByName(ctx, name)
}

func (a *networksAdapter) UpsertByName(ctx context.Context, name string) (int64, error) {
	return a.networks.Upsert(ctx, taxonomy.Network{Name: name})
}

func (a *networksAdapter) SetForSeries(ctx context.Context, seriesID domain.SeriesID, ids []int64) error {
	return a.networks.Set(ctx, seriesID, ids)
}

// SonarrClientLookup resolves a Sonarr client by instance name.
type SonarrClientLookup func(instanceName domain.InstanceName) (*sonarr.Client, bool)

// Syncer is the SeriesSyncer implementation for the webhook handler.
// Owns the Sonarr client lookup + the SyncDeps for the entity-model writes.
type Syncer struct {
	Deps   SyncDeps
	Lookup SonarrClientLookup
	Logger *slog.Logger
}

// SyncFromSonarrAPI fetches the three Sonarr payloads (series, episodes,
// episode files) and calls SyncSeriesFromSonarr.
func (s *Syncer) SyncFromSonarrAPI(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) error {
	client, ok := s.Lookup(instanceName)
	if !ok {
		return fmt.Errorf("sync from sonarr: unknown instance %q", instanceName)
	}
	sp, err := client.GetSeriesPayload(ctx, sonarrSeriesID)
	if err != nil {
		return fmt.Errorf("sync from sonarr: get series: %w", err)
	}
	episodes, err := client.ListEpisodesForSync(ctx, sonarrSeriesID)
	if err != nil {
		return fmt.Errorf("sync from sonarr: list episodes: %w", err)
	}
	files, err := client.ListEpisodeFilesForSync(ctx, sonarrSeriesID)
	if err != nil {
		return fmt.Errorf("sync from sonarr: list episode files: %w", err)
	}
	bundle := SonarrPayloadBundle{Series: sp, Episodes: episodes, EpisodeFiles: files}
	_, err = SyncSeriesFromSonarr(ctx, s.Deps, instanceName, bundle)
	return err
}
