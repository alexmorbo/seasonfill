// Package regrab is the cmd/server adapter package that satisfies the
// application/regrab.QbitClientFactory and DetectorFactory boundaries
// with concrete infrastructure/qbit implementations. Keeping these in a
// thin adapter package avoids a circular import between cmd/server and
// application/regrab (the use case has the interface; the cmd/server
// wiring would otherwise need to define the impl in main.go which
// crowds main.go further).
package regrab

import (
	"context"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	appregrab "github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
)

// configFor projects the use case's Settings into a qbit.Config.
//
// Settings.PasswordPlaintext is the already-decrypted password (the use
// case's Lookup step ran the cipher) — the adapters never see ciphertext.
// Timeout is left zero → qbit.NewClient applies its 30s default.
func configFor(s appregrab.Settings) qbit.Config {
	return qbit.Config{
		URL:      s.URL,
		Username: s.Username,
		Password: s.PasswordPlaintext,
		Category: s.Category,
	}
}

// clientFor is the single funnel every adapter in this file goes through.
// With a cache it returns a view over the shared per-connection session;
// without one (zero-value adapter, tests) it falls back to the pre-B1.6
// transient client so behaviour is unchanged.
func clientFor(cache *qbit.ClientCache, s appregrab.Settings) (qbit.Client, error) {
	if cache == nil {
		return qbit.NewClient(configFor(s))
	}
	return cache.Get(configFor(s))
}

// QbitClientFactoryFunc satisfies application/regrab.QbitClientFactory by
// mapping Settings → qbit.Config → a cached client.
//
// Instances that share a qBittorrent (same url+username+password, any
// category) get views over ONE session and ONE login (B1.6). The returned
// client's Close is a release, not a teardown — callers keep calling it.
//
// The zero value carries no cache and builds a transient client per call.
// Production wiring MUST use NewQbitClientFactory with the process-wide
// cache from wiring.BuildRegrab.
type QbitClientFactoryFunc struct {
	cache *qbit.ClientCache
}

// NewQbitClientFactory wires the adapter to the shared connection cache.
func NewQbitClientFactory(cache *qbit.ClientCache) QbitClientFactoryFunc {
	return QbitClientFactoryFunc{cache: cache}
}

// NewClient implements application/regrab.QbitClientFactory.
func (f QbitClientFactoryFunc) NewClient(s appregrab.Settings) (qbit.Client, error) {
	return clientFor(f.cache, s)
}

// QbitProbeFunc satisfies handlers.QbitProbe. Story 090 introduced this
// so the watchdog rollup handler can fill QbitReachable before the
// per-instance polling loop has run for the first time after a pod
// restart. Since B1.6 it shares the connection cache with the client
// factory and the torrents lister instead of opening a fresh session per
// probe.
type QbitProbeFunc struct {
	cache *qbit.ClientCache
}

// NewQbitProbe wires the adapter to the shared connection cache.
func NewQbitProbe(cache *qbit.ClientCache) QbitProbeFunc {
	return QbitProbeFunc{cache: cache}
}

// Probe implements handlers.QbitProbe. Returns true when qBit responded
// to /api/v2/app/version within the supplied ctx deadline. Any other
// outcome (timeout, network error, unauthenticated) returns false; the
// error is surfaced for caller-side debug logging only.
func (f QbitProbeFunc) Probe(ctx context.Context, s appregrab.Settings) (bool, error) {
	client, err := clientFor(f.cache, s)
	if err != nil {
		return false, err
	}
	defer func() { _ = client.Close() }()
	if err := client.Ping(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// QbitTorrentsListerFunc satisfies handlers.QbitTorrentsLister. Story 094
// added this so the watchdog rollup handler can compute the watched and
// unregistered counters on demand — before the per-instance polling loop
// has stamped its first runtime-state snapshot. Since B1.6 it shares the
// connection cache; the per-instance Category still applies as a
// server-side filter because the cached view carries it.
type QbitTorrentsListerFunc struct {
	cache *qbit.ClientCache
}

// NewQbitTorrentsLister wires the adapter to the shared connection cache.
func NewQbitTorrentsLister(cache *qbit.ClientCache) QbitTorrentsListerFunc {
	return QbitTorrentsListerFunc{cache: cache}
}

// ListTorrents implements handlers.QbitTorrentsLister. The returned slice
// is empty when qBit is unreachable, unauthenticated, or returns no
// torrents in the configured category. Errors are surfaced for the
// caller's debug logging only — the rollup handler treats any non-nil
// error as "fall back to the prior RuntimeStateStore value".
func (f QbitTorrentsListerFunc) ListTorrents(ctx context.Context, s appregrab.Settings) ([]qbit.Torrent, error) {
	client, err := clientFor(f.cache, s)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	if err := client.Login(ctx); err != nil {
		return nil, err
	}
	return client.ListTorrents(ctx)
}

// DetectorFactoryFunc satisfies application/regrab.DetectorFactory by
// wrapping qbit.NewDetector. The use case calls this once per cycle
// with the per-instance customMsgs slice.
type DetectorFactoryFunc struct{}

// NewDetector implements application/regrab.DetectorFactory. The
// return type is the use case's Detector interface — qbit.Detector
// satisfies it implicitly by exposing Detect.
func (DetectorFactoryFunc) NewDetector(c qbit.Client, customMsgs []string) appregrab.Detector {
	d := qbit.NewDetector(c, customMsgs)
	return detectorAdapter{d: d}
}

// detectorAdapter narrows *qbit.Detector to the regrab.Detector
// interface so the test mocks in application/regrab/mocks/ can stand
// in without importing infrastructure/qbit. The adapter is one method
// thick — Detect.
type detectorAdapter struct {
	d *qbit.Detector
}

func (a detectorAdapter) Detect(ctx context.Context, hash string) (qbit.DetectionResult, error) {
	return a.d.Detect(ctx, hash)
}
