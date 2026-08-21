package qbit

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// connKey identifies a qBittorrent *connection*: everything that decides
// which authenticated session a request runs under.
//
// Category is deliberately absent. It is a per-caller server-side list
// filter (torrents/info?category=), not a connection property — two
// arr-instances pointing at the same qBittorrent with different
// categories share one session and one login (B1.6).
//
// A struct key rather than a "url|user|pass" string: a password
// containing the separator would otherwise collide two distinct
// credential sets onto one entry.
type connKey struct {
	url      string
	username string
	password string
}

func keyFor(cfg Config) connKey {
	return connKey{url: cfg.URL, username: cfg.Username, password: cfg.Password}
}

// maxCachedConns bounds the map. Entries are keyed by credential set, so
// growth only happens when the operator rotates qBittorrent credentials
// (a homelab runs one or two distinct connections). The cap is a memory
// backstop, not a working-set limit: on overflow the least-recently
// handed-out entry is dropped and its idle sockets released, and a later
// Get for that key simply rebuilds the connection.
const maxCachedConns = 16

// ClientCache hands out connection-deduplicated Clients. One entry per
// connKey owns one authenticated session; every caller gets a lightweight
// view over it carrying its own Category.
//
// Safe for concurrent use: the regrab poll loops, the torrentsync session
// factory, the torrent-action HTTP path and the watchdog rollup handler
// all call Get from different goroutines.
type ClientCache struct {
	mu      sync.Mutex
	entries map[connKey]*cacheEntry
	now     func() time.Time
}

type cacheEntry struct {
	conn *sharedConn
	// lastUsed is the wall clock of the most recent Get. Guarded by
	// ClientCache.mu — never read or written off the cache path.
	lastUsed time.Time
}

// NewClientCache returns an empty cache. Exactly one instance is
// constructed per process (wiring/watchdog.go) and shared by the client
// factory, the probe and the torrents lister.
func NewClientCache() *ClientCache {
	return &ClientCache{entries: make(map[connKey]*cacheEntry), now: time.Now}
}

// Get returns a Client for cfg's connection, constructing and caching the
// underlying session on first use for that connKey. Config errors from
// NewClient (empty/invalid URL) bubble unchanged.
//
// The returned Client is a view, not an owner: its Close is a release
// (see cachedClient.Close). Callers keep their defer Close() as-is.
func (cc *ClientCache) Get(cfg Config) (Client, error) {
	key := keyFor(cfg)

	cc.mu.Lock()
	defer cc.mu.Unlock()

	entry, ok := cc.entries[key]
	if !ok {
		base, err := newClient(connConfig(cfg))
		if err != nil {
			return nil, err
		}
		entry = &cacheEntry{conn: &sharedConn{base: base}}
		cc.entries[key] = entry
	}
	// Stamped before evictLocked so a freshly built entry is never its
	// own victim.
	entry.lastUsed = cc.now()
	cc.evictLocked()

	return &cachedClient{conn: entry.conn, category: cfg.Category}, nil
}

// Len reports the number of cached connections. Diagnostics and tests.
func (cc *ClientCache) Len() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return len(cc.entries)
}

// evictLocked drops the least-recently handed-out entry once the map
// exceeds maxCachedConns. Caller holds cc.mu.
func (cc *ClientCache) evictLocked() {
	if len(cc.entries) <= maxCachedConns {
		return
	}
	var (
		victimKey connKey
		oldest    time.Time
		found     bool
	)
	for k, e := range cc.entries {
		if !found || e.lastUsed.Before(oldest) {
			victimKey, oldest, found = k, e.lastUsed, true
		}
	}
	if !found {
		return
	}
	victim := cc.entries[victimKey]
	delete(cc.entries, victimKey)
	victim.conn.releaseIdle()
}

// connConfig strips the per-caller Category: the shared base client must
// not carry one caller's server-side filter. Every handed-out view
// supplies its own category to listTorrents.
//
// Timeout and Instance come from whichever caller opened the connection.
// Both are left zero by every production caller (see
// infrastructure/regrab.configFor), so no divergence is reachable today.
func connConfig(cfg Config) Config {
	cfg.Category = ""
	return cfg
}

// sharedConn owns one authenticated qBittorrent session. Every
// cachedClient handed out for the same connKey points at the same
// sharedConn, so Login happens once per connection instead of once per
// instance or once per call.
type sharedConn struct {
	base *client

	mu       sync.Mutex
	loggedIn bool
}

// login performs the real Login at most once per connection. Concurrent
// callers serialise on mu so exactly one HTTP login is ever in flight; a
// failed attempt leaves the connection unauthenticated, so the next
// caller retries.
//
// A one-shot login cannot strand an expired session: autobrr/go-qbittorrent
// re-authenticates by itself when the cookie jar is empty and on any 403
// response.
func (s *sharedConn) login(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loggedIn {
		return nil
	}
	if err := s.base.Login(ctx); err != nil {
		return err
	}
	s.loggedIn = true
	return nil
}

// releaseIdle drops the connection's idle TCP sockets on eviction. It
// deliberately does NOT call base.Close(): Close only flips the client's
// unsynchronised `closed` flag, which would both poison and data-race
// with any straggler still holding a view. http.Client.CloseIdleConnections
// is safe for concurrent use and covers the only real resource this
// client owns.
func (s *sharedConn) releaseIdle() {
	if hc := s.base.inner.GetHTTPClient(); hc != nil {
		hc.CloseIdleConnections()
	}
}

// cachedClient is the per-caller view of a shared connection: the
// caller's Category plus a pointer to the session. Everything else
// forwards to the shared base client.
//
// Close is a release, not a teardown — the session belongs to the cache.
// Callers keep their `defer client.Close()` unchanged; it now returns nil
// without touching the session, which is what lets one connection outlive
// a regrab cycle and serve the next instance's poll.
type cachedClient struct {
	conn     *sharedConn
	category string
}

var _ Client = (*cachedClient)(nil)

func (c *cachedClient) Login(ctx context.Context) error { return c.conn.login(ctx) }

func (c *cachedClient) ListTorrents(ctx context.Context) ([]Torrent, error) {
	return c.conn.base.listTorrents(ctx, c.category)
}

func (c *cachedClient) GetTrackers(ctx context.Context, hash string) ([]Tracker, error) {
	return c.conn.base.GetTrackers(ctx, hash)
}

func (c *cachedClient) Pause(ctx context.Context, hash string) error {
	return c.conn.base.Pause(ctx, hash)
}

func (c *cachedClient) Resume(ctx context.Context, hash string) error {
	return c.conn.base.Resume(ctx, hash)
}

func (c *cachedClient) Recheck(ctx context.Context, hash string) error {
	return c.conn.base.Recheck(ctx, hash)
}

func (c *cachedClient) Ping(ctx context.Context) error {
	if err := c.conn.login(ctx); err != nil {
		return fmt.Errorf("qbit ping: %w", err)
	}
	return c.conn.base.appVersion(ctx)
}

func (c *cachedClient) NewSyncSession(ctx context.Context) (SyncSession, error) {
	if err := c.conn.login(ctx); err != nil {
		return nil, fmt.Errorf("qbit sync session: %w", err)
	}
	return newSyncSessionFor(c.conn.base), nil
}

// Close releases this view. The shared session stays open for the other
// holders of the same connection — see the type godoc.
func (c *cachedClient) Close() error { return nil }
