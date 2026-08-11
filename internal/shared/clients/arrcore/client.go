package arrcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/internal/admin/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/internal/observability"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// Transport is the shared arr HTTP surface: the request primitives plus the v3
// endpoints whose wire shape is byte-identical between Sonarr and Radarr
// (/system/status, /qualityprofile, /rootfolder, /tag). *Client satisfies it.
//
// EXTENSION SEAM (Ф6-R-3): the Radarr client embeds *Client to inherit this
// surface unchanged and only adds Radarr-domain endpoints (movies, etc.) on
// top. Do NOT add Sonarr- or Radarr-specific endpoints to this interface —
// only methods proven identical across both arrs belong here.
type Transport interface {
	Do(ctx context.Context, req *http.Request, endpoint string, out any) error
	Get(ctx context.Context, endpoint string, query url.Values, out any) error
	SearchGet(ctx context.Context, endpoint string, query url.Values, out any) error
	Post(ctx context.Context, endpoint string, body, out any) error
	Put(ctx context.Context, endpoint string, body, out any) error
	Delete(ctx context.Context, endpoint string) error

	SystemStatus(ctx context.Context) (ports.SystemStatus, error)
	GetQualityProfile(ctx context.Context, id int) (ports.QualityProfile, error)
	ListQualityProfiles(ctx context.Context) ([]ports.QualityProfile, error)
	ListRootFolders(ctx context.Context) ([]ports.RootFolder, error)
	CreateTag(ctx context.Context, label string) (ports.Tag, error)
	Name() string
}

var _ Transport = (*Client)(nil)

// Client is the shared arr transport. It owns the HTTP plumbing, rate limiting,
// and the endpoints identical across Sonarr/Radarr. Sonarr (and, in R-3,
// Radarr) embed *Client to promote the exported methods.
type Client struct {
	name    shareddomain.InstanceName
	baseURL string
	apiKey  string
	// http is the default client (every endpoint EXCEPT the search-GET path).
	http *http.Client
	// httpSearch is the long-timeout client used only by SearchGet. When
	// WithSearchTimeout is not supplied (or is zero), httpSearch aliases http.
	httpSearch *http.Client
	limiter    *ratelimit.Limiter
	// global is set by WithGlobalLimiter (frozen at construction). globalPtr is
	// set by WithGlobalLimiterPointer (live-reloaded). Mutually exclusive — the
	// pointer wins if both are supplied (last write wins in option order).
	global    *ratelimit.Limiter
	globalPtr *atomic.Pointer[ratelimit.Limiter]
}

// Option configures a Client at construction.
type Option func(*Client)

// WithGlobalLimiter sets the shared global limiter for cross-instance
// protection. Pass nil for unlimited.
func WithGlobalLimiter(l *ratelimit.Limiter) Option {
	return func(c *Client) { c.global = l }
}

// WithGlobalLimiterPointer captures an atomic pointer to the live global
// limiter. The client reads the pointer on every API call so reload-time swaps
// take effect immediately. nil-safe: a nil load means "no global rate limit on
// this call".
func WithGlobalLimiterPointer(p *atomic.Pointer[ratelimit.Limiter]) Option {
	return func(c *Client) {
		if p != nil {
			c.globalPtr = p
			c.global = nil
		}
	}
}

// WithSearchTimeout installs a separate http.Client used only by SearchGet.
// Pass 0 (or negative) to keep the base-timeout client for search too.
func WithSearchTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d <= 0 {
			return
		}
		c.httpSearch = &http.Client{Timeout: d}
	}
}

