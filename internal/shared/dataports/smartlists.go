package dataports

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SmartListSeriesRow is one series row on a smart-list shelf. A single row
// type is shared across shelves; each repo method populates only the fields
// its shelf defines:
//   - ended_incomplete → MissingCount (exact aired-monitored-fileless total)
//   - returning_soon   → NextAirDate  (series.next_air_date, inside window)
//   - hiatus           → LastAiredAt  (MAX aired episode air_date)
//
// Title is COALESCE(series.original_title,”) — mirrors the gaps/stats series
// title projection.
type SmartListSeriesRow struct {
	SeriesID     domain.SeriesID
	SonarrID     domain.SonarrSeriesID
	Title        string
	MissingCount int
	NextAirDate  *time.Time
	LastAiredAt  *time.Time
}

// SmartListsRepository surfaces the read-only "smart lists" shelves backing
// GET /api/v1/insights/lists. Every method is a bounded COUNT / top-N query
// over EXISTING tables (series_cache / series / episodes / episode_states);
// no writes, no migration. The wall-clock boundaries (now / until / cutoff)
// are computed in Go by the usecase and passed as bind params so the SQLite
// test lane and the Postgres prod target agree (no NOW()/INTERVAL/FILTER/cast;
// only LOWER/COALESCE/IN/GROUP BY/HAVING/LIMIT). Every query is instance-scoped
// (series_cache.instance_name = ? AND series_cache.deleted_at IS NULL) and,
// where episodes are involved, episode_states.deleted_at IS NULL.
type SmartListsRepository interface {
	// DistinctInstances lists instance_name values with at least one live
	// series_cache row (deleted_at IS NULL), ordered ascending.
	DistinctInstances(ctx context.Context) ([]string, error)

	// EndedIncomplete returns up to `limit` terminal-status series (LOWER
	// status in ended/canceled/cancelled) that have at least one aired,
	// monitored, fileless canonical episode (season > 0, air_date <= now),
	// ordered by missing count DESC (series_id ASC tiebreak). MissingCount
	// is the exact per-series aired-monitored-fileless total.
	EndedIncomplete(ctx context.Context, instance string, now time.Time, limit int) ([]SmartListSeriesRow, error)
	// EndedIncompleteCount is the exact number of series matching the shelf.
	EndedIncompleteCount(ctx context.Context, instance string, now time.Time) (int, error)

	// ReturningSoon returns up to `limit` series whose next_air_date falls in
	// [now, until], ordered by next_air_date ASC (series_id ASC tiebreak).
	ReturningSoon(ctx context.Context, instance string, now, until time.Time, limit int) ([]SmartListSeriesRow, error)
	// ReturningSoonCount is the exact number of series matching the shelf.
	ReturningSoonCount(ctx context.Context, instance string, now, until time.Time) (int, error)

	// Hiatus returns up to `limit` returning-status series (LOWER status in
	// returning series/continuing) with NO scheduled next airing whose last
	// aired episode (air_date <= now) is older than cutoff, ordered oldest
	// last-aired first (series_id ASC tiebreak). LastAiredAt is that MAX
	// aired episode air_date.
	Hiatus(ctx context.Context, instance string, now, cutoff time.Time, limit int) ([]SmartListSeriesRow, error)
	// HiatusCount is the exact number of series matching the shelf.
	HiatusCount(ctx context.Context, instance string, now, cutoff time.Time) (int, error)
}
