package sonarr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

type Client struct {
	name    string
	baseURL string
	apiKey  string
	// http is the default client (used by every endpoint EXCEPT
	// SearchReleases). Timeout = SonarrInstance.Timeout.
	http *http.Client
	// httpSearch is the long-timeout client used ONLY by
	// SearchReleases. When WithSearchTimeout is not supplied (or is
	// zero), httpSearch aliases `http` — i.e. behaviour identical to
	// the pre-015 single-client model.
	httpSearch *http.Client
	limiter    *ratelimit.Limiter
	// global is set by WithGlobalLimiter (frozen at construction).
	// globalPtr is set by WithGlobalLimiterPointer (live-reloaded).
	// The two are mutually exclusive — the pointer wins if both are
	// supplied (last write wins in functional-options order).
	global    *ratelimit.Limiter
	globalPtr *atomic.Pointer[ratelimit.Limiter]
	// poster is a dedicated limiter for MediaCover requests. It exists
	// so frontend-driven poster bursts can't starve watchdog
	// SystemStatus calls that share the global limiter. nil = unlimited.
	poster *ratelimit.Limiter
	logger *slog.Logger
}

// Option configures a Client at construction.
type Option func(*Client)

// WithGlobalLimiter sets the shared global limiter for cross-instance
// protection. Pass nil for unlimited.
func WithGlobalLimiter(l *ratelimit.Limiter) Option {
	return func(c *Client) { c.global = l }
}

// WithGlobalLimiterPointer captures an atomic pointer to the live
// global limiter. The client reads the pointer on every API call
// so reload-time swaps take effect immediately. nil-safe: a nil
// load means "no global rate limit on this call".
func WithGlobalLimiterPointer(p *atomic.Pointer[ratelimit.Limiter]) Option {
	return func(c *Client) {
		if p != nil {
			c.globalPtr = p
			c.global = nil
		}
	}
}

// globalLimiter returns the current global limiter (or nil for
// unlimited). Callers must nil-check before invoking Wait.
func (c *Client) globalLimiter() *ratelimit.Limiter {
	if c.globalPtr != nil {
		return c.globalPtr.Load()
	}
	return c.global
}

// WithPosterLimiter sets the dedicated MediaCover rate limiter. The
// poster path does NOT share the global limiter — bursts from the
// frontend grid (60+ posters/page) would otherwise starve concurrent
// /system/status calls used by the watchdog. Pass nil for unlimited.
func WithPosterLimiter(l *ratelimit.Limiter) Option {
	return func(c *Client) { c.poster = l }
}

// WithSearchTimeout installs a separate http.Client used ONLY by
// SearchReleases. Pass 0 (or negative) to keep the base-timeout
// client for search too — defensive default for operators who don't
// opt in. The base http.Client (and its connection pool via
// http.DefaultTransport) is unchanged.
func WithSearchTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d <= 0 {
			return
		}
		c.httpSearch = &http.Client{Timeout: d}
	}
}

func New(name, baseURL, apiKey string, timeout time.Duration, logger *slog.Logger) *Client {
	return NewWithOptions(name, baseURL, apiKey, timeout, nil, logger)
}

func NewWithLimiter(name, baseURL, apiKey string, timeout time.Duration, limiter *ratelimit.Limiter, logger *slog.Logger) *Client {
	return NewWithOptions(name, baseURL, apiKey, timeout, limiter, logger)
}

