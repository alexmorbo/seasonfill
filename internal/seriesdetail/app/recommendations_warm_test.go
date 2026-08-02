package seriesdetail

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	mediaapp "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// --- fakes ---

// warmFakeLookup is a media.HashLookupPort where `stored` maps a source URL to
// its hash ONLY for warm (status='stored') blobs; anything absent is a store
// miss (ports.ErrNotFound), exactly like the production repo's status filter.
type warmFakeLookup struct{ stored map[string]string }

func (f *warmFakeLookup) HashForSourceURL(_ context.Context, url string) (string, error) {
	if h, ok := f.stored[url]; ok {
		return h, nil
	}
	return "", ports.ErrNotFound
}
func (f *warmFakeLookup) EnsurePending(context.Context, string, string, string) error { return nil }

// warmStubEnqueuer records the hot-lane (mediaproxy priority) enqueue URLs.
type warmStubEnqueuer struct {
	mu   sync.Mutex
	urls []string
}

func (e *warmStubEnqueuer) Enqueue(_ context.Context, reqs []mediaapp.EnqueueRequest) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range reqs {
		e.urls = append(e.urls, r.UpstreamURL)
	}
}
func (e *warmStubEnqueuer) got() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := append([]string(nil), e.urls...)
	return out
}

// warmFakeMediaTexts serves per-id poster raw paths (series_media_texts). Only
// ListByIDsWithFallback is exercised by the recs path; the rest satisfy the port.
type warmFakeMediaTexts struct{ posters map[domain.SeriesID]string }

func (f *warmFakeMediaTexts) ListByIDsWithFallback(
	_ context.Context, ids []domain.SeriesID, _ string,
) (map[domain.SeriesID]series.SeriesMediaText, error) {
	out := make(map[domain.SeriesID]series.SeriesMediaText, len(ids))
	for _, id := range ids {
		if p, ok := f.posters[id]; ok {
			pp := p
			out[id] = series.SeriesMediaText{SeriesID: id, PosterAsset: &pp}
		}
	}
	return out, nil
}
func (f *warmFakeMediaTexts) Get(context.Context, domain.SeriesID, string) (series.SeriesMediaText, error) {
	return series.SeriesMediaText{}, ports.ErrNotFound
}
func (f *warmFakeMediaTexts) GetWithFallback(context.Context, domain.SeriesID, string) (series.SeriesMediaText, error) {
	return series.SeriesMediaText{}, ports.ErrNotFound
}
func (f *warmFakeMediaTexts) GetBackdropAnyLang(context.Context, domain.SeriesID, string) (*string, error) {
	return nil, nil
}
func (f *warmFakeMediaTexts) GetPosterAnyLang(context.Context, domain.SeriesID, string) (*string, error) {
	return nil, nil
}

func warmURL(path string) string { return mediaapp.BuildTMDBImageURL("w342", path) }

