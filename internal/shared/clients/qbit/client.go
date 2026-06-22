package qbit

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync/atomic"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/http/httpx"
)

// Config is the constructor input for a Client. Username + Password may
// both be empty — local qBit instances without auth are a supported
// deployment (parent invariant D61). Timeout defaults to 30s when zero.
type Config struct {
	URL      string
	Username string
	Password string
	Category string
	Timeout  time.Duration
	// Instance is the Sonarr instance name this client serves. Used
	// to label session telemetry (story 479b). Empty string disables
	// labelling — the gauge will publish under instance="" which the
	// operator can ignore on the dashboard.
	Instance domain.InstanceName
}

// Story 479b — reauth reason classifier. Closed set; kept in parity
// with internal/observability/qbit_session.go QbitReauthReason*
// constants. Duplicated locally so the qbit client does not import
// the observability package (which is shared infra and pulls a wider
// dependency graph than a low-level client should).
const (
	reauthReasonCookieExpired = "cookie_expired"
	reauthReasonNetworkError  = "network_error"
	reauthReasonUnauthorized  = "unauthorized"
	reauthReasonUnknown       = "unknown"
)

// SessionMetrics is the qBit client's session telemetry port. nil-OK
// via the nullSessionMetrics default installed by NewClient.
type SessionMetrics interface {
	// IncReauth bumps the re-login counter. Called from Login() on
	// every successful re-login (skip the first ever).
	IncReauth(instance domain.InstanceName, reason string)
}

type nullSessionMetrics struct{}

func (nullSessionMetrics) IncReauth(domain.InstanceName, string) {}

// Torrent is the lean per-torrent shape the Watchdog cares about. Sourced
// from qbt.Torrent — only fields the use case reads are exposed here.
//
// Tags is the raw comma-separated qBit tag string ("issue, foo, bar"); the
// watchdog rollup handler scans it for qbit_manage-style markers
// ("issue" / "unregistered" / "tracker_error") to compute the on-demand
// unregistered counter without a per-torrent tracker probe (Story 094).
type Torrent struct {
	Hash     string
	Name     string
	Category string
	State    string
	Tags     string
	AddedOn  time.Time
}

// Tracker mirrors the qBit /api/v2/torrents/trackers shape. Status uses
// the qBit numeric encoding (0=Disabled, 1=NotContacted, 2=Working,
// 3=Updating, 4=NotWorking).
type Tracker struct {
	URL    string
	Status int
	Msg    string
}

// Client is the surface the Watchdog uses against a qBit instance.
type Client interface {
	Login(ctx context.Context) error
	ListTorrents(ctx context.Context) ([]Torrent, error)
	GetTrackers(ctx context.Context, hash string) ([]Tracker, error)
	Ping(ctx context.Context) error
	NewSyncSession(ctx context.Context) (SyncSession, error)
	Close() error
}

type client struct {
	cfg    Config
	inner  *qbt.Client
	anon   bool
	closed bool

	// Story 479b — session telemetry. loginAt is unix-seconds of the
	// most recent successful Login. loginCount is the total number of
	// successful Login calls; second+ logins increment the reauth
	// counter. lastLoginErrKind classifies the most recent failed
	// Login attempt so the next successful Login knows the reauth
	// reason ("cookie_expired" by default; "network_error" /
	// "unauthorized" when the prior attempt failed).
	loginAt          atomic.Int64
	loginCount       atomic.Int32
	lastLoginErrKind atomic.Value // string; one of reauthReason* consts

	// metrics is the session telemetry sink. nullSessionMetrics by
	// default; production wiring may replace it via SetSessionMetrics.
	metrics SessionMetrics

	// instance is the Sonarr instance name this client serves.
	// Populated by NewClient via cfg.Instance.
	instance domain.InstanceName
}

// SetSessionMetrics installs the session telemetry sink. Concurrent
// callers MUST NOT call this after the client has been handed off to
// a goroutine that calls Login — the field is a plain interface, not
// atomic. Production wiring sets it once at construction. Test code
// uses package-internal access (same package).
func (c *client) SetSessionMetrics(m SessionMetrics) {
	if m == nil {
		m = nullSessionMetrics{}
	}
	c.metrics = m
}

// LoginAge returns the wall-clock duration since the last successful
// Login. Returns 0 when no successful Login has yet happened. Safe
// for concurrent use. Exposed at the package level so SyncSession can
// surface it to the torrentsync use case without going through the
// public Client interface.
func (c *client) LoginAge(now time.Time) time.Duration {
	at := c.loginAt.Load()
	if at == 0 {
		return 0
	}
	return now.Sub(time.Unix(at, 0))
}

