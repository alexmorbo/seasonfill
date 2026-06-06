package ports

import (
	"context"

	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
)

type QualityItem struct {
	ID     int
	Name   string
	Order  int
	Weight int
}

type QualityProfile struct {
	ID    int
	Name  string
	Items []QualityItem
}

type Indexer struct {
	ID       int
	Name     string
	Priority int
}

type HistoryEvent struct {
	EpisodeNumber int
	SeasonNumber  int
	GUID          string
	IndexerName   string
	IndexerID     int
	OccurredAtUTC string
}

type SystemStatus struct {
	Version     string
	InstanceURL string
}

type Tag struct {
	ID    int
	Label string
}

//go:generate moq -out sonarr_mock.go . SonarrClient

type SonarrClient interface {
	SystemStatus(ctx context.Context) (SystemStatus, error)
	ListSeries(ctx context.Context) ([]series.Series, error)
	// ListSeriesCache fetches the same /api/v3/series payload as
	// ListSeries but maps to the richer series.CacheEntry shape used by
	// the series_cache repository (041e). instanceName is stamped onto
	// every returned entry — Sonarr does not echo it.
	ListSeriesCache(ctx context.Context, instanceName string) ([]series.CacheEntry, error)
	GetSeries(ctx context.Context, id int) (series.Series, error)
	ListEpisodes(ctx context.Context, seriesID, seasonNumber int) ([]series.Episode, error)
	ListEpisodeFiles(ctx context.Context, seriesID int) (map[int]int, error)
	SearchReleases(ctx context.Context, seriesID, seasonNumber int) ([]release.Release, error)
	GetQualityProfile(ctx context.Context, id int) (QualityProfile, error)
	ListIndexers(ctx context.Context) ([]Indexer, error)
	ListTags(ctx context.Context) ([]Tag, error)
	GrabHistory(ctx context.Context, seriesID int) ([]HistoryEvent, error)
	ForceGrab(ctx context.Context, guid string, indexerID int) (string, error)
	Name() string
}
