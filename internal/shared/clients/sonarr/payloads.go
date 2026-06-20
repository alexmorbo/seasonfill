// Package sonarr — typed payload shapes consumed by E-1's sync worker
// (application/scan/sonarr_sync.go). Distinct from the lean domain
// `series.Series` projection used by the existing scan/evaluate path:
// the sync worker needs richer fields (network, genres, runtime,
// ratings, firstAired, lastAired, nextAiring, previousAiring) that
// the evaluate path doesn't read.
package sonarr

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesPayload is the rich Sonarr series shape consumed by E-1's
// SyncSeriesFromSonarr.
type SeriesPayload struct {
	ID             shareddomain.SonarrSeriesID
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
	TVDBID         shareddomain.TVDBID
	IMDBID         shareddomain.IMDBID
	TMDBID         shareddomain.TMDBID
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
	ID            int
	SeasonNumber  int
	EpisodeIDs    []int
	Path          string
	RelativePath  string
	SizeBytes     int64
	QualityID     int
	QualityName   string
	ReleaseGroup  string
	VideoCodec    string // mediaInfo.videoCodec ("HEVC", "x264")
	AudioCodec    string // mediaInfo.audioCodec ("DDP", "DTS")
	AudioChannels string // mediaInfo.audioChannels ("5.1", "2.0")
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
	ID                    int                         `json:"id"`
	SeriesID              shareddomain.SonarrSeriesID `json:"seriesId,omitempty"`
	EpisodeID             int                         `json:"episodeId,omitempty"`
	SeasonNumber          int                         `json:"seasonNumber,omitempty"`
	Title                 string                      `json:"title"`
	Status                string                      `json:"status,omitempty"`
	TrackedDownloadStatus string                      `json:"trackedDownloadStatus,omitempty"`
	TrackedDownloadState  string                      `json:"trackedDownloadState,omitempty"`
	DownloadID            string                      `json:"downloadId,omitempty"`
	Protocol              string                      `json:"protocol,omitempty"`
	Size                  int64                       `json:"size,omitempty"`
	SizeLeft              int64                       `json:"sizeleft,omitempty"`
	Series                *seriesDTO                  `json:"series,omitempty"`
	Episode               *episodeDTO                 `json:"episode,omitempty"`
}

// QueuePayload is the typed queue response.
type QueuePayload struct {
	TotalRecords int
	Records      []QueueRecord
}

// QueueRecord is one queue entry (PRD §4.5 torrent→series bridge).
type QueueRecord struct {
	ID            int
	SeriesID      shareddomain.SonarrSeriesID
	EpisodeID     int
	SeasonNumber  int
	Title         string
	Status        string
	DownloadID    string
	Protocol      string
	EpisodeNumber int   // story 379 — populated when /queue?includeEpisode=true
	Size          int64 // bytes, 0 when unknown
	SizeLeft      int64 // bytes remaining, 0 when complete/unknown
}