// NewWithOptions constructs a Client and applies functional options.
// Default httpSearch aliases http (= same timeout as every other
// endpoint). WithSearchTimeout, if applied, overrides httpSearch
// with a longer-timeout client for SearchReleases only.
func NewWithOptions(name, baseURL, apiKey string, timeout time.Duration, limiter *ratelimit.Limiter, logger *slog.Logger, opts ...Option) *Client {
	base := &http.Client{Timeout: timeout}
	c := &Client{
		name:       name,
		baseURL:    baseURL,
		apiKey:     apiKey,
		http:       base,
		httpSearch: base, // default alias — overridden by WithSearchTimeout
		limiter:    limiter,
		logger:     logger,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) Name() string { return c.name }

func (c *Client) do(ctx context.Context, req *http.Request, endpoint string, out any) error {
	return c.doWithClient(ctx, c.http, req, endpoint, out)
}

// doWithClient is the workhorse that lets callers pick which
// http.Client (and therefore which timeout) to use. SearchReleases
// supplies c.httpSearch; everything else funnels through c.http via
// the thin `do` wrapper above.
func (c *Client) doWithClient(ctx context.Context, hc *http.Client, req *http.Request, endpoint string, out any) error {
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if req.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Per-instance limiter first, then global. Both nil-safe and honor ctx.
	// When the wait queue outruns ctx, surface as ErrInstanceSelfThrottled
	// — distinct from "Sonarr is down" — so the healthcheck can transition
	// the instance to SelfThrottled instead of UnavailableUnknown.
	if err := ratelimit.Wait(c.limiter, ctx); err != nil {
		if errors.Is(err, ratelimit.ErrSelfThrottled) {
			return fmt.Errorf("rate limit wait %s: %w", endpoint, errors.Join(err, domain.ErrInstanceSelfThrottled))
		}
		return fmt.Errorf("rate limit wait %s: %w", endpoint, err)
	}
	if err := ratelimit.Wait(c.globalLimiter(), ctx); err != nil {
		if errors.Is(err, ratelimit.ErrSelfThrottled) {
			return fmt.Errorf("global rate limit wait %s: %w", endpoint, errors.Join(err, domain.ErrInstanceSelfThrottled))
		}
		return fmt.Errorf("global rate limit wait %s: %w", endpoint, err)
	}

	start := time.Now()
	resp, err := hc.Do(req)
	dur := time.Since(start).Seconds()

	if err != nil {
		observability.SonarrAPIRequest(c.name, endpoint, "error")
		observability.ObserveSonarrAPIDuration(c.name, endpoint, dur)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("call %s: %w", endpoint, ctxErr)
		}
		// Transport errors (DNS/connect/timeout) join the network sentinel so
		// the scan/watchdog can classify without re-parsing url.Error.
		return fmt.Errorf("call %s: %w", endpoint, errors.Join(err, domain.ErrInstanceNetwork))
	}
	defer func() { _ = resp.Body.Close() }()

	observability.ObserveSonarrAPIDuration(c.name, endpoint, dur)
	observability.SonarrAPIRequest(c.name, endpoint, strconv.Itoa(resp.StatusCode))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		se := &StatusError{Endpoint: endpoint, Status: resp.StatusCode, Body: string(body)}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return fmt.Errorf("%w: %w", domain.ErrInstanceUnauthorized, se)
		}
		return se
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

// searchGet is `get` routed through c.httpSearch — the long-timeout
// client. Only SearchReleases uses it; every other endpoint uses
// `get`. When WithSearchTimeout was not supplied, c.httpSearch
// aliases c.http (same behaviour as get).
func (c *Client) searchGet(ctx context.Context, endpoint string, query url.Values, out any) error {
	full := c.baseURL + endpoint
	if query != nil {
		full += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return fmt.Errorf("build request %s: %w", endpoint, err)
	}
	return c.doWithClient(ctx, c.httpSearch, req, endpoint, out)
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

func (c *Client) put(ctx context.Context, endpoint string, body any, out any) error {
	full := c.baseURL + endpoint
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode body %s: %w", endpoint, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, full, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("build request %s: %w", endpoint, err)
	}
	return c.do(ctx, req, endpoint, out)
}

func (c *Client) delete(ctx context.Context, endpoint string) error {
	full := c.baseURL + endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, full, nil)
	if err != nil {
		return fmt.Errorf("build request %s: %w", endpoint, err)
	}
	return c.do(ctx, req, endpoint, nil)
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

func (c *Client) ListSeriesCache(ctx context.Context, instanceName string) ([]series.CacheEntry, error) {
	var dtos []seriesDTO
	if err := c.get(ctx, "/api/v3/series", nil, &dtos); err != nil {
		return nil, err
	}
	out := make([]series.CacheEntry, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, seriesDTOToCacheEntry(d, instanceName))
	}
	return out, nil
}

