package sonarr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

type Client struct {
	name    string
	baseURL string
	apiKey  string
	http    *http.Client
	limiter *ratelimit.Limiter
	logger  *slog.Logger
}

func New(name, baseURL, apiKey string, timeout time.Duration, logger *slog.Logger) *Client {
	return NewWithLimiter(name, baseURL, apiKey, timeout, ratelimit.New(0, 0), logger)
}

func NewWithLimiter(name, baseURL, apiKey string, timeout time.Duration, limiter *ratelimit.Limiter, logger *slog.Logger) *Client {
	return &Client{
		name:    name,
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: timeout,
		},
		limiter: limiter,
		logger:  logger,
	}
}

func (c *Client) Name() string { return c.name }

func (c *Client) do(ctx context.Context, req *http.Request, endpoint string, out any) error {
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if req.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limit wait %s: %w", endpoint, err)
		}
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	dur := time.Since(start).Seconds()

	if err != nil {
		observability.SonarrAPIRequest(c.name, endpoint, "error")
		observability.ObserveSonarrAPIDuration(c.name, endpoint, dur)
		return fmt.Errorf("call %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	observability.ObserveSonarrAPIDuration(c.name, endpoint, dur)
	observability.SonarrAPIRequest(c.name, endpoint, strconv.Itoa(resp.StatusCode))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &StatusError{Endpoint: endpoint, Status: resp.StatusCode, Body: string(body)}
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, endpoint string, query url.Values, out any) error {
	full := c.baseURL + endpoint
	if query != nil {
		full += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return fmt.Errorf("build request %s: %w", endpoint, err)
	}
	return c.do(ctx, req, endpoint, out)
}

func (c *Client) post(ctx context.Context, endpoint string, body any, out any) error {
	full := c.baseURL + endpoint
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode body %s: %w", endpoint, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, full, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("build request %s: %w", endpoint, err)
	}
	return c.do(ctx, req, endpoint, out)
}

func (c *Client) SystemStatus(ctx context.Context) (ports.SystemStatus, error) {
	var dto systemStatusDTO
	if err := c.get(ctx, "/api/v3/system/status", nil, &dto); err != nil {
		return ports.SystemStatus{}, err
	}
	return ports.SystemStatus{Version: dto.Version, InstanceURL: dto.InstanceURL}, nil
}

func (c *Client) ListSeries(ctx context.Context) ([]series.Series, error) {
	var dtos []seriesDTO
	if err := c.get(ctx, "/api/v3/series", nil, &dtos); err != nil {
		return nil, err
	}
	out := make([]series.Series, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, toSeries(d))
	}
	return out, nil
}

func (c *Client) GetSeries(ctx context.Context, id int) (series.Series, error) {
	var dto seriesDTO
	if err := c.get(ctx, "/api/v3/series/"+strconv.Itoa(id), nil, &dto); err != nil {
		return series.Series{}, err
	}
	return toSeries(dto), nil
}

func (c *Client) ListEpisodes(ctx context.Context, seriesID, seasonNumber int) ([]series.Episode, error) {
	q := url.Values{}
	q.Set("seriesId", strconv.Itoa(seriesID))
	q.Set("seasonNumber", strconv.Itoa(seasonNumber))
	var dtos []episodeDTO
	if err := c.get(ctx, "/api/v3/episode", q, &dtos); err != nil {
		return nil, err
	}
	out := make([]series.Episode, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, series.Episode{
			ID:            d.ID,
			Number:        d.EpisodeNumber,
			SeasonNumber:  d.SeasonNumber,
			Title:         d.Title,
			Monitored:     d.Monitored,
			HasFile:       d.HasFile,
			AirDateUTC:    d.AirDateUtc,
			EpisodeFileID: d.EpisodeFileID,
		})
	}
	return out, nil
}

func (c *Client) ListEpisodeFiles(ctx context.Context, seriesID int) (map[int]int, error) {
	q := url.Values{}
	q.Set("seriesId", strconv.Itoa(seriesID))
	var dtos []episodeFileDTO
	if err := c.get(ctx, "/api/v3/episodeFile", q, &dtos); err != nil {
		return nil, err
	}
	out := make(map[int]int, len(dtos))
	for _, d := range dtos {
		out[d.ID] = d.Quality.Quality.ID
	}
	return out, nil
}

