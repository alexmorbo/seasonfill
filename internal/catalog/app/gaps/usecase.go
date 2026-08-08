// Package gaps assembles the read-only library-gap report backing
// GET /api/v1/insights/gaps. A "gap" is a monitored, already-aired,
// fileless canonical episode (specials — season 0 — excluded). The
// usecase owns the wall clock (now), derives the per-instance list when
// no filter is given, and assembles the top-N series ranking + its gap
// episode detail into the nested instance → series → season → episode
// structure. The DB queries live behind a narrow GapRepository port.
package gaps

import (
	"context"
	"fmt"
	"time"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// seriesDrillDownLimit bounds the drill-down to the top-N SERIES with the
// most gaps (biggest-gap-first). Each series carries its EXACT gap total
// from the rank query, so the badge is right even when a series' episode
// list is clipped by gapDetailEpisodeCap.
const seriesDrillDownLimit = 50

// gapDetailEpisodeCap is a generous SAFETY cap on the flat gap-episode
// detail row count across the top-N series. It exists only to bound a
// pathological payload; because the series set/order/title/badge come from
// the rank query, this cap can only clip a tail series' episode list — it
// can never drop a series from the report.
const gapDetailEpisodeCap = 5000

// GapEpisode is one aired, monitored, fileless episode in the drill-down.
type GapEpisode struct {
	EpisodeID     domain.EpisodeID
	SeasonNumber  int
	EpisodeNumber int
	AirDate       *time.Time
}

// GapSeason is a per-season gap breakdown. MissingCount / AiredMonitoredCount
// are exact per-season totals; WholeSeasonMissing is true when every aired
// monitored episode of the season lacks a file. Episodes is the bounded list.
type GapSeason struct {
	SeasonNumber        int
	MissingCount        int
	AiredMonitoredCount int
	WholeSeasonMissing  bool
	Episodes            []GapEpisode
}

// GapSeries groups a series's gap seasons. MissingCount is the EXACT
// instance-wide per-series gap total (from the rank query), independent of
// how many episodes made it into the capped detail list.
type GapSeries struct {
	SeriesID     domain.SeriesID
	Title        string
	MissingCount int
	Seasons      []GapSeason
}

// GapInstance is the per-instance gap report. MissingEpisodeCount and
// WholeSeasonMissingCount are exact instance-wide totals; Series is the
// bounded top-N drill-down.
type GapInstance struct {
	InstanceName            string
	MissingEpisodeCount     int
	WholeSeasonMissingCount int
	Series                  []GapSeries
}

// Report is the assembled library-gap report.
type Report struct {
	GeneratedAt time.Time
	Instances   []GapInstance
}

// UseCase builds the Report from the GapRepository port.
type UseCase struct {
	repo  ports.GapRepository
	clock func() time.Time
}

// NewUseCase wires the read-only gaps usecase. Clock defaults to
// time.Now().UTC.
func NewUseCase(repo ports.GapRepository) *UseCase {
	return &UseCase{
		repo:  repo,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

// WithClock swaps the clock for deterministic tests. The returned now is
// passed to every repo call as the "already aired" bind boundary.
func (uc *UseCase) WithClock(clock func() time.Time) *UseCase {
	uc.clock = clock
	return uc
}

// Build assembles the report. With a non-empty instanceFilter it scopes
// to that single instance (still returning a single zero-count element
// when that instance is healthy or unknown). Empty filter aggregates
// across every instance that has episode_states rows, grouped per
// instance. Any query error aborts (a partial report would mislead).
func (uc *UseCase) Build(ctx context.Context, instanceFilter string) (Report, error) {
	now := uc.clock()
	rep := Report{GeneratedAt: now, Instances: []GapInstance{}}

	var instances []string
	if instanceFilter != "" {
		instances = []string{instanceFilter}
	} else {
		var err error
		instances, err = uc.repo.DistinctInstances(ctx)
		if err != nil {
			return Report{}, fmt.Errorf("gaps build: list instances: %w", err)
		}
	}

	for _, inst := range instances {
		gi, err := uc.buildInstance(ctx, inst, now)
		if err != nil {
			return Report{}, err
		}
		rep.Instances = append(rep.Instances, gi)
	}
	return rep, nil
}

func (uc *UseCase) buildInstance(ctx context.Context, instance string, now time.Time) (GapInstance, error) {
	missingCount, err := uc.repo.MissingEpisodeCount(ctx, instance, now)
	if err != nil {
		return GapInstance{}, fmt.Errorf("gaps build: %s: missing count: %w", instance, err)
	}
	wholeSeason, err := uc.repo.WholeSeasonMissingCount(ctx, instance, now)
	if err != nil {
		return GapInstance{}, fmt.Errorf("gaps build: %s: whole-season count: %w", instance, err)
	}
	ranks, err := uc.repo.GapSeriesRanked(ctx, instance, now, seriesDrillDownLimit)
	if err != nil {
		return GapInstance{}, fmt.Errorf("gaps build: %s: series ranked: %w", instance, err)
	}
	ids := make([]domain.SeriesID, 0, len(ranks))
	for _, rk := range ranks {
		ids = append(ids, rk.SeriesID)
	}
	rows, err := uc.repo.GapEpisodesForSeries(ctx, instance, now, ids, gapDetailEpisodeCap)
	if err != nil {
		return GapInstance{}, fmt.Errorf("gaps build: %s: gap episodes: %w", instance, err)
	}
	return GapInstance{
		InstanceName:            instance,
		MissingEpisodeCount:     missingCount,
		WholeSeasonMissingCount: wholeSeason,
		Series:                  assembleSeries(ranks, rows),
	}, nil
}

// seasonAcc holds the in-progress season aggregates for one series while
// detail rows are folded in. Pointers keep the season aggregates stable
// while episodes are appended.
type seasonAcc struct {
	order   []int
	seasons map[int]*GapSeason
}

// assembleSeries builds the nested series → season → episode structure in
// RANK order. Series identity, order, Title and MissingCount come from the
// authoritative rank list; the detail rows only fill in seasons/episodes.
// A ranked series with no detail rows (should not happen normally) still
// appears with its exact MissingCount and an empty (non-nil) Seasons slice.
// Season order within a series is first-seen in the detail rows (the detail
// query orders by season_number, episode_number).
func assembleSeries(ranks []ports.GapSeriesRank, rows []ports.GapEpisodeRow) []GapSeries {
	detail := make(map[domain.SeriesID]*seasonAcc, len(ranks))
	for _, r := range rows {
		acc, ok := detail[r.SeriesID]
		if !ok {
			acc = &seasonAcc{seasons: make(map[int]*GapSeason)}
			detail[r.SeriesID] = acc
		}
		season, ok := acc.seasons[r.SeasonNumber]
		if !ok {
			season = &GapSeason{
				SeasonNumber:        r.SeasonNumber,
				MissingCount:        r.SeasonMissing,
				AiredMonitoredCount: r.SeasonAiredMonitored,
				WholeSeasonMissing:  r.SeasonMissing == r.SeasonAiredMonitored && r.SeasonAiredMonitored > 0,
			}
			acc.seasons[r.SeasonNumber] = season
			acc.order = append(acc.order, r.SeasonNumber)
		}
		season.Episodes = append(season.Episodes, GapEpisode{
			EpisodeID:     r.EpisodeID,
			SeasonNumber:  r.SeasonNumber,
			EpisodeNumber: r.EpisodeNumber,
			AirDate:       r.AirDate,
		})
	}

	out := make([]GapSeries, 0, len(ranks))
	for _, rk := range ranks {
		gs := GapSeries{
			SeriesID:     rk.SeriesID,
			Title:        rk.Title,
			MissingCount: rk.GapCount,
			Seasons:      []GapSeason{},
		}
		if acc, ok := detail[rk.SeriesID]; ok {
			seasons := make([]GapSeason, 0, len(acc.order))
			for _, sn := range acc.order {
				seasons = append(seasons, *acc.seasons[sn])
			}
			gs.Seasons = seasons
		}
		out = append(out, gs)
	}
	return out
}
