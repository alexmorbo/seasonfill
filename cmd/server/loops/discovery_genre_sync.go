// discovery_genre_sync.go is the goroutine entry point for the
// discovery GenreSyncer (story 540 / B-49). Pattern mirrors
// discovery.go: the wirer owns construction, the loop owns ctx +
// boot/shutdown log lines.
package loops

import (
	"context"
	"log/slog"
	"time"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
)

// DefaultDiscoveryGenreSyncInterval mirrors discoapp.DefaultGenreSyncInterval
// at the loops boundary so server.go can pass
// loops.DefaultDiscoveryGenreSyncInterval alongside
// loops.DefaultDiscoveryInterval without an extra import.
const DefaultDiscoveryGenreSyncInterval = discoapp.DefaultGenreSyncInterval

// RunDiscoveryGenreSync blocks until ctx is cancelled. interval<=0
// falls back to DefaultDiscoveryGenreSyncInterval (24h).
func RunDiscoveryGenreSync(ctx context.Context, s *discoapp.GenreSyncer, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = DefaultDiscoveryGenreSyncInterval
	}
	if log != nil {
		log.InfoContext(ctx, "discovery genre sync started",
			slog.Duration("interval", interval))
	}
	s.RunForever(ctx, interval)
	if log != nil {
		log.InfoContext(ctx, "discovery genre sync stopped")
	}
}
