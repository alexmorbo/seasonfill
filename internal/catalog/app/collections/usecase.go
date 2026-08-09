// Package collections assembles the read-only "curated collections" report
// backing GET /api/v1/insights/collections. It fans out across every instance
// with a live series_cache row (or the single ?instance= filter) and, per
// instance, evaluates a STATIC in-code list of curated collections — each a set
// of TMDB keyword ids grouped under a slug + Russian-facing title + is_franchise
// flag. A collection with 0 owned series is HIDDEN; the rest are ordered by
// owned_count DESC. All DB work lives behind the narrow CollectionsRepository
// port. To add/edit a collection, edit curatedCollections below — no migration.
package collections

import (
	"context"
	"fmt"
	"sort"
	"time"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// collectionSeriesLimit bounds each collection's series slice. The OwnedCount is
// the EXACT matching total (from the repo), independent of this cap.
const collectionSeriesLimit = 50

// collectionSeed is one curated collection definition. Slug is stable (FE
// localizes by it); Title is a machine-stable label fallback; KeywordTMDBIDs is
// the union set a series must match ANY of; IsFranchise marks true-franchise
// showcases (vs thematic buckets).
type collectionSeed struct {
	Slug           string
	Title          string
	KeywordTMDBIDs []int64
	IsFranchise    bool
}

// curatedCollections — the STATIC, in-code, editable seed. Verified against the
// live homelab library (see story I-5). Order here is not significant; the
// report is re-sorted by owned_count DESC. Collections may overlap by design
// (e.g. MCU ⊂ Кинокомиксы).
var curatedCollections = []collectionSeed{
	{Slug: "books", Title: "Based on books", KeywordTMDBIDs: []int64{818}},
	{Slug: "true_crime", Title: "True crime", KeywordTMDBIDs: []int64{9826, 703, 12565}},
	{Slug: "lgbt", Title: "LGBT", KeywordTMDBIDs: []int64{158718, 258533}},
	{Slug: "miniseries", Title: "Miniseries", KeywordTMDBIDs: []int64{11162}},
	{Slug: "scifi_alt", Title: "Sci-fi & alt-history", KeywordTMDBIDs: []int64{12026, 4565, 9951}},
	{Slug: "true_story", Title: "Based on a true story", KeywordTMDBIDs: []int64{9672}},
	{Slug: "period", Title: "Period drama", KeywordTMDBIDs: []int64{15060}},
	{Slug: "comics", Title: "Comic-book", KeywordTMDBIDs: []int64{9715, 9717, 180547, 155030}},
	{Slug: "dark_comedy", Title: "Dark comedy", KeywordTMDBIDs: []int64{10123}},
	{Slug: "anthology", Title: "Anthology", KeywordTMDBIDs: []int64{9706}},
	{Slug: "mcu", Title: "MCU", KeywordTMDBIDs: []int64{180547}, IsFranchise: true},
}

// CollectionSeries is one owned series in a collection.
type CollectionSeries struct {
	SeriesID domain.SeriesID
	SonarrID domain.SonarrSeriesID
	Title    string
}

// Collection is one curated bucket with its exact owned total + bounded series.
type Collection struct {
	Slug        string
	Title       string
	OwnedCount  int
	IsFranchise bool
	Series      []CollectionSeries
}

// Instance is the per-instance collection set (0-owned collections omitted).
type Instance struct {
	InstanceName string
	Collections  []Collection
}

// Report is the assembled collections report.
type Report struct {
	GeneratedAt time.Time
	Instances   []Instance
}

// UseCase builds the Report from the CollectionsRepository port.
type UseCase struct {
	repo  ports.CollectionsRepository
	clock func() time.Time
}

// NewUseCase wires the read-only collections usecase. Clock defaults to
// time.Now().UTC.
func NewUseCase(repo ports.CollectionsRepository) *UseCase {
	return &UseCase{
		repo:  repo,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

// WithClock swaps the clock for deterministic tests (only affects generated_at).
func (uc *UseCase) WithClock(clock func() time.Time) *UseCase {
	uc.clock = clock
	return uc
}

// Build assembles the report. A non-empty instanceFilter scopes to that single
// instance; an empty filter fans out across every instance with a live
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
			return Report{}, fmt.Errorf("collections build: list instances: %w", err)
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
	cols := make([]Collection, 0, len(curatedCollections))
	for _, seed := range curatedCollections {
		res, err := uc.repo.Collection(ctx, instance, seed.KeywordTMDBIDs, collectionSeriesLimit)
		if err != nil {
			return Instance{}, fmt.Errorf("collections build: %s: %s: %w", instance, seed.Slug, err)
		}
		if res.OwnedCount == 0 {
			continue // HIDE empty collections
		}
		cols = append(cols, Collection{
			Slug:        seed.Slug,
			Title:       seed.Title,
			OwnedCount:  res.OwnedCount,
			IsFranchise: seed.IsFranchise,
			Series:      mapSeries(res.Series),
		})
	}

	// Order by owned_count DESC, slug ASC tiebreak (deterministic).
	sort.SliceStable(cols, func(i, j int) bool {
		if cols[i].OwnedCount != cols[j].OwnedCount {
			return cols[i].OwnedCount > cols[j].OwnedCount
		}
		return cols[i].Slug < cols[j].Slug
	})

	return Instance{InstanceName: instance, Collections: cols}, nil
}

func mapSeries(rows []ports.CollectionSeriesRow) []CollectionSeries {
	out := make([]CollectionSeries, 0, len(rows))
	for _, r := range rows {
		out = append(out, CollectionSeries{SeriesID: r.SeriesID, SonarrID: r.SonarrID, Title: r.Title})
	}
	return out
}