const defaultTimeout = 30 * time.Second

// NewClient validates cfg and constructs a Client. Returns
// ErrInvalidConfig wrapping the parse failure when cfg.URL is empty or
// uses an unsupported scheme. The upstream qbt.NewClient never returns
// an error — validation lives entirely on this side.
func NewClient(cfg Config) (Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("%w: url is empty", ErrInvalidConfig)
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: scheme %q not supported", ErrInvalidConfig, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%w: host is empty", ErrInvalidConfig)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	inner := qbt.NewClient(qbt.Config{
		Host:     cfg.URL,
		Username: cfg.Username,
		Password: cfg.Password,
		Timeout:  int(timeout / time.Second),
	})

	// Story 478 (B-31) — wrap the library-allocated http.Client.Transport
	// with httpx.MetricsTransport so every qBit Web API call surfaces in
	// seasonfill_external_http_request_* under client="qbit". The library
	// configures its own *http.Transport at construction (HTTP/2 prefer,
	// proxy-aware, cookie jar, 100/10 idle conns, TLSSkipVerify wired
	// from cfg) — we wrap that transport in place rather than replacing
	// the whole *http.Client via WithHTTPClient, so the library's tuned
	// dialer + idle pool stays in the chain underneath the metrics
	// layer. Mirrors the tmdb_cdn pattern in
	// internal/shared/clients/tmdb/client.go:266-268.
	//
	// Safety: NewClient is a pure constructor (no goroutines, no
	// network I/O), so mutating the transport here is race-free — no
	// other goroutine has a handle to the client yet. httpx.NewMetrics
	// Transport falls back to http.DefaultTransport when its inner is
	// nil, so a hypothetical future library change shipping a nil
	// transport is absorbed without panic.
	if httpClient := inner.GetHTTPClient(); httpClient != nil {
		httpClient.Transport = httpx.NewMetricsTransport(
			"qbit",
			httpx.QbitEndpointFor,
			httpClient.Transport,
		)
	}

	c := &client{
		cfg:      cfg,
		inner:    inner,
		anon:     cfg.Username == "" && cfg.Password == "",
		metrics:  nullSessionMetrics{},
		instance: cfg.Instance,
	}
	// Empty-string sentinel — first successful Login does NOT
	// increment the reauth counter (it's the "initial" Login).
	c.lastLoginErrKind.Store("")
	return c, nil
}

// Login establishes a session cookie with qBit. No-op when both
// Username and Password are empty (D61 local-qBit-no-auth).
//
// Error mapping:
//   - qbt.ErrBadCredentials / qbt.ErrIPBanned →
//     errors.Join(err, sharedErrors.ErrInstanceUnauthorized)
//   - any other transport / wrap error →
//     errors.Join(err, sharedErrors.ErrInstanceNetwork)
func (c *client) Login(ctx context.Context) error {
	if c.closed {
		return errors.New("qbit client closed")
	}
	if c.anon {
		return nil
	}
	if err := c.inner.LoginCtx(ctx); err != nil {
		// Story 479b — classify the failure so the next successful
		// Login can publish the right reauth reason. Order matters:
		// auth-shaped errors win over the catch-all network bucket.
		kind := reauthReasonUnknown
		switch {
		case errors.Is(err, qbt.ErrBadCredentials) || errors.Is(err, qbt.ErrIPBanned):
			kind = reauthReasonUnauthorized
		case ctx.Err() == nil:
			// Anything not an explicit context cancellation we treat
			// as a network failure for reauth-reason purposes.
			kind = reauthReasonNetworkError
		}
		c.lastLoginErrKind.Store(kind)

		if errors.Is(err, qbt.ErrBadCredentials) || errors.Is(err, qbt.ErrIPBanned) {
			return fmt.Errorf("qbit login: %w", errors.Join(err, sharedErrors.ErrInstanceUnauthorized))
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("qbit login: %w", ctxErr)
		}
		return fmt.Errorf("qbit login: %w", errors.Join(err, sharedErrors.ErrInstanceNetwork))
	}

	// Story 479b — record successful login timestamp + emit reauth on
	// every Login AFTER the first.
	c.loginAt.Store(time.Now().Unix())
	if c.loginCount.Add(1) > 1 {
		reason := reauthReasonCookieExpired
		if raw := c.lastLoginErrKind.Load(); raw != nil {
			if s, ok := raw.(string); ok && s != "" {
				reason = s
			}
		}
		c.metrics.IncReauth(c.instance, reason)
	}
	c.lastLoginErrKind.Store("")
	return nil
}

