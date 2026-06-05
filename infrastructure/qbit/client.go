package qbit

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/alexmorbo/seasonfill/domain"
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
}

// Torrent is the lean per-torrent shape the Watchdog cares about. Sourced
// from qbt.Torrent — only fields the use case reads are exposed here.
type Torrent struct {
	Hash     string
	Name     string
	Category string
	State    string
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
	Close() error
}

type client struct {
	cfg    Config
	inner  *qbt.Client
	anon   bool
	closed bool
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

	return &client{
		cfg:   cfg,
		inner: inner,
		anon:  cfg.Username == "" && cfg.Password == "",
	}, nil
}

// Login establishes a session cookie with qBit. No-op when both
// Username and Password are empty (D61 local-qBit-no-auth).
//
// Error mapping:
//   - qbt.ErrBadCredentials / qbt.ErrIPBanned →
//     errors.Join(err, domain.ErrInstanceUnauthorized)
//   - any other transport / wrap error →
//     errors.Join(err, domain.ErrInstanceNetwork)
func (c *client) Login(ctx context.Context) error {
	if c.closed {
		return errors.New("qbit client closed")
	}
	if c.anon {
		return nil
	}
	if err := c.inner.LoginCtx(ctx); err != nil {
		if errors.Is(err, qbt.ErrBadCredentials) || errors.Is(err, qbt.ErrIPBanned) {
			return fmt.Errorf("qbit login: %w", errors.Join(err, domain.ErrInstanceUnauthorized))
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("qbit login: %w", ctxErr)
		}
		return fmt.Errorf("qbit login: %w", errors.Join(err, domain.ErrInstanceNetwork))
	}
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
		return nil, fmt.Errorf("qbit list torrents: %w", errors.Join(err, domain.ErrInstanceNetwork))
	}
	out := make([]Torrent, 0, len(raw))
	for _, t := range raw {
		out = append(out, Torrent{
			Hash:     t.Hash,
			Name:     t.Name,
			Category: t.Category,
			State:    string(t.State),
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
		return nil, fmt.Errorf("qbit get trackers: %w", errors.Join(err, domain.ErrInstanceNetwork))
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

// Close marks the client as closed. The upstream qbt.Client uses
// http.DefaultTransport — there is no connection pool to drain. The
// boolean flag exists so callers can detect post-Close calls without
// the upstream lib growing a Close method.
func (c *client) Close() error {
	c.closed = true
	return nil
}