func seriesPayloadFromDTO(d seriesDTO) SeriesPayload {
	p := SeriesPayload{
		ID:             shareddomain.SonarrSeriesID(d.ID),
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
	p := EpisodeFilePayload{
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
	if d.MediaInfo != nil {
		p.VideoCodec = d.MediaInfo.VideoCodec
		p.AudioCodec = d.MediaInfo.AudioCodec
		p.AudioChannels = string(d.MediaInfo.AudioChannels)
	}
	return p
}

// GetSeriesPayload returns the full Sonarr seriesDTO for E-1's
// SyncSeriesFromSonarr. Distinct from GetSeries (lean domain
// projection) so the sync writer has access to network/genres/
// runtime/ratings/previousAiring/nextAiring/firstAired/lastAired.
func (c *Client) GetSeriesPayload(ctx context.Context, id shareddomain.SonarrSeriesID) (SeriesPayload, error) {
	var dto seriesDTO
	if err := c.get(ctx, "/api/v3/series/"+strconv.Itoa(int(id)), nil, &dto); err != nil {
		return SeriesPayload{}, err
	}
	return seriesPayloadFromDTO(dto), nil
}

// ListEpisodesForSync returns the full episode payload for a series
// (overview, runtime, finaleType, absoluteEpisodeNumber — fields the
// existing ListEpisodes intentionally omits).
func (c *Client) ListEpisodesForSync(ctx context.Context, seriesID shareddomain.SonarrSeriesID) ([]EpisodePayload, error) {
	q := url.Values{}
	q.Set("seriesId", strconv.Itoa(int(seriesID)))
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
func (c *Client) ListEpisodeFilesForSync(ctx context.Context, seriesID shareddomain.SonarrSeriesID) ([]EpisodeFilePayload, error) {
	q := url.Values{}
	q.Set("seriesId", strconv.Itoa(int(seriesID)))
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

// QueueAll calls GET /api/v3/queue without a seriesId filter. The
// torrentsync reconciler (PRD §4.5 source 3) does ONE upstream call
// per tick and matches against the in-memory unmapped-hashes set
// locally; per-series fan-out would multiply API load by the number
// of unmapped series and starve the global rate limiter.
//
// pageSize=1000 is Sonarr's effective ceiling — production fleets
// never have more than a few hundred active downloads at once.
func (c *Client) QueueAll(ctx context.Context) (QueuePayload, error) {
	q := url.Values{}
	q.Set("includeSeries", "false")
	q.Set("includeEpisode", "false")
	q.Set("pageSize", "1000")
	var dto queueDTO
	if err := c.get(ctx, "/api/v3/queue", q, &dto); err != nil {
		return QueuePayload{}, fmt.Errorf("queue all: %w", err)
	}
	out := QueuePayload{
		TotalRecords: dto.TotalRecords,
		Records:      make([]QueueRecord, 0, len(dto.Records)),
	}
	for _, r := range dto.Records {
		rec := QueueRecord{
			ID:           r.ID,
			SeriesID:     r.SeriesID,
			EpisodeID:    r.EpisodeID,
			SeasonNumber: r.SeasonNumber,
			Title:        r.Title,
			Status:       r.Status,
			DownloadID:   r.DownloadID,
			Protocol:     r.Protocol,
			Size:         r.Size,
			SizeLeft:     r.SizeLeft,
		}
		if r.Episode != nil {
			rec.EpisodeNumber = r.Episode.EpisodeNumber
			if r.Episode.Title != "" {
				rec.Title = r.Episode.Title
			}
			if rec.SeasonNumber == 0 {
				rec.SeasonNumber = r.Episode.SeasonNumber
			}
		}
		out.Records = append(out.Records, rec)
	}
	return out, nil
}

// HistoryPage is one page of paginated grab history. The reconciler
// (PRD §4.5 source 4) walks pages oldest-cursor-first until either
// the per-tick cap (10) is hit or Sonarr returns fewer than pageSize
// records — the latter signals end-of-data and resets the cursor.
type HistoryPage struct {
	Page         int
	PageSize     int
	TotalRecords int
	Records      []HistoryGrab
}

// HistoryGrab is the per-record projection the reconciler reads.
// Kept distinct from ports.HistoryEvent (which feeds the regrab
// audit path) because the reconciler needs `DownloadID` and
// nothing else — bringing GUID/indexer along would force ports/sonarr
// to grow a hash field it does not need.
type HistoryGrab struct {
	DownloadID   string
	SeriesID     shareddomain.SonarrSeriesID
	SeasonNumber int
}

// GrabHistoryPaged returns one page of /api/v3/history?eventType=1
// for ALL series. Used by the torrentsync reconciler — the per-
// series GrabHistory is kept intact for Story 210's regrab audit.
//
// page is 1-indexed (Sonarr convention). pageSize MUST be the same
// across reconciler calls — otherwise the cursor walks the wrong
// offsets. The reconciler uses 50 (Sonarr's default).
func (c *Client) GrabHistoryPaged(ctx context.Context, page, pageSize int) (HistoryPage, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	q := url.Values{}
	q.Set("eventType", "1")
	q.Set("page", strconv.Itoa(page))
	q.Set("pageSize", strconv.Itoa(pageSize))
	q.Set("sortKey", "date")
	q.Set("sortDirection", "descending")
	var resp historyPagedResponse
	if err := c.get(ctx, "/api/v3/history", q, &resp); err != nil {
		return HistoryPage{}, fmt.Errorf("history page %d: %w", page, err)
	}
	out := HistoryPage{
		Page:         resp.Page,
		PageSize:     resp.PageSize,
		TotalRecords: resp.TotalRecords,
		Records:      make([]HistoryGrab, 0, len(resp.Records)),
	}
	for _, r := range resp.Records {
		if r.DownloadID == "" {
			// Usenet grabs have no torrent hash; the reconciler
			// only maps torrents. Skip silently.
			continue
		}
		series := shareddomain.SonarrSeriesID(0)
		if r.SeriesID != 0 {
			series = r.SeriesID
		}
		season := 0
		if r.Episode != nil {
			season = r.Episode.SeasonNumber
		}
		out.Records = append(out.Records, HistoryGrab{
			DownloadID:   strings.ToLower(r.DownloadID),
			SeriesID:     series,
			SeasonNumber: season,
		})
	}
	return out, nil
}

// Queue calls GET /api/v3/queue?seriesId={id}&includeSeries=true&
// includeEpisode=true. Used by the torrentsync reconciler (PRD §4.5)
// to bridge active downloads → series; E-1 lands the client method
// so the reconciler story can pick it up.
func (c *Client) Queue(ctx context.Context, seriesID shareddomain.SonarrSeriesID) (QueuePayload, error) {
	q := url.Values{}
	q.Set("seriesId", strconv.Itoa(int(seriesID)))
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
		rec := QueueRecord{
			ID:           r.ID,
			SeriesID:     r.SeriesID,
			EpisodeID:    r.EpisodeID,
			SeasonNumber: r.SeasonNumber,
			Title:        r.Title,
			Status:       r.Status,
			DownloadID:   r.DownloadID,
			Protocol:     r.Protocol,
			Size:         r.Size,
			SizeLeft:     r.SizeLeft,
		}
		if r.Episode != nil {
			rec.EpisodeNumber = r.Episode.EpisodeNumber
			if r.Episode.Title != "" {
				rec.Title = r.Episode.Title
			}
			if rec.SeasonNumber == 0 {
				rec.SeasonNumber = r.Episode.SeasonNumber
			}
		}
		out.Records = append(out.Records, rec)
	}
	return out, nil
}