// New constructs a Client and applies functional options. Default httpSearch
// aliases http (same timeout as every other endpoint); WithSearchTimeout, if
// applied, overrides httpSearch with a longer-timeout client.
func New(name shareddomain.InstanceName, baseURL, apiKey string, timeout time.Duration, limiter *ratelimit.Limiter, opts ...Option) *Client {
	base := &http.Client{Timeout: timeout}
	c := &Client{
		name:       name,
		baseURL:    baseURL,
		apiKey:     apiKey,
		http:       base,
		httpSearch: base, // default alias — overridden by WithSearchTimeout
		limiter:    limiter,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) Name() string { return string(c.name) }

// globalLimiter returns the current global limiter (or nil for unlimited).
// Callers must nil-check before invoking Wait.
func (c *Client) globalLimiter() *ratelimit.Limiter {
	if c.globalPtr != nil {
		return c.globalPtr.Load()
	}
	return c.global
}

// Do issues req through the default (non-search) http client.
func (c *Client) Do(ctx context.Context, req *http.Request, endpoint string, out any) error {
	return c.doWithClient(ctx, c.http, req, endpoint, out)
}

// doWithClient is the workhorse that lets callers pick which http.Client (and
// therefore which timeout) to use. SearchGet supplies c.httpSearch; everything
// else funnels through c.http via Do.
func (c *Client) doWithClient(ctx context.Context, hc *http.Client, req *http.Request, endpoint string, out any) error {
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if req.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Per-instance limiter first, then global. Both nil-safe and honor ctx.
	// When the wait queue outruns ctx, surface as ErrInstanceSelfThrottled —
	// distinct from "instance is down" — so the healthcheck can transition the
	// instance to SelfThrottled instead of UnavailableUnknown.
	if err := ratelimit.Wait(c.limiter, ctx); err != nil {
		if errors.Is(err, ratelimit.ErrSelfThrottled) {
			return fmt.Errorf("rate limit wait %s: %w", endpoint, errors.Join(err, sharedErrors.ErrInstanceSelfThrottled))
		}
		return fmt.Errorf("rate limit wait %s: %w", endpoint, err)
	}
	if err := ratelimit.Wait(c.globalLimiter(), ctx); err != nil {
		if errors.Is(err, ratelimit.ErrSelfThrottled) {
			return fmt.Errorf("global rate limit wait %s: %w", endpoint, errors.Join(err, sharedErrors.ErrInstanceSelfThrottled))
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
		return fmt.Errorf("call %s: %w", endpoint, errors.Join(err, sharedErrors.ErrInstanceNetwork))
	}
	defer func() { _ = resp.Body.Close() }()

	observability.ObserveSonarrAPIDuration(c.name, endpoint, dur)
	observability.SonarrAPIRequest(c.name, endpoint, strconv.Itoa(resp.StatusCode))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, BodyMaxBytes))
		se := &StatusError{Endpoint: endpoint, Status: resp.StatusCode, Body: string(body)}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return fmt.Errorf("%w: %w", sharedErrors.ErrInstanceUnauthorized, se)
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

// Get issues GET endpoint?query through the default http client.
func (c *Client) Get(ctx context.Context, endpoint string, query url.Values, out any) error {
	full := c.baseURL + endpoint
	if query != nil {
		full += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return fmt.Errorf("build request %s: %w", endpoint, err)
	}
	return c.Do(ctx, req, endpoint, out)
}

// SearchGet is Get routed through c.httpSearch — the long-timeout client. Only
// interactive indexer search uses it; every other endpoint uses Get. When
// WithSearchTimeout was not supplied, c.httpSearch aliases c.http.
func (c *Client) SearchGet(ctx context.Context, endpoint string, query url.Values, out any) error {
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

func (c *Client) Post(ctx context.Context, endpoint string, body, out any) error {
	full := c.baseURL + endpoint
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode body %s: %w", endpoint, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, full, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("build request %s: %w", endpoint, err)
	}
	return c.Do(ctx, req, endpoint, out)
}

func (c *Client) Put(ctx context.Context, endpoint string, body, out any) error {
	full := c.baseURL + endpoint
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode body %s: %w", endpoint, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, full, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("build request %s: %w", endpoint, err)
	}
	return c.Do(ctx, req, endpoint, out)
}

func (c *Client) Delete(ctx context.Context, endpoint string) error {
	full := c.baseURL + endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, full, nil)
	if err != nil {
		return fmt.Errorf("build request %s: %w", endpoint, err)
	}
	return c.Do(ctx, req, endpoint, nil)
}

// --- Shared v3 endpoints (identical Sonarr/Radarr) ---

func (c *Client) SystemStatus(ctx context.Context) (ports.SystemStatus, error) {
	var dto systemStatusDTO
	if err := c.Get(ctx, "/api/v3/system/status", nil, &dto); err != nil {
		return ports.SystemStatus{}, err
	}
	return ports.SystemStatus{Version: dto.Version, InstanceURL: dto.InstanceURL}, nil
}

func (c *Client) GetQualityProfile(ctx context.Context, id int) (ports.QualityProfile, error) {
	var dto qualityProfileDTO
	if err := c.Get(ctx, "/api/v3/qualityprofile/"+strconv.Itoa(id), nil, &dto); err != nil {
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

// ListQualityProfiles calls GET /api/v3/qualityprofile and returns the full
// list. Unlike GetQualityProfile(id), the per-item allowance loop is skipped —
// the N-4 modal picker only needs id+name. Callers that need the rich Items
// slice must fall back to GetQualityProfile(id).
func (c *Client) ListQualityProfiles(ctx context.Context) ([]ports.QualityProfile, error) {
	var dtos []qualityProfileDTO
	if err := c.Get(ctx, "/api/v3/qualityprofile", nil, &dtos); err != nil {
		return nil, err
	}
	out := make([]ports.QualityProfile, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, ports.QualityProfile{ID: d.ID, Name: d.Name})
	}
	return out, nil
}

// ListRootFolders calls GET /api/v3/rootfolder. The arr returns every
// configured root in one round-trip; no filtering — the caller picks based on
// Accessible.
func (c *Client) ListRootFolders(ctx context.Context) ([]ports.RootFolder, error) {
	var dtos []rootFolderDTO
	if err := c.Get(ctx, "/api/v3/rootfolder", nil, &dtos); err != nil {
		return nil, err
	}
	out := make([]ports.RootFolder, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, ports.RootFolder{
			ID:         d.ID,
			Path:       d.Path,
			Accessible: d.Accessible,
			FreeSpace:  d.FreeSpace,
		})
	}
	return out, nil
}

// CreateTag posts {label} to /api/v3/tag and returns the created (or
// pre-existing — the arr deduplicates by label) row. Idempotent at the arr
// layer, so the resolver does not race on concurrent identical labels.
func (c *Client) CreateTag(ctx context.Context, label string) (ports.Tag, error) {
	body := createTagRequest{Label: label}
	var dto tagDTO
	if err := c.Post(ctx, "/api/v3/tag", body, &dto); err != nil {
		return ports.Tag{}, err
	}
	return ports.Tag{ID: dto.ID, Label: dto.Label}, nil
}
