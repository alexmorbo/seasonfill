package seriesdetail

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Reuses ovFakeCache, ovFakeSeries, i64ptrOV from overview_test.go
// (same package).

type recFakeRecs struct {
	ids []domain.SeriesID
	err error
}

func (f recFakeRecs) ListBySeries(_ context.Context, _ domain.SeriesID) ([]domain.SeriesID, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ids, nil
}

type recFakeCacheLookup struct {
	rows map[domain.SeriesID][]series.CacheEntry
}

func (f *recFakeCacheLookup) ListBySeriesID(_ context.Context, id domain.SeriesID) ([]series.CacheEntry, error) {
	return f.rows[id], nil
}

func (f *recFakeCacheLookup) ListBySeriesIDs(_ context.Context, ids []domain.SeriesID) (map[domain.SeriesID][]series.CacheEntry, error) {
	out := make(map[domain.SeriesID][]series.CacheEntry, len(ids))
	for _, id := range ids {
		if rows, ok := f.rows[id]; ok && len(rows) > 0 {
			out[id] = rows
		}
	}
	return out, nil
}

func newRecComposer(
	canonByID map[domain.SeriesID]series.Canon,
	cache map[string]series.CacheEntry,
	recs RecommendationsPort,
	lookup SeriesCacheLookupPort,
) *Composer {
	return NewComposer(Deps{
		SeriesCache:       &ovFakeCache{entries: cache},
		SeriesCacheLookup: lookup,
		Series:            &ovFakeSeries{rows: canonByID},
		Recommendations:   recs,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:               func() time.Time { return time.Now().UTC() },
	})
}

func TestComposerGetRecommendations_HappyPath(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42, Title: "Source"},
		10: {ID: 10, Title: "Rec A"},
		20: {ID: 20, Title: "Rec B"},
	}
	recs := recFakeRecs{ids: []domain.SeriesID{10, 20}}
	lookup := &recFakeCacheLookup{rows: map[domain.SeriesID][]series.CacheEntry{
		10: {{InstanceName: "beta", SonarrSeriesID: 99}},
	}}
	c := newRecComposer(canonByID, cache, recs, lookup)

	out, err := c.GetRecommendations(t.Context(), "alpha", 1, 20, 0)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, 2, out.TotalCount)
	require.Equal(t, 2, len(out.Items))
	require.False(t, out.HasMore)
	require.Equal(t, "Rec A", out.Items[0].Series.Title)
	require.True(t, out.Items[0].InLibrary)
	require.Equal(t, domain.InstanceName("beta"), out.Items[0].InstanceName)
	require.False(t, out.Items[1].InLibrary)
}

func TestComposerGetRecommendations_Pagination_HasMore(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42}, 10: {ID: 10}, 20: {ID: 20}, 30: {ID: 30}, 40: {ID: 40},
	}
	recs := recFakeRecs{ids: []domain.SeriesID{10, 20, 30, 40}}
	c := newRecComposer(canonByID, cache, recs, &recFakeCacheLookup{})

	// Page 1: limit=2 offset=0 → 2 items, has_more=true.
	out, err := c.GetRecommendations(t.Context(), "alpha", 1, 2, 0)
	require.NoError(t, err)
	require.Equal(t, 4, out.TotalCount)
	require.Equal(t, 2, len(out.Items))
	require.True(t, out.HasMore)
	require.Equal(t, domain.SeriesID(10), out.Items[0].Series.ID)

	// Page 2: limit=2 offset=2 → 2 items, has_more=false.
	out, err = c.GetRecommendations(t.Context(), "alpha", 1, 2, 2)
	require.NoError(t, err)
	require.Equal(t, 2, len(out.Items))
	require.False(t, out.HasMore)
	require.Equal(t, domain.SeriesID(30), out.Items[0].Series.ID)

	// Past end: offset >= total → empty items, has_more=false.
	out, err = c.GetRecommendations(t.Context(), "alpha", 1, 2, 99)
	require.NoError(t, err)
	require.Equal(t, 0, len(out.Items))
	require.False(t, out.HasMore)
}

func TestComposerGetRecommendations_LimitClamp(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canonByID := map[domain.SeriesID]series.Canon{42: {ID: 42}}
	c := newRecComposer(canonByID, cache, recFakeRecs{}, &recFakeCacheLookup{})

	// limit=0 → default. offset=-5 → 0.
	out, err := c.GetRecommendations(t.Context(), "alpha", 1, 0, -5)
	require.NoError(t, err)
	require.NotNil(t, out)
	// limit=999 → clamped to 50. (no items here, just exercises the path)
	out, err = c.GetRecommendations(t.Context(), "alpha", 1, 999, 0)
	require.NoError(t, err)
	require.NotNil(t, out)
}

