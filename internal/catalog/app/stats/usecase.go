// Package stats assembles the read-only library-statistics report backing
// GET /api/v1/insights/stats. It fans out across every instance with a live
// series_cache row (or the single ?instance= filter), rolls up totals,
// top-N genre/network buckets, grab terminal-state counts and qBit torrent
// totals, and derives the grab success_rate. All DB work lives behind a
// narrow StatsRepository port.
package stats

import (
	"context"
	"fmt"
	"time"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// kindLimit bounds the top-N genre/network drill-down. 20 is a comfortable
// operator window without turning the report into a full taxonomy export.
const kindLimit = 20

// Totals is the per-instance library rollup.
type Totals struct {
	SeriesCount    int
	EpisodesOnDisk int
	TotalSizeBytes int64
}

// KindBucket is one genre/network row (resolved name + counts + size).
type KindBucket struct {
	Name        string
	SeriesCount int
	SizeBytes   int64
}

// GrabSuccess is the terminal-state grab breakdown plus the derived rate.
// SuccessRate = Imported / (Imported + Failed); 0 when the denominator is 0.
// Grabbed (in-flight) is intentionally excluded from the denominator.
type GrabSuccess struct {
	Grabbed     int
	Imported    int
	Failed      int
	SuccessRate float64
}

// TorrentTotals is the per-instance qBit rollup.
type TorrentTotals struct {
	TorrentCount         int
	TotalUploadedBytes   int64
	TotalDownloadedBytes int64
	AvgRatio             float64
}

// Instance is the per-instance statistics block.
type Instance struct {
	InstanceName  string
	Totals        Totals
	ByGenre       []KindBucket
	ByNetwork     []KindBucket
	GrabSuccess   GrabSuccess
	TorrentTotals TorrentTotals
}

// Report is the assembled library-statistics report.
type Report struct {
	GeneratedAt time.Time
	Instances   []Instance
}

// UseCase builds the Report from the StatsRepository port.
type UseCase struct {
	repo  ports.StatsRepository
	clock func() time.Time
}

// NewUseCase wires the read-only stats usecase. Clock defaults to
// time.Now().UTC (used only for the report's GeneratedAt stamp).
func NewUseCase(repo ports.StatsRepository) *UseCase {
	return &UseCase{
		repo:  repo,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

// WithClock swaps the clock for deterministic tests.
func (uc *UseCase) WithClock(clock func() time.Time) *UseCase {
	uc.clock = clock
	return uc
}

// Build assembles the report. A non-empty instanceFilter scopes to that
// single instance (still a single-element list even when it is unknown/
// empty); an empty filter fans out across every instance with a live
// series_cache row. Any query error aborts (a partial report would mislead).
func (uc *UseCase) Build(ctx context.Context, instanceFilter string) (Report, error) {
	rep := Report{GeneratedAt: uc.clock(), Instances: []Instance{}}

	var instances []string
	if instanceFilter != "" {
		instances = []string{instanceFilter}
	} else {
		var err error
		instances, err = uc.repo.DistinctInstances(ctx)
		if err != nil {
			return Report{}, fmt.Errorf("stats build: list instances: %w", err)
		}
	}

	for _, inst := range instances {
		si, err := uc.buildInstance(ctx, inst)
		if err != nil {
			return Report{}, err
		}
		rep.Instances = append(rep.Instances, si)
	}
	return rep, nil
}

func (uc *UseCase) buildInstance(ctx context.Context, instance string) (Instance, error) {
	totals, err := uc.repo.Totals(ctx, instance)
	if err != nil {
		return Instance{}, fmt.Errorf("stats build: %s: totals: %w", instance, err)
	}
	byGenre, err := uc.repo.ByGenre(ctx, instance, kindLimit)
	if err != nil {
		return Instance{}, fmt.Errorf("stats build: %s: by_genre: %w", instance, err)
	}
	byNetwork, err := uc.repo.ByNetwork(ctx, instance, kindLimit)
	if err != nil {
		return Instance{}, fmt.Errorf("stats build: %s: by_network: %w", instance, err)
	}
	grabs, err := uc.repo.GrabSuccess(ctx, instance)
	if err != nil {
		return Instance{}, fmt.Errorf("stats build: %s: grab_success: %w", instance, err)
	}
	torrents, err := uc.repo.TorrentTotals(ctx, instance)
	if err != nil {
		return Instance{}, fmt.Errorf("stats build: %s: torrent_totals: %w", instance, err)
	}

	return Instance{
		InstanceName:  instance,
		Totals:        Totals(totals),
		ByGenre:       toBuckets(byGenre),
		ByNetwork:     toBuckets(byNetwork),
		GrabSuccess:   toGrabSuccess(grabs),
		TorrentTotals: TorrentTotals(torrents),
	}, nil
}

func toGrabSuccess(c ports.StatsGrabCounts) GrabSuccess {
	denom := c.Imported + c.Failed
	var rate float64
	if denom > 0 {
		rate = float64(c.Imported) / float64(denom)
	}
	return GrabSuccess{
		Grabbed:     c.Grabbed,
		Imported:    c.Imported,
		Failed:      c.Failed,
		SuccessRate: rate,
	}
}

func toBuckets(in []ports.StatsKindBucket) []KindBucket {
	out := make([]KindBucket, 0, len(in))
	for _, b := range in {
		out = append(out, KindBucket(b))
	}
	return out
}
