package dataports

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// GapEpisodeRow is one bounded library-gap drill-down row: a monitored,
// already-aired, fileless canonical episode (season > 0) for a given
// Sonarr instance. Each row carries its series title plus the
// per-(series, season) aired-monitored totals used to decide
// whole-season-missing — computed via correlated subqueries so the
// totals stay accurate even when the episode list is LIMIT-truncated.
type GapEpisodeRow struct {
	SeriesID      domain.SeriesID
	Title         string
	SeasonNumber  int
	EpisodeNumber int
	EpisodeID     domain.EpisodeID
	AirDate       *time.Time
	// SeasonAiredMonitored — total aired monitored episodes (season > 0)
	// for this (series, season) in this instance.
	SeasonAiredMonitored int
	// SeasonMissing — of those, how many lack a file. Whole-season-missing
	// when SeasonMissing == SeasonAiredMonitored && SeasonAiredMonitored > 0.
	SeasonMissing int
}

// GapRepository surfaces the read-only library-gap queries backing
// GET /api/v1/insights/gaps. A "gap" is a monitored, already-aired,
// fileless canonical episode (specials — season 0 — excluded). The
// "already aired" boundary (now) is computed in Go by the usecase and
// passed as a bind param so the SQLite test lane and the Postgres prod
// target agree (no NOW()/INTERVAL/cast/FILTER). All predicates filter
// episode_states.deleted_at IS NULL (SeriesDelete cascade soft-deletes).
type GapRepository interface {
	// DistinctInstances lists instance_name values that have at least one
	// live episode_states row, ordered ascending.
	DistinctInstances(ctx context.Context) ([]string, error)
	// MissingEpisodeCount counts aired monitored fileless episodes
	// (season > 0) for the instance.
	MissingEpisodeCount(ctx context.Context, instance string, now time.Time) (int, error)
	// WholeSeasonMissingCount counts (series, season) pairs whose every
	// aired monitored episode (season > 0) lacks a file.
	WholeSeasonMissingCount(ctx context.Context, instance string, now time.Time) (int, error)
	// GapEpisodes returns a bounded (top-N), ordered slice of gap episodes
	// for the instance, each carrying its per-season aired-monitored +
	// missing totals. Ordered by series_id, season_number, episode_number.
	GapEpisodes(ctx context.Context, instance string, now time.Time, limit int) ([]GapEpisodeRow, error)
}