// newWarmComposer wires the recs path with a real *media.Resolver (unified ON)
// backed by warmFakeLookup + the hot-lane stub, plus a series_media_texts fake.
func newWarmComposer(
	canonByID map[domain.SeriesID]series.Canon,
	cache map[string]series.CacheEntry,
	recs RecommendationsPort,
	posters map[domain.SeriesID]string,
	stored map[string]string,
	enq *warmStubEnqueuer,
) *Composer {
	res := media.NewResolver(&warmFakeLookup{stored: stored}, enq, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	res.SetUnifiedResolve(true)
	return NewComposer(Deps{
		SeriesCache:      &ovFakeCache{entries: cache},
		Series:           &ovFakeSeries{rows: canonByID},
		Recommendations:  recs,
		SeriesMediaTexts: &warmFakeMediaTexts{posters: posters},
		MediaResolver:    res,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:              func() time.Time { return time.Now().UTC() },
	})
}

func TestComposerGetRecommendations_WarmGate(t *testing.T) {
	sentinel := mediaapp.SentinelMissingHash
	const hashA = "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111"
	const hashB = "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222"

	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42, OriginalTitle: new("Source")},
		10: {ID: 10, OriginalTitle: new("Rec A")},
		20: {ID: 20, OriginalTitle: new("Rec B")},
	}
	recs := recFakeRecs{ids: []domain.SeriesID{10, 20}}
	// Rec 10 poster "/a.jpg", rec 20 poster "/b.jpg".
	posters := map[domain.SeriesID]string{10: "/a.jpg", 20: "/b.jpg"}

	t.Run("all_stored_real_hashes_not_degraded", func(t *testing.T) {
		enq := &warmStubEnqueuer{}
		stored := map[string]string{warmURL("/a.jpg"): hashA, warmURL("/b.jpg"): hashB}
		c := newWarmComposer(canonByID, cache, recs, posters, stored, enq)

		out, err := c.GetRecommendations(t.Context(), "alpha", 1, "", 20, 0)
		require.NoError(t, err)
		require.Len(t, out.Items, 2)
		require.NotNil(t, out.Items[0].PosterAsset)
		require.NotNil(t, out.Items[1].PosterAsset)
		assert.Equal(t, hashA, *out.Items[0].PosterAsset)
		assert.Equal(t, hashB, *out.Items[1].PosterAsset)
		assert.NotContains(t, out.Degraded, RecPosterColdDegradedTag)
		assert.Empty(t, enq.got(), "no hot-lane enqueue when all warm")
	})

	t.Run("none_stored_all_sentinel_degraded_enqueued_each", func(t *testing.T) {
		enq := &warmStubEnqueuer{}
		c := newWarmComposer(canonByID, cache, recs, posters, map[string]string{}, enq)

		out, err := c.GetRecommendations(t.Context(), "alpha", 1, "", 20, 0)
		require.NoError(t, err)
		require.Len(t, out.Items, 2)
		assert.Equal(t, sentinel, *out.Items[0].PosterAsset)
		assert.Equal(t, sentinel, *out.Items[1].PosterAsset)
		assert.Contains(t, out.Degraded, RecPosterColdDegradedTag)
		assert.ElementsMatch(t, []string{warmURL("/a.jpg"), warmURL("/b.jpg")}, enq.got())
	})

	t.Run("mixed_per_item_hash_sentinel_degraded", func(t *testing.T) {
		enq := &warmStubEnqueuer{}
		stored := map[string]string{warmURL("/a.jpg"): hashA} // only rec 10 warm
		c := newWarmComposer(canonByID, cache, recs, posters, stored, enq)

		out, err := c.GetRecommendations(t.Context(), "alpha", 1, "", 20, 0)
		require.NoError(t, err)
		require.Len(t, out.Items, 2)
		assert.Equal(t, hashA, *out.Items[0].PosterAsset)
		assert.Equal(t, sentinel, *out.Items[1].PosterAsset)
		assert.Contains(t, out.Degraded, RecPosterColdDegradedTag)
		assert.Equal(t, []string{warmURL("/b.jpg")}, enq.got(), "only the cold rec warms on hot lane")
	})

	t.Run("all_warm_with_no_poster_rec_not_degraded_no_regression", func(t *testing.T) {
		// Rec 10 warm; rec 20 has NO series_media_texts poster row → terminal
		// sentinel (nothing to warm) → must NOT degrade + must NOT enqueue.
		enq := &warmStubEnqueuer{}
		stored := map[string]string{warmURL("/a.jpg"): hashA}
		postersNoRow := map[domain.SeriesID]string{10: "/a.jpg"} // 20 absent
		c := newWarmComposer(canonByID, cache, recs, postersNoRow, stored, enq)

		out, err := c.GetRecommendations(t.Context(), "alpha", 1, "", 20, 0)
		require.NoError(t, err)
		require.Len(t, out.Items, 2)
		assert.Equal(t, hashA, *out.Items[0].PosterAsset)
		assert.Equal(t, sentinel, *out.Items[1].PosterAsset)
		assert.NotContains(t, out.Degraded, RecPosterColdDegradedTag,
			"a rec with no poster is terminal-monogram, not a warming item")
		assert.Empty(t, enq.got(), "no hot-lane enqueue for a no-poster rec")
	})
}