func TestComposerGetRecommendations_NoCacheRow(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: nil},
	}
	c := newRecComposer(map[domain.SeriesID]series.Canon{}, cache, recFakeRecs{}, &recFakeCacheLookup{})

	out, err := c.GetRecommendations(t.Context(), "alpha", 1, 20, 0)
	require.Nil(t, out)
	require.Error(t, err)
	require.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestComposerGetRecommendations_ListFailureDegrades(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	c := newRecComposer(
		map[domain.SeriesID]series.Canon{42: {ID: 42}},
		cache,
		recFakeRecs{err: errors.New("tmdb down")}, //nolint:err113
		&recFakeCacheLookup{},
	)

	out, err := c.GetRecommendations(t.Context(), "alpha", 1, 20, 0)
	require.NoError(t, err, "list failure must NOT fail the response")
	require.NotNil(t, out)
	require.Equal(t, 0, len(out.Items))
	require.Equal(t, 0, out.TotalCount)
	require.Contains(t, out.Degraded, "tmdb_series")
}

func TestComposerGetRecommendations_StubSkipped(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42},
		10: {ID: 10, Title: "Resolved"},
		// 20 missing → stub-skip.
	}
	recs := recFakeRecs{ids: []domain.SeriesID{10, 20}}
	c := newRecComposer(canonByID, cache, recs, &recFakeCacheLookup{})

	out, err := c.GetRecommendations(t.Context(), "alpha", 1, 20, 0)
	require.NoError(t, err)
	require.Equal(t, 1, out.TotalCount, "stub-only rows must be dropped from TotalCount")
	require.Equal(t, 1, len(out.Items))
}

// TestComposerGetRecommendations_BatchOrderPreserved pins the
// observable contract that Story 551's batch path holds the same
// guarantees as the pre-batch shape: input id-order is preserved on
// the wire even when the underlying ListByIDs returns rows in id ASC
// (which differs from recIDs ordering on most real series — TMDB
// returns by popularity, not id).
func TestComposerGetRecommendations_BatchOrderPreserved(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	// Recs returned in TMDB-popularity order (30, 10, 20), but the
	// underlying Series.ListByIDs sorts by id ASC (10, 20, 30) per
	// the in-repo convention. The composer MUST project back into the
	// recIDs sequence.
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42, Title: "Source"},
		10: {ID: 10, Title: "Rec-10"},
		20: {ID: 20, Title: "Rec-20"},
		30: {ID: 30, Title: "Rec-30"},
	}
	recs := recFakeRecs{ids: []domain.SeriesID{30, 10, 20}}
	c := newRecComposer(canonByID, cache, recs, &recFakeCacheLookup{})

	out, err := c.GetRecommendations(t.Context(), "alpha", 1, 20, 0)
	require.NoError(t, err)
	require.Equal(t, 3, len(out.Items))
	require.Equal(t, domain.SeriesID(30), out.Items[0].Series.ID,
		"input slice order preserved on the wire (NOT id ASC)")
	require.Equal(t, domain.SeriesID(10), out.Items[1].Series.ID)
	require.Equal(t, domain.SeriesID(20), out.Items[2].Series.ID)
}

// TestComposerGetRecommendations_BatchListFailureDegradesQuiet pins
// that a transient ListByIDs failure surfaces as an empty resolved
// slice + a warn log (not a 500), matching the silent-degrade
// semantics the prior per-row Get loop carried.
func TestComposerGetRecommendations_BatchListFailureDegradesQuiet(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	// rows table errs on every Get / ListByIDs path.
	failSeries := &ovFakeSeries{rows: map[domain.SeriesID]series.Canon{42: {ID: 42}}, err: errors.New("db down")} //nolint:err113
	c := NewComposer(Deps{
		SeriesCache:       &ovFakeCache{entries: cache},
		SeriesCacheLookup: &recFakeCacheLookup{},
		Series:            failSeries,
		Recommendations:   recFakeRecs{ids: []domain.SeriesID{10, 20}},
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:               func() time.Time { return time.Now().UTC() },
	})

	out, err := c.GetRecommendations(t.Context(), "alpha", 1, 20, 0)
	require.NoError(t, err, "DB failure on canon batch must NOT 5xx the recs endpoint")
	require.NotNil(t, out)
	require.Equal(t, 0, len(out.Items))
	require.Equal(t, 0, out.TotalCount)
}
