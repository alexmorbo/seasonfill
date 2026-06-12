// Package sonarr — typed payload shapes consumed by E-1's sync worker
// (application/scan/sonarr_sync.go). Distinct from the lean domain
// `series.Series` projection used by the existing scan/evaluate path:
// the sync worker needs richer fields (network, genres, runtime,
// ratings, firstAired, lastAired, nextAiring, previousAiring) that
// the evaluate path doesn't read.
package sonarr

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/alexmorbo/seasonfill/domain/series"
)

// SeriesPayload is the rich Sonarr series shape consumed by E-1's
// SyncSeriesFromSonarr.
type SeriesPayload struct {
	ID             int
	Title          string
	SortTitle      string
	TitleSlug      string
	Year           int
	Status         string
	Network        string
	Genres         []string
	Runtime        int
	Overview       string
	Monitored      bool
	QualityProfile int
	Tags           []int
	TVDBID         int
	IMDBID         string
	TMDBID         int
	PreviousAiring *time.Time
	NextAiring     *time.Time
	FirstAired     *time.Time
	LastAired      *time.Time
	Ratings        *RatingsPayload
	Seasons        []series.Season
	Statistics     series.Statistics
}

// RatingsPayload mirrors Sonarr's nested ratings block.
type RatingsPayload struct {
	Votes int
	Value float64
}

// EpisodePayload carries the full Sonarr episode shape for E-1.
type EpisodePayload struct {
	ID             int
	EpisodeNumber  int
	SeasonNumber   int
	Title          string
	Overview       string
	Monitored      bool
	HasFile        bool
	AirDateUTC     time.Time
	Runtime        int
	FinaleType     string
	AbsoluteNumber *int
	EpisodeFileID  int
}

// EpisodeFilePayload carries the full Sonarr episode-file shape for E-1.
type EpisodeFilePayload struct {
	ID           int
	SeasonNumber int
	EpisodeIDs   []int
	Path         string
	RelativePath string
	SizeBytes    int64
	QualityID    int
	QualityName  string
	ReleaseGroup string
}

// queueDTO mirrors Sonarr's GET /api/v3/queue response.
type queueDTO struct {
	Page          int              `json:"page"`
	PageSize      int              `json:"pageSize"`
	SortKey       string           `json:"sortKey"`
	SortDirection string           `json:"sortDirection"`
	TotalRecords  int              `json:"totalRecords"`
	Records       []queueRecordDTO `json:"records"`
}

type queueRecordDTO struct {
	ID                    int         `json:"id"`
	SeriesID              int         `json:"seriesId,omitempty"`
	EpisodeID             int         `json:"episodeId,omitempty"`
	SeasonNumber          int         `json:"seasonNumber,omitempty"`
	Title                 string      `json:"title"`
	Status                string      `json:"status,omitempty"`
	TrackedDownloadStatus string      `json:"trackedDownloadStatus,omitempty"`
	TrackedDownloadState  string      `json:"trackedDownloadState,omitempty"`
	DownloadID            string      `json:"downloadId,omitempty"`
	Protocol              string      `json:"protocol,omitempty"`
	Series                *seriesDTO  `json:"series,omitempty"`
	Episode               *episodeDTO `json:"episode,omitempty"`
}

// QueuePayload is the typed queue response.
type QueuePayload struct {
	TotalRecords int
	Records      []QueueRecord
}

// QueueRecord is one queue entry (PRD §4.5 torrent→series bridge).
type QueueRecord struct {
	ID           int
	SeriesID     int
	EpisodeID    int
	SeasonNumber int
	Title        string
	Status       string
	DownloadID   string
	Protocol     string
}

func seriesPayloadFromDTO(d seriesDTO) SeriesPayload {
	p := SeriesPayload{
		ID:             d.ID,
		Title:          d.Title,
		SortTitle:      d.SortTitle,
		TitleSlug:      d.TitleSlug,
		Year:           d.Year,
		Status:         d.Status,
		Network:        d.Network,
		Genres:         append([]string(nil), d.Genres...),
		Runtime:        d.Runtime,
		Overview:       d.Overview,
		Monitored:      d.Monitored,
		QualityProfile: d.QualityProfile,
		Tags:           append([]int(nil), d.Tags...),
		TVDBID:         d.TVDBID,
		IMDBID:         d.IMDBID,
		TMDBID:         d.TMDBID,
		PreviousAiring: d.PreviousAiring,
		NextAiring:     d.NextAiring,
		FirstAired:     d.FirstAired,
		LastAired:      d.LastAired,
		Statistics:     toStatistics(d.Statistics),
	}
	if d.Ratings != nil {
		p.Ratings = &RatingsPayload{Votes: d.Ratings.Votes, Value: d.Ratings.Value}
	}
	for _, s := range d.Seasons {
		p.Seasons = append(p.Seasons, series.Season{
			Number:     s.SeasonNumber,
			Monitored:  s.Monitored,
			Statistics: toStatistics(s.Statistics),
		})
	}
	return p
}