// ListTorrents fetches the torrent list, applying cfg.Category as a
// server-side filter. Empty cfg.Category returns every torrent the
// authenticated session can see.
func (c *client) ListTorrents(ctx context.Context) ([]Torrent, error) {
	if c.closed {
		return nil, errors.New("qbit client closed")
	}
	opts := qbt.TorrentFilterOptions{}
	if c.cfg.Category != "" {
		opts.Category = c.cfg.Category
	}
	raw, err := c.inner.GetTorrentsCtx(ctx, opts)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("qbit list torrents: %w", ctxErr)
		}
		return nil, fmt.Errorf("qbit list torrents: %w", errors.Join(err, sharedErrors.ErrInstanceNetwork))
	}
	out := make([]Torrent, 0, len(raw))
	for _, t := range raw {
		out = append(out, Torrent{
			Hash:     t.Hash,
			Name:     t.Name,
			Category: t.Category,
			State:    string(t.State),
			Tags:     t.Tags,
			AddedOn:  time.Unix(t.AddedOn, 0).UTC(),
		})
	}
	return out, nil
}

// GetTrackers returns the tracker list for hash. qBit's 404 (torrent
// gone) is surfaced as ErrTorrentNotFound; the upstream library
// normalises 404 to a (nil, nil) tuple — the wrapper distinguishes
// "torrent gone" from "torrent has zero trackers" by checking for nil.
func (c *client) GetTrackers(ctx context.Context, hash string) ([]Tracker, error) {
	if c.closed {
		return nil, errors.New("qbit client closed")
	}
	if hash == "" {
		return nil, fmt.Errorf("%w: empty hash", ErrTorrentNotFound)
	}
	raw, err := c.inner.GetTorrentTrackersCtx(ctx, hash)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("qbit get trackers: %w", ctxErr)
		}
		return nil, fmt.Errorf("qbit get trackers: %w", errors.Join(err, sharedErrors.ErrInstanceNetwork))
	}
	if raw == nil {
		return nil, fmt.Errorf("qbit get trackers %q: %w", hash, ErrTorrentNotFound)
	}
	out := make([]Tracker, 0, len(raw))
	for _, t := range raw {
		out = append(out, Tracker{
			URL:    t.Url,
			Status: int(t.Status),
			Msg:    t.Message,
		})
	}
	return out, nil
}

// Ping performs a fast reachability check against qBit. It logs in
// (no-op for anonymous deployments per D61) and then calls
// /api/v2/app/version. Returns nil on success; any error indicates
// qBit is unreachable, unauthenticated, or otherwise unhealthy.
//
// Used by the watchdog rollup handler (Story 090) to fill the
// QbitReachable bit before the per-instance polling loop has had its
// first cycle. The caller is expected to bound this call with a short
// ctx deadline (3s in the rollup handler).
func (c *client) Ping(ctx context.Context) error {
	if c.closed {
		return errors.New("qbit client closed")
	}
	if err := c.Login(ctx); err != nil {
		return fmt.Errorf("qbit ping: %w", err)
	}
	if _, err := c.inner.GetAppVersionCtx(ctx); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("qbit ping: %w", ctxErr)
		}
		return fmt.Errorf("qbit ping: %w", errors.Join(err, sharedErrors.ErrInstanceNetwork))
	}
	return nil
}

// NewSyncSession returns a per-instance qBit /sync/maindata cursor.
// The session is NOT safe for concurrent use — the torrentsync loop
// (story 220) owns exactly one session per qBit instance and calls
// Refresh from a single goroutine.
//
// Login is performed eagerly (no-op for anonymous deployments,
// D61); the first Refresh will reuse the established session
// cookie. Subsequent re-logins on 403 are handled by autobrr's
// internal retry — we do not need to thread them.
func (c *client) NewSyncSession(ctx context.Context) (SyncSession, error) {
	return newSyncSession(ctx, c)
}

// Close marks the client as closed. The upstream qbt.Client uses
// http.DefaultTransport — there is no connection pool to drain. The
// boolean flag exists so callers can detect post-Close calls without
// the upstream lib growing a Close method.
func (c *client) Close() error {
	c.closed = true
	return nil
}