func (c *Client) SearchReleases(ctx context.Context, seriesID, seasonNumber int) ([]release.Release, error) {
	q := url.Values{}
	q.Set("seriesId", strconv.Itoa(seriesID))
	q.Set("seasonNumber", strconv.Itoa(seasonNumber))
	var dtos []releaseDTO
	if err := c.get(ctx, "/api/v3/release", q, &dtos); err != nil {
		return nil, err
	}
	out := make([]release.Release, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, release.Release{
			GUID:                 d.GUID,
			Title:                d.Title,
			IndexerID:            d.IndexerID,
			IndexerName:          d.Indexer,
			Protocol:             d.Protocol,
			QualityID:            d.Quality.Quality.ID,
			QualityName:          d.Quality.Quality.Name,
			CustomFormatScore:    d.CustomFormatScore,
			Seeders:              d.Seeders,
			Leechers:             d.Leechers,
			SizeBytes:            d.Size,
			MappedEpisodeNumbers: d.MappedEpisodeNumbers,
			MappedSeasonNumber:   d.MappedSeasonNumber,
			Rejections:           d.Rejections,
			PublishedUTC:         d.PublishDate,
			IsFullSeason:         d.FullSeason,
		})
	}
	return out, nil
}

func (c *Client) GetQualityProfile(ctx context.Context, id int) (ports.QualityProfile, error) {
	var dto qualityProfileDTO
	if err := c.get(ctx, "/api/v3/qualityprofile/"+strconv.Itoa(id), nil, &dto); err != nil {
		return ports.QualityProfile{}, err
	}
	prof := ports.QualityProfile{ID: dto.ID, Name: dto.Name}
	order := 0
	for _, it := range dto.Items {
		order++
		if it.Quality != nil {
			if it.Allowed {
				prof.Items = append(prof.Items, ports.QualityItem{
					ID:    it.Quality.ID,
					Name:  it.Quality.Name,
					Order: order,
				})
			}
			continue
		}
		for _, sub := range it.Items {
			if sub.Quality != nil && (sub.Allowed || it.Allowed) {
				prof.Items = append(prof.Items, ports.QualityItem{
					ID:    sub.Quality.ID,
					Name:  sub.Quality.Name,
					Order: order,
				})
			}
		}
	}
	return prof, nil
}

func (c *Client) ListIndexers(ctx context.Context) ([]ports.Indexer, error) {
	var dtos []indexerDTO
	if err := c.get(ctx, "/api/v3/indexer", nil, &dtos); err != nil {
		return nil, err
	}
	out := make([]ports.Indexer, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, ports.Indexer{ID: d.ID, Name: d.Name, Priority: d.Priority})
	}
	return out, nil
}

func (c *Client) ListTags(ctx context.Context) ([]ports.Tag, error) {
	var dtos []tagDTO
	if err := c.get(ctx, "/api/v3/tag", nil, &dtos); err != nil {
		return nil, err
	}
	out := make([]ports.Tag, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, ports.Tag{ID: d.ID, Label: d.Label})
	}
	return out, nil
}

func (c *Client) GrabHistory(ctx context.Context, seriesID int) ([]ports.HistoryEvent, error) {
	q := url.Values{}
	q.Set("seriesId", strconv.Itoa(seriesID))
	q.Set("eventType", "1")
	q.Set("pageSize", "50")
	var resp historyResponse
	if err := c.get(ctx, "/api/v3/history", q, &resp); err != nil {
		return nil, err
	}
	out := make([]ports.HistoryEvent, 0, len(resp.Records))
	for _, r := range resp.Records {
		ev := ports.HistoryEvent{}
		if r.Episode != nil {
			ev.EpisodeNumber = r.Episode.EpisodeNumber
			ev.SeasonNumber = r.Episode.SeasonNumber
		}
		if v, ok := r.Data["guid"].(string); ok {
			ev.GUID = v
		}
		switch {
		case r.Indexer != "":
			ev.IndexerName = r.Indexer
		default:
			if v, ok := r.Data["indexer"].(string); ok {
				ev.IndexerName = v
			}
		}
		out = append(out, ev)
	}
	return out, nil
}

// ForceGrab calls POST /api/v3/release — the endpoint Sonarr UI uses for
// "Override and add to Download Queue". On success Sonarr returns 2xx; on
// permanent failure 4xx; on Sonarr internal error 5xx.
func (c *Client) ForceGrab(ctx context.Context, guid string, indexerID int) error {
	body := forceGrabRequest{GUID: guid, IndexerID: indexerID}
	return c.post(ctx, "/api/v3/release", body, nil)
}

func toSeries(d seriesDTO) series.Series {
	st := series.SeriesTypeStandard
	switch d.SeriesType {
	case "anime":
		st = series.SeriesTypeAnime
	case "daily":
		st = series.SeriesTypeDaily
	}
	seasons := make([]series.Season, 0, len(d.Seasons))
	for _, s := range d.Seasons {
		seasons = append(seasons, series.Season{Number: s.SeasonNumber, Monitored: s.Monitored})
	}
	return series.Series{
		ID:             d.ID,
		Title:          d.Title,
		Type:           st,
		Monitored:      d.Monitored,
		TagIDs:         d.Tags,
		QualityProfile: d.QualityProfile,
		Seasons:        seasons,
	}
}