func episodePayloadFromDTO(d episodeDTO) EpisodePayload {
	return EpisodePayload{
		ID:             d.ID,
		EpisodeNumber:  d.EpisodeNumber,
		SeasonNumber:   d.SeasonNumber,
		Title:          d.Title,
		Overview:       d.Overview,
		Monitored:      d.Monitored,
		HasFile:        d.HasFile,
		AirDateUTC:     d.AirDateUtc,
		Runtime:        d.Runtime,
		FinaleType:     d.FinaleType,
		AbsoluteNumber: d.AbsoluteEpisodeNumber,
		EpisodeFileID:  d.EpisodeFileID,
	}
}

func episodeFilePayloadFromDTO(d episodeFileDTO) EpisodeFilePayload {
	return EpisodeFilePayload{
		ID:           d.ID,
		SeasonNumber: d.SeasonNumber,
		EpisodeIDs:   append([]int(nil), d.EpisodeIDs...),
		Path:         d.Path,
		RelativePath: d.RelativePath,
		SizeBytes:    d.Size,
		QualityID:    d.Quality.Quality.ID,
		QualityName:  d.Quality.Quality.Name,
		ReleaseGroup: d.ReleaseGroup,
	}
}

// GetSeriesPayload returns the full Sonarr seriesDTO for E-1's
// SyncSeriesFromSonarr. Distinct from GetSeries (lean domain
// projection) so the sync writer has access to network/genres/
// runtime/ratings/previousAiring/nextAiring/firstAired/lastAired.
func (c *Client) GetSeriesPayload(ctx context.Context, id int) (SeriesPayload, error) {
	var dto seriesDTO
	if err := c.get(ctx, "/api/v3/series/"+strconv.Itoa(id), nil, &dto); err != nil {
		return SeriesPayload{}, err
	}
	return seriesPayloadFromDTO(dto), nil
}

// ListEpisodesForSync returns the full episode payload for a series
// (overview, runtime, finaleType, absoluteEpisodeNumber — fields the
// existing ListEpisodes intentionally omits).
func (c *Client) ListEpisodesForSync(ctx context.Context, seriesID int) ([]EpisodePayload, error) {
	q := url.Values{}
	q.Set("seriesId", strconv.Itoa(seriesID))
	var dtos []episodeDTO
	if err := c.get(ctx, "/api/v3/episode", q, &dtos); err != nil {
		return nil, err
	}
	out := make([]EpisodePayload, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, episodePayloadFromDTO(d))
	}
	return out, nil
}

// ListEpisodeFilesForSync returns the full episode-file payload (path,
// releaseGroup, episodeIds, quality) for the series — E-1 walks
// every file for the per-instance episode_states upsert.
func (c *Client) ListEpisodeFilesForSync(ctx context.Context, seriesID int) ([]EpisodeFilePayload, error) {
	q := url.Values{}
	q.Set("seriesId", strconv.Itoa(seriesID))
	var dtos []episodeFileDTO
	if err := c.get(ctx, "/api/v3/episodeFile", q, &dtos); err != nil {
		return nil, err
	}
	out := make([]EpisodeFilePayload, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, episodeFilePayloadFromDTO(d))
	}
	return out, nil
}

// Queue calls GET /api/v3/queue?seriesId={id}&includeSeries=true&
// includeEpisode=true. Used by the torrentsync reconciler (PRD §4.5)
// to bridge active downloads → series; E-1 lands the client method
// so the reconciler story can pick it up.
func (c *Client) Queue(ctx context.Context, seriesID int) (QueuePayload, error) {
	q := url.Values{}
	q.Set("seriesId", strconv.Itoa(seriesID))
	q.Set("includeSeries", "true")
	q.Set("includeEpisode", "true")
	q.Set("pageSize", "1000")
	var dto queueDTO
	if err := c.get(ctx, "/api/v3/queue", q, &dto); err != nil {
		return QueuePayload{}, err
	}
	out := QueuePayload{
		TotalRecords: dto.TotalRecords,
		Records:      make([]QueueRecord, 0, len(dto.Records)),
	}
	for _, r := range dto.Records {
		out.Records = append(out.Records, QueueRecord{
			ID:           r.ID,
			SeriesID:     r.SeriesID,
			EpisodeID:    r.EpisodeID,
			SeasonNumber: r.SeasonNumber,
			Title:        r.Title,
			Status:       r.Status,
			DownloadID:   r.DownloadID,
			Protocol:     r.Protocol,
		})
	}
	return out, nil
}
