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

	appregrab "github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
)

// QbitClientFactoryFunc satisfies application/regrab.QbitClientFactory
// by mapping Settings → qbit.Config → qbit.NewClient.
type QbitClientFactoryFunc struct{}

// NewClient implements application/regrab.QbitClientFactory.
//
// Settings.PasswordPlaintext is the already-decrypted password (the
// use case's Lookup step ran the cipher). The factory does not see
// ciphertext.
func (QbitClientFactoryFunc) NewClient(s appregrab.Settings) (qbit.Client, error) {
	return qbit.NewClient(qbit.Config{
		URL:      s.URL,
		Username: s.Username,
		Password: s.PasswordPlaintext,
		Category: s.Category,
		// Timeout left zero → qbit.NewClient applies its 30s default.
	})
}

// QbitProbeFunc satisfies handlers.QbitProbe. It builds a transient
// qbit.Client, calls Ping with the supplied ctx, and closes the client.
// Story 090 introduced this so the watchdog rollup handler can fill
// QbitReachable before the per-instance polling loop has run for the
// first time after a pod restart.
type QbitProbeFunc struct{}

// Probe implements handlers.QbitProbe. Returns true when qBit responded
// to /api/v2/app/version within the supplied ctx deadline. Any other
// outcome (timeout, network error, unauthenticated) returns false; the
// error is surfaced for caller-side debug logging only.
func (QbitProbeFunc) Probe(ctx context.Context, s appregrab.Settings) (bool, error) {
	client, err := qbit.NewClient(qbit.Config{
		URL:      s.URL,
		Username: s.Username,
		Password: s.PasswordPlaintext,
		Category: s.Category,
	})
	if err != nil {
		return false, err
	}
	defer func() { _ = client.Close() }()
	if err := client.Ping(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// QbitTorrentsListerFunc satisfies handlers.QbitTorrentsLister. It
// builds a transient qbit.Client, lists torrents (server-side filtered
// by Settings.Category), and closes the client. Story 094 added this
// so the watchdog rollup handler can compute the watched and
// unregistered counters on demand — before the per-instance polling
// loop has stamped its first runtime-state snapshot.
type QbitTorrentsListerFunc struct{}

// ListTorrents implements handlers.QbitTorrentsLister. The returned
// slice is empty when qBit is unreachable, unauthenticated, or returns
// no torrents in the configured category. Errors are surfaced for the
// caller's debug logging only — the rollup handler treats any non-nil
// error as "fall back to the prior RuntimeStateStore value".
func (QbitTorrentsListerFunc) ListTorrents(ctx context.Context, s appregrab.Settings) ([]qbit.Torrent, error) {
	client, err := qbit.NewClient(qbit.Config{
		URL:      s.URL,
		Username: s.Username,
		Password: s.PasswordPlaintext,
		Category: s.Category,
	})
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