func seriesDTOToCacheEntry(d seriesDTO, instanceName string) series.CacheEntry {
	entry := series.CacheEntry{
		InstanceName:   instanceName,
		SonarrSeriesID: d.ID,
		Title:          d.Title,
		TitleSlug:      d.TitleSlug,
		Monitored:      d.Monitored,
	}
	// MissingCount: aggregate of aired but not-on-disk (045a).
	if d.Statistics != nil {
		entry.MissingCount = series.Statistics{
			EpisodeCount:     d.Statistics.EpisodeCount,
			EpisodeFileCount: d.Statistics.EpisodeFileCount,
		}.AiredMissing()
	}
	// Pointer fields: nil unless Sonarr returned a non-zero value.
	if d.Year > 0 {
		v := d.Year
		entry.Year = &v
	}
	if d.TVDBID > 0 {
		v := d.TVDBID
		entry.TVDBID = &v
	}
	if d.IMDBID != "" {
		v := d.IMDBID
		entry.IMDBID = &v
	}
	if d.TMDBID > 0 {
		v := d.TMDBID
		entry.TMDBID = &v
	}
	if d.Status != "" {
		v := d.Status
		entry.Status = &v
	}
	if d.Network != "" {
		v := d.Network
		entry.Network = &v
	}
	if d.Runtime > 0 {
		v := d.Runtime
		entry.RuntimeMinutes = &v
	}
	if d.Overview != "" {
		v := d.Overview
		entry.Overview = &v
	}
	if len(d.Genres) > 0 {
		g := make([]string, len(d.Genres))
		copy(g, d.Genres)
		entry.Genres = g
	}
	if d.PreviousAiring != nil {
		v := *d.PreviousAiring
		entry.LastAiredAt = &v
	}
	// First image per cover type wins.
	for _, img := range d.Images {
		if img.URL == "" {
			continue
		}
		url := img.URL
		switch img.CoverType {
		case "poster":
			if entry.PosterPath == nil {
				entry.PosterPath = &url
			}
		case "fanart":
			if entry.FanartPath == nil {
				entry.FanartPath = &url
			}
		case "banner":
			if entry.BannerPath == nil {
				entry.BannerPath = &url
			}
		}
	}
	return entry
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

// ListEpisodeFilesBySeason returns the rich per-file metadata for one
// season. Two Sonarr round-trips:
//
//  1. GET /api/v3/episodeFile?seriesId=&seasonNumber=  → file rows
//  2. GET /api/v3/episode?seriesId=&seasonNumber=      → episode rows
//     (we read episode.episodeFileId → episodeNumber map)
//
// Grouping happens in Go so the API stays stable across Sonarr
// versions. Capped at 200 entries server-side; Sonarr's natural
// response is ≤ 1000 per season. 043c.
func (c *Client) ListEpisodeFilesBySeason(
	ctx context.Context, seriesID, seasonNumber int,
) ([]ports.EpisodeFileDetail, error) {
	q := url.Values{}
	q.Set("seriesId", strconv.Itoa(seriesID))
	q.Set("seasonNumber", strconv.Itoa(seasonNumber))
	var fileDTOs []episodeFileDTO
	if err := c.get(ctx, "/api/v3/episodeFile", q, &fileDTOs); err != nil {
		return nil, fmt.Errorf("sonarr episodeFile: %w", err)
	}
	var epDTOs []episodeDTO
	if err := c.get(ctx, "/api/v3/episode", q, &epDTOs); err != nil {
		return nil, fmt.Errorf("sonarr episode: %w", err)
	}

	// episodeFileID -> sorted []episodeNumber
	byFile := make(map[int][]int, len(fileDTOs))
	for _, e := range epDTOs {
		if e.EpisodeFileID == 0 {
			continue
		}
		byFile[e.EpisodeFileID] = append(byFile[e.EpisodeFileID], e.EpisodeNumber)
	}
	for k := range byFile {
		sort.Ints(byFile[k])
	}

	const cap = 200
	out := make([]ports.EpisodeFileDetail, 0, len(fileDTOs))
	for _, f := range fileDTOs {
		if len(out) >= cap {
			break
		}
		en := byFile[f.ID]
		if en == nil {
			en = []int{}
		}
		out = append(out, ports.EpisodeFileDetail{
			ID:             f.ID,
			RelativePath:   f.RelativePath,
			SeasonNumber:   f.SeasonNumber,
			EpisodeNumbers: en,
			SizeBytes:      f.Size,
			Quality:        f.Quality.Quality.Name,
		})
	}
	return out, nil
}

func (c *Client) SearchReleases(ctx context.Context, seriesID, seasonNumber int) ([]release.Release, error) {
	q := url.Values{}
	q.Set("seriesId", strconv.Itoa(seriesID))
	q.Set("seasonNumber", strconv.Itoa(seasonNumber))
	var dtos []releaseDTO
	// searchGet (vs get) routes through c.httpSearch — the
	// long-timeout client. Interactive indexer search (Prowlarr →
	// RuTracker / others) can take 30s+; the base client's timeout
	// would surface as `context deadline exceeded` here even though
	// every other endpoint completes in <1s.
	if err := c.searchGet(ctx, "/api/v3/release", q, &dtos); err != nil {
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

// ForceGrab calls POST /api/v3/release — the endpoint Sonarr UI uses
// for "Override and add to Download Queue". 2xx success, 4xx permanent
// failure, 5xx transient. On 2xx, ForceGrab best-effort extracts the
// `downloadClientId` integer from the response and base-10-formats it;
// nil/zero/absent → empty string (the steady-state per 008b).
// Decode-only errors after a 2xx are intentionally swallowed (DEBUG
// log + return "", nil) — the grab itself succeeded. Non-2xx and
// network errors propagate the existing StatusError wrap chain.
func (c *Client) ForceGrab(ctx context.Context, guid string, indexerID int) (string, error) {
	body := forceGrabRequest{GUID: guid, IndexerID: indexerID}
	var resp releaseCreateResponse
	if err := c.post(ctx, "/api/v3/release", body, &resp); err != nil {
		// Decode-only failures look like "decode /api/v3/release: ..."
		// and arrive AFTER a 2xx — suppress them. Every other shape
		// (non-2xx, network, rate-limit, ctx-cancel) wraps a sentinel
		// the classifier already understands; bubble those up verbatim.
		if isDecodeOnlyError(err) {
			c.logger.DebugContext(ctx, "force_grab_response_decode_skipped",
				slog.String("instance", c.name),
				slog.String("error", err.Error()),
			)
			return "", nil
		}
		return "", err
	}
	if resp.DownloadClientID == nil || *resp.DownloadClientID == 0 {
		return "", nil
	}
	return strconv.Itoa(*resp.DownloadClientID), nil
}

// ParseRelease calls Sonarr's /api/v3/parse endpoint and returns the
// trimmed ParseResult. Sonarr returns 200 with parsedEpisodeInfo:null
// for un-recognised titles — ParseRelease tolerates this and emits a
// zero-valued ParseResult{} without erroring. Non-2xx propagates via
// the existing StatusError wrap chain. Uses the default-timeout HTTP
// client (NOT httpSearch — /api/v3/parse is a fast string parse, not
// an indexer-search).
func (c *Client) ParseRelease(ctx context.Context, title string) (ports.ParseResult, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return ports.ParseResult{Languages: []string{}}, nil
	}
	q := url.Values{}
	q.Set("title", title)
	var dto parseResourceDTO
	if err := c.get(ctx, "/api/v3/parse", q, &dto); err != nil {
		return ports.ParseResult{}, err
	}
	internal := parseResultFromDTO(dto)
	return ports.ParseResult{
		Quality:      internal.Quality,
		Source:       internal.Source,
		Resolution:   internal.Resolution,
		Languages:    internal.Languages,
		ReleaseGroup: internal.ReleaseGroup,
	}, nil
}

// isDecodeOnlyError reports whether the error is the JSON-decode wrap
// emitted by Client.do after a successful (2xx) response. Compared by
// the fixed prefix "decode " injected at client.go's decode site —
// keeping the check string-stable across endpoints.
func isDecodeOnlyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.HasPrefix(err.Error(), "decode /api/v3/release:")
}

// toStatistics maps Sonarr's statisticsDTO into the domain Statistics
// value object. Nil-safe — Sonarr omits the block for empty series and
// callers pass through a nil pointer in that case.
//
// 046a forwards TotalEpisodeCount / AiredEpisodeCount alongside the
// pre-existing EpisodeCount / EpisodeFileCount fields. Fixtures that
// pre-date 046a only set EpisodeCount → Total / Aired default to zero;
// downstream callers tolerate that path via Statistics.AiredMissing and
// SeasonStatsFromStatistics fallback paths.
func toStatistics(p *statisticsDTO) series.Statistics {
	if p == nil {
		return series.Statistics{}
	}
	return series.Statistics{
		EpisodeCount:     p.EpisodeCount,
		EpisodeFileCount: p.EpisodeFileCount,
		Total:            p.TotalEpisodeCount,
		Aired:            p.AiredEpisodeCount,
	}
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
		seasons = append(seasons, series.Season{
			Number: s.SeasonNumber, Monitored: s.Monitored,
			Statistics: toStatistics(s.Statistics),
		})
	}
	return series.Series{
		ID: d.ID, Title: d.Title, Type: st,
		Monitored: d.Monitored, TagIDs: d.Tags,
		QualityProfile: d.QualityProfile, Seasons: seasons,
		Statistics: toStatistics(d.Statistics),
	}
}
