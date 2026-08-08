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

// GapSeriesRank is one row of the top-N series ranking: a series that has
// at least one gap, with its EXACT instance-wide gap total. Ordered
// biggest-gap-first. This is the authoritative drill-down list — the
// series set, their order, titles and per-series missing badge all come
// from here, never from the (safety-capped) episode detail. So the detail
// cap can only clip a tail series' episode list, never drop a series.
type GapSeriesRank struct {
	SeriesID domain.SeriesID
	Title    string
	// GapCount — exact number of aired monitored fileless episodes
	// (season > 0) for this series in this instance. Feeds GapSeries.MissingCount.
	GapCount int
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
	// GapSeriesRanked returns the top-N series with the most gaps, ordered
	// by gap count DESC (series_id ASC tiebreak), each with its EXACT
	// per-series gap total. limitSeries bounds the number of series.
	GapSeriesRanked(ctx context.Context, instance string, now time.Time, limitSeries int) ([]GapSeriesRank, error)
	// GapEpisodesForSeries returns the gap episodes for the given series,
	// each carrying its per-(series, season) aired-monitored + missing
	// totals. limitEpisodes is a generous safety cap on the flat row
	// count (it can clip a tail series' episodes but never drops a
	// series — the series set comes from GapSeriesRanked). An empty
	// seriesIDs slice returns an empty result without querying.
	GapEpisodesForSeries(ctx context.Context, instance string, now time.Time, seriesIDs []domain.SeriesID, limitEpisodes int) ([]GapEpisodeRow, error)
}
