// Package gaps assembles the read-only library-gap report backing
// GET /api/v1/insights/gaps. A "gap" is a monitored, already-aired,
// fileless canonical episode (specials — season 0 — excluded). The
// usecase owns the wall clock (now), derives the per-instance list when
// no filter is given, and assembles the flat gap-episode drill-down into
// the nested instance → series → season → episode structure. The DB
// queries live behind a narrow GapRepository port.
package gaps

import (
	"context"
	"fmt"
	"time"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// drillDownLimit bounds the gap-episode drill-down per instance. Mirrors
// the health pulse's 50-row operator triage window; the per-season and
// per-instance COUNTs remain exact (correlated subqueries) even when the
// listed episodes are truncated to this cap.
const drillDownLimit = 50

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

// GapSeries groups a series's gap seasons. MissingCount is the sum of the
// per-season exact missing counts present in the drill-down.
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
	rows, err := uc.repo.GapEpisodes(ctx, instance, now, drillDownLimit)
	if err != nil {
		return GapInstance{}, fmt.Errorf("gaps build: %s: gap episodes: %w", instance, err)
	}
	return GapInstance{
		InstanceName:            instance,
		MissingEpisodeCount:     missingCount,
		WholeSeasonMissingCount: wholeSeason,
		Series:                  assembleSeries(rows),
	}, nil
}

// seriesAcc holds the in-progress nested structure for one series.
// Pointers keep the season aggregates stable while episodes are appended;
// the flat slices are materialized only at the end.
type seriesAcc struct {
	series      *GapSeries
	seasonOrder []int
	seasons     map[int]*GapSeason
}

// assembleSeries folds the flat, pre-ordered (series, season, episode)
// gap rows into the nested series → season → episode structure,
// preserving the SQL ordering.
func assembleSeries(rows []ports.GapEpisodeRow) []GapSeries {
	order := make([]domain.SeriesID, 0)
	accs := make(map[domain.SeriesID]*seriesAcc)

	for _, r := range rows {
		acc, ok := accs[r.SeriesID]
		if !ok {
			acc = &seriesAcc{
				series:  &GapSeries{SeriesID: r.SeriesID, Title: r.Title},
				seasons: make(map[int]*GapSeason),
			}
			accs[r.SeriesID] = acc
			order = append(order, r.SeriesID)
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
			acc.seasonOrder = append(acc.seasonOrder, r.SeasonNumber)
			acc.series.MissingCount += r.SeasonMissing
		}
		season.Episodes = append(season.Episodes, GapEpisode{
			EpisodeID:     r.EpisodeID,
			SeasonNumber:  r.SeasonNumber,
			EpisodeNumber: r.EpisodeNumber,
			AirDate:       r.AirDate,
		})
	}

	out := make([]GapSeries, 0, len(order))
	for _, id := range order {
		acc := accs[id]
		seasons := make([]GapSeason, 0, len(acc.seasonOrder))
		for _, sn := range acc.seasonOrder {
			seasons = append(seasons, *acc.seasons[sn])
		}
		acc.series.Seasons = seasons
		out = append(out, *acc.series)
	}
	return out
}
