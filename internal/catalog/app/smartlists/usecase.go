// Package smartlists assembles the read-only "smart lists" report backing
// GET /api/v1/insights/lists. It fans out across every instance with a live
// series_cache row (or the single ?instance= filter) and builds three fixed
// shelves per instance: ended_incomplete, returning_soon, hiatus. The usecase
// owns the wall clock and derives the returning-soon window (now+35d) and the
// hiatus cutoff (now-90d), passing them as bind params to the repo. All DB
// work lives behind a narrow SmartListsRepository port.
package smartlists

import (
	"context"
	"fmt"
	"time"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// shelfSeriesLimit bounds each shelf's series slice. The shelf `Count` is the
// EXACT matching total (from the *Count repo methods), independent of this cap.
const shelfSeriesLimit = 50

// returningSoonWindow — a next episode airing within 35 days is "returning
// soon". Wide enough to catch a monthly cadence, tight enough to stay a
// short-list.
const returningSoonWindow = 35 * 24 * time.Hour

// hiatusThreshold — a returning series whose last aired episode is older than
// 90 days (with no scheduled next airing) is "on hiatus".
const hiatusThreshold = 90 * 24 * time.Hour

// Shelf keys + stable English titles. The FE localizes by Key; Title is a
// machine-stable fallback label.
const (
	keyEndedIncomplete = "ended_incomplete"
	keyReturningSoon   = "returning_soon"
	keyHiatus          = "hiatus"
)

// SmartListSeries is one series on a shelf. Exactly one of MissingCount /
// NextAirDate / LastAiredAt is set, per the owning shelf.
type SmartListSeries struct {
	SeriesID     domain.SeriesID
	SonarrID     domain.SonarrSeriesID
	Title        string
	MissingCount *int
	NextAirDate  *time.Time
	LastAiredAt  *time.Time
}

// Shelf is one named list. Count is the exact matching total; Series is the
// bounded (top-50) slice.
type Shelf struct {
	Key    string
	Title  string
	Count  int
	Series []SmartListSeries
}

// Instance is the per-instance shelf set (always all three shelves).
type Instance struct {
	InstanceName string
	Shelves      []Shelf
}

// Report is the assembled smart-lists report.
type Report struct {
	GeneratedAt time.Time
	Instances   []Instance
}

// UseCase builds the Report from the SmartListsRepository port.
type UseCase struct {
	repo  ports.SmartListsRepository
	clock func() time.Time
}

// NewUseCase wires the read-only smart-lists usecase. Clock defaults to
// time.Now().UTC.
func NewUseCase(repo ports.SmartListsRepository) *UseCase {
	return &UseCase{
		repo:  repo,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

// WithClock swaps the clock for deterministic tests. The returned now anchors
// the aired boundary, the returning-soon window and the hiatus cutoff.
func (uc *UseCase) WithClock(clock func() time.Time) *UseCase {
	uc.clock = clock
	return uc
}

// Build assembles the report. A non-empty instanceFilter scopes to that single
// instance; an empty filter fans out across every instance with a live
// series_cache row. Any query error aborts (a partial report would mislead).
func (uc *UseCase) Build(ctx context.Context, instanceFilter string) (Report, error) {
	now := uc.clock()
	rep := Report{GeneratedAt: now, Instances: []Instance{}}

	var instances []string
	if instanceFilter != "" {
		instances = []string{instanceFilter}
	} else {
		var err error
		instances, err = uc.repo.DistinctInstances(ctx)
		if err != nil {
			return Report{}, fmt.Errorf("smartlists build: list instances: %w", err)
		}
	}

	until := now.Add(returningSoonWindow)
	cutoff := now.Add(-hiatusThreshold)

	for _, inst := range instances {
		si, err := uc.buildInstance(ctx, inst, now, until, cutoff)
		if err != nil {
			return Report{}, err
		}
		rep.Instances = append(rep.Instances, si)
	}
	return rep, nil
}

func (uc *UseCase) buildInstance(ctx context.Context, instance string, now, until, cutoff time.Time) (Instance, error) {
	ended, err := uc.repo.EndedIncomplete(ctx, instance, now, shelfSeriesLimit)
	if err != nil {
		return Instance{}, fmt.Errorf("smartlists build: %s: ended_incomplete: %w", instance, err)
	}
	endedCount, err := uc.repo.EndedIncompleteCount(ctx, instance, now)
	if err != nil {
		return Instance{}, fmt.Errorf("smartlists build: %s: ended_incomplete_count: %w", instance, err)
	}
	returning, err := uc.repo.ReturningSoon(ctx, instance, now, until, shelfSeriesLimit)
	if err != nil {
		return Instance{}, fmt.Errorf("smartlists build: %s: returning_soon: %w", instance, err)
	}
	returningCount, err := uc.repo.ReturningSoonCount(ctx, instance, now, until)
	if err != nil {
		return Instance{}, fmt.Errorf("smartlists build: %s: returning_soon_count: %w", instance, err)
	}
	hiatus, err := uc.repo.Hiatus(ctx, instance, now, cutoff, shelfSeriesLimit)
	if err != nil {
		return Instance{}, fmt.Errorf("smartlists build: %s: hiatus: %w", instance, err)
	}
	hiatusCount, err := uc.repo.HiatusCount(ctx, instance, now, cutoff)
	if err != nil {
		return Instance{}, fmt.Errorf("smartlists build: %s: hiatus_count: %w", instance, err)
	}

	return Instance{
		InstanceName: instance,
		Shelves: []Shelf{
			{Key: keyEndedIncomplete, Title: "Ended with gaps", Count: endedCount, Series: mapEnded(ended)},
			{Key: keyReturningSoon, Title: "Returning soon", Count: returningCount, Series: mapReturning(returning)},
			{Key: keyHiatus, Title: "On hiatus", Count: hiatusCount, Series: mapHiatus(hiatus)},
		},
	}, nil
}

func mapEnded(rows []ports.SmartListSeriesRow) []SmartListSeries {
	out := make([]SmartListSeries, 0, len(rows))
	for _, r := range rows {
		mc := r.MissingCount
		out = append(out, SmartListSeries{
			SeriesID: r.SeriesID, SonarrID: r.SonarrID, Title: r.Title, MissingCount: &mc,
		})
	}
	return out
}

func mapReturning(rows []ports.SmartListSeriesRow) []SmartListSeries {
	out := make([]SmartListSeries, 0, len(rows))
	for _, r := range rows {
		out = append(out, SmartListSeries{
			SeriesID: r.SeriesID, SonarrID: r.SonarrID, Title: r.Title, NextAirDate: r.NextAirDate,
		})
	}
	return out
}

func mapHiatus(rows []ports.SmartListSeriesRow) []SmartListSeries {
	out := make([]SmartListSeries, 0, len(rows))
	for _, r := range rows {
		out = append(out, SmartListSeries{
			SeriesID: r.SeriesID, SonarrID: r.SonarrID, Title: r.Title, LastAiredAt: r.LastAiredAt,
		})
	}
	return out
}
