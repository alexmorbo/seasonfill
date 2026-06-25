// genre_sync.go syncs the canonical TMDB genre name catalog into
// genres_i18n for every locale.SupportedUserLanguages tag. Story 540 /
// B-49: without this loop the picker SQL fallback collapses ru-RU back
// to en-US for the 10/18 TV genres that the per-series enrichment
// worker has never touched in Russian.
//
// Cadence: one pass at boot, then every 24h. TMDB updates this list
// roughly once a year — the cadence is "operator-imperceptible refresh"
// not "data freshness". Failures degrade gracefully: existing rows
// stay, next tick retries.
//
// Wiring lives in internal/wiring/discovery.go; the goroutine entry
// point lives in cmd/server/loops/discovery_genre_sync.go.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// DefaultGenreSyncInterval is the production cadence — see package doc.
const DefaultGenreSyncInterval = 24 * time.Hour

// TMDBGenreLister is the narrow TMDB seam GenreSyncer needs. The real
// implementation lives in internal/wiring/discovery.go where the wiring
// adapter bridges *tmdb.Client.GenreListTV to the package-local
// GenreListResult shape.
type TMDBGenreLister interface {
	GenreListTV(ctx context.Context, language string) (*GenreListResult, error)
}

// GenreListResult is the wire-decoupled shape the syncer consumes.
// The wiring adapter converts tmdb.GenreListResponse → GenreListResult
// (one field rename: Genres → Items) so the discovery package never
// imports the tmdb client's type.
type GenreListResult struct {
	Items []GenreListItem
}

// GenreListItem is one row of GenreListResult.Items.
type GenreListItem struct {
	ID   int
	Name string
}

// GenreUpserter writes one canon genre row (by tmdb_id) and returns
// the canon PK. Implemented by enrichpersistence.GenresRepository.Upsert.
type GenreUpserter interface {
	Upsert(ctx context.Context, g taxonomy.Genre) (int64, error)
}

// GenreI18nUpserter writes the localised name row. Implemented by
// enrichpersistence.GenresI18nRepository.Upsert.
type GenreI18nUpserter interface {
	Upsert(ctx context.Context, t taxonomy.GenreI18n) error
}

// GenreSyncerDeps is the input contract.
type GenreSyncerDeps struct {
	TMDB      TMDBGenreLister
	Genres    GenreUpserter
	I18n      GenreI18nUpserter
	Languages []string // non-empty list of BCP-47 tags.
	Log       *slog.Logger
}

// GenreSyncer drives one pass over Languages. Stateless — safe to call
// Tick concurrently if a future RunForever variant adds it.
type GenreSyncer struct {
	deps GenreSyncerDeps
}

// NewGenreSyncer wraps deps. A nil Log is caller's choice — log calls
// inside Tick guard against nil to keep tests terse without requiring
// every caller to wire a logger.
func NewGenreSyncer(deps GenreSyncerDeps) *GenreSyncer {
	return &GenreSyncer{deps: deps}
}

// Tick performs one pass: for each language, fetch TMDB, upsert the
// canon genre by tmdb_id, then upsert the per-language name row.
// Per-language failure is logged and DOES NOT short-circuit the loop;
// a transient ru-RU outage must not poison en-US. Returns the first
// error seen (for caller's retry-tracking), or nil if all langs OK.
func (s *GenreSyncer) Tick(ctx context.Context) error {
	if len(s.deps.Languages) == 0 {
		return fmt.Errorf("genre sync: languages must be non-empty")
	}
	var firstErr error
	for _, lang := range s.deps.Languages {
		if err := s.tickLanguage(ctx, lang); err != nil {
			if s.deps.Log != nil {
				s.deps.Log.WarnContext(ctx, "genre sync failed for language",
					slog.String("language", lang), slog.String("error", err.Error()))
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
	}
	return firstErr
}

func (s *GenreSyncer) tickLanguage(ctx context.Context, lang string) error {
	resp, err := s.deps.TMDB.GenreListTV(ctx, lang)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", lang, err)
	}
	var written int
	for _, item := range resp.Items {
		if item.ID <= 0 || item.Name == "" {
			continue
		}
		tmdbID := domain.TMDBID(item.ID)
		canonID, err := s.deps.Genres.Upsert(ctx, taxonomy.Genre{
			TMDBID: &tmdbID,
		})
		if err != nil {
			return fmt.Errorf("upsert genre tmdb_id=%d: %w", item.ID, err)
		}
		if err := s.deps.I18n.Upsert(ctx, taxonomy.GenreI18n{
			GenreID:  canonID,
			Language: lang,
			Name:     item.Name,
		}); err != nil {
			return fmt.Errorf("upsert i18n tmdb_id=%d lang=%s: %w", item.ID, lang, err)
		}
		written++
	}
	if s.deps.Log != nil {
		s.deps.Log.InfoContext(ctx, "genre sync language complete",
			slog.String("language", lang),
			slog.Int("written", written))
	}
	return nil
}

// RunForever fires Tick immediately, then every interval. interval<=0
// falls back to DefaultGenreSyncInterval. Exits when ctx is cancelled.
func (s *GenreSyncer) RunForever(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultGenreSyncInterval
	}
	_ = s.Tick(ctx) // boot pass; error is already logged inside.
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = s.Tick(ctx)
		}
	}
}
