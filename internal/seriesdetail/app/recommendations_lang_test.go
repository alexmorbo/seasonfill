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
	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// recFakeTextsBatch implements SeriesTextsPort. Per-id map lets tests
// seed which recs have a localised title and which fall through to
// canon.
type recFakeTextsBatch struct {
	single series.SeriesText
	batch  map[domain.SeriesID]series.SeriesText
	err    error
}

func (f *recFakeTextsBatch) GetWithFallback(_ context.Context, id domain.SeriesID, _ string) (series.SeriesText, error) {
	if t, ok := f.batch[id]; ok {
		return t, nil
	}
	return f.single, nil
}

func (f *recFakeTextsBatch) ListByIDsWithFallback(_ context.Context, ids []domain.SeriesID, _ string) (map[domain.SeriesID]series.SeriesText, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[domain.SeriesID]series.SeriesText, len(ids))
	for _, id := range ids {
		if t, ok := f.batch[id]; ok {
			out[id] = t
		}
	}
	return out, nil
}

// recFakeMediaBatch implements SeriesMediaTextsPort (Story 584b). Per-id
// map lets tests seed which recs have a per-language poster row and which
// fall through to their canon poster.
type recFakeMediaBatch struct {
	batch map[domain.SeriesID]series.SeriesMediaText
	err   error
}

func (f *recFakeMediaBatch) Get(context.Context, domain.SeriesID, string) (series.SeriesMediaText, error) {
	return series.SeriesMediaText{}, nil
}

func (f *recFakeMediaBatch) GetWithFallback(context.Context, domain.SeriesID, string) (series.SeriesMediaText, error) {
	return series.SeriesMediaText{}, nil
}

func (f *recFakeMediaBatch) ListByIDsWithFallback(_ context.Context, ids []domain.SeriesID, _ string) (map[domain.SeriesID]series.SeriesMediaText, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[domain.SeriesID]series.SeriesMediaText, len(ids))
	for _, id := range ids {
		if m, ok := f.batch[id]; ok {
			out[id] = m
		}
	}
	return out, nil
}

func (f *recFakeMediaBatch) GetBackdropAnyLang(context.Context, domain.SeriesID, string) (*string, error) {
	return nil, nil
}

func (f *recFakeMediaBatch) GetPosterAnyLang(context.Context, domain.SeriesID, string) (*string, error) {
	return nil, nil
}

// TestComposerGetRecommendations_LangLocalisesPresentTitles pins the
// bug the operator surfaced live: series_texts.ru-RU exists but the
// wire still emitted the EN canon.Title. Two recs — one with a
// ru-RU row, one without. Verifies the localised title wins where
// present + canon holds where the row is absent.
func TestComposerGetRecommendations_LangLocalisesPresentTitles(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42, OriginalTitle: new("Source")},
		10: {ID: 10, OriginalTitle: new("ER")},      // has ru-RU
		20: {ID: 20, OriginalTitle: new("Firefly")}, // no ru-RU row
	}
	recs := recFakeRecs{ids: []domain.SeriesID{10, 20}}
	texts := &recFakeTextsBatch{
		batch: map[domain.SeriesID]series.SeriesText{
			10: {SeriesID: 10, Language: "ru-RU", Title: new("Скорая помощь")},
		},
	}
	c := NewComposer(Deps{
		SeriesCache:     &ovFakeCache{entries: cache},
		Series:          &ovFakeSeries{rows: canonByID},
		SeriesTexts:     texts,
		Recommendations: recs,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:             func() time.Time { return time.Now().UTC() },
	})

	out, err := c.GetRecommendations(t.Context(), "alpha", 1, "ru-RU", 20, 0)
	require.NoError(t, err)
	require.Equal(t, 2, len(out.Items))
	require.Equal(t, "Скорая помощь", out.Items[0].Title, "localised title wins when ru-RU row present")
	require.Equal(t, "Firefly", out.Items[1].Title, "canon title held when no localised row")
}

// TestComposerGetRecommendations_LangLocalisesPresentPosters pins the
// Story 584b read path: two recs, one with a ru-RU poster row and one
// without. The localised rec's resolved poster derives from /ru.jpg while the
// other resolves to nil — S-E3a removed the canon poster fallback, so a rec
// with no series_media_texts row renders the FE monogram.
func TestComposerGetRecommendations_LangLocalisesPresentPosters(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42, OriginalTitle: new("Source")},
		10: {ID: 10, OriginalTitle: new("Rec A")}, // has ru-RU poster
		20: {ID: 20, OriginalTitle: new("Rec B")}, // no per-lang poster
	}
	recs := recFakeRecs{ids: []domain.SeriesID{10, 20}}
	mediaTexts := &recFakeMediaBatch{
		batch: map[domain.SeriesID]series.SeriesMediaText{
			10: {SeriesID: 10, Language: "ru-RU", PosterAsset: new("/ru.jpg")},
		},
	}
	resolver := skEagerResolver()
	resolver.SetUnifiedResolve(true) // Resolve mints eager hash on miss
	c := NewComposer(Deps{
		SeriesCache:      &ovFakeCache{entries: cache},
		Series:           &ovFakeSeries{rows: canonByID},
		Recommendations:  recs,
		SeriesMediaTexts: mediaTexts,
		MediaResolver:    resolver,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:              func() time.Time { return time.Now().UTC() },
	})

	out, err := c.GetRecommendations(t.Context(), "alpha", 1, "ru-RU", 20, 0)
	require.NoError(t, err)
	require.Equal(t, 2, len(out.Items))
	require.NotNil(t, out.Items[0].PosterAsset)
	require.Equal(t, skEagerHash("/ru.jpg", "w342"), *out.Items[0].PosterAsset,
		"localised poster wins when ru-RU row present")
	// S-E3a — no per-lang row → nil raw path; the unified-resolve resolver mints
	// the missing-sentinel hash (FE renders the monogram). No canon fallback.
	require.NotNil(t, out.Items[1].PosterAsset)
	require.Equal(t, appmedia.SentinelMissingHash, *out.Items[1].PosterAsset,
		"no per-lang row → sentinel-missing hash (canon poster fallback removed in S-E3a)")
}

// TestComposerGetRecommendations_MediaNilDepYieldsNilPoster — no media port
// wired: S-E3a removed the canon poster fallback, so every rec poster resolves
// to nil (FE monogram) rather than a canon path.
func TestComposerGetRecommendations_MediaNilDepYieldsNilPoster(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42, OriginalTitle: new("Source")},
		10: {ID: 10, OriginalTitle: new("Rec A")},
	}
	recs := recFakeRecs{ids: []domain.SeriesID{10}}
	resolver := skEagerResolver()
	resolver.SetUnifiedResolve(true)
	c := NewComposer(Deps{
		SeriesCache:     &ovFakeCache{entries: cache},
		Series:          &ovFakeSeries{rows: canonByID},
		Recommendations: recs,
		// SeriesMediaTexts intentionally nil — no poster source → nil.
		MediaResolver: resolver,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           func() time.Time { return time.Now().UTC() },
	})

	out, err := c.GetRecommendations(t.Context(), "alpha", 1, "ru-RU", 20, 0)
	require.NoError(t, err)
	require.Equal(t, 1, len(out.Items))
	require.NotNil(t, out.Items[0].PosterAsset)
	require.Equal(t, appmedia.SentinelMissingHash, *out.Items[0].PosterAsset,
		"nil media dep → sentinel-missing hash (no canon fallback)")
}

// TestComposerGetRecommendations_MediaBatchFailureDegradesQuiet — the
// per-lang poster batch load failing must NOT 5xx the recs endpoint and
// must keep canon posters (warn-logged, response not degraded).
func TestComposerGetRecommendations_MediaBatchFailureDegradesQuiet(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42, OriginalTitle: new("Source")},
		10: {ID: 10, OriginalTitle: new("Rec A")},
	}
	recs := recFakeRecs{ids: []domain.SeriesID{10}}
	mediaTexts := &recFakeMediaBatch{err: errors.New("db down")} //nolint:err113
	resolver := skEagerResolver()
	resolver.SetUnifiedResolve(true)
	c := NewComposer(Deps{
		SeriesCache:      &ovFakeCache{entries: cache},
		Series:           &ovFakeSeries{rows: canonByID},
		Recommendations:  recs,
		SeriesMediaTexts: mediaTexts,
		MediaResolver:    resolver,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:              func() time.Time { return time.Now().UTC() },
	})

	out, err := c.GetRecommendations(t.Context(), "alpha", 1, "ru-RU", 20, 0)
	require.NoError(t, err, "media batch load failure must NOT 5xx the recs endpoint")
	require.Equal(t, 1, len(out.Items))
	require.NotNil(t, out.Items[0].PosterAsset)
	require.Equal(t, appmedia.SentinelMissingHash, *out.Items[0].PosterAsset,
		"media batch load failure degrades to sentinel-missing hash (no canon fallback in S-E3a)")
}

// TestComposerGetRecommendations_LangDefaultsToEnUS pins that empty
// / whitespace / oversize lang normalises to en-US via resolveLang.
// Behaviour proof: no localisation attempted (SeriesTexts nil-safe),
// canon titles pass through untouched.
func TestComposerGetRecommendations_LangDefaultsToEnUS(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42, OriginalTitle: new("Source")},
		10: {ID: 10, OriginalTitle: new("Rec A")},
	}
	recs := recFakeRecs{ids: []domain.SeriesID{10}}
	// No SeriesTexts wired — verifies nil-safe branch (no crash) and
	// no title override.
	c := newRecComposer(canonByID, cache, recs, &recFakeCacheLookup{})

	for _, lang := range []string{"", "   ", "this-is-way-too-long-of-a-language-tag-blah-blah"} {
		out, err := c.GetRecommendations(t.Context(), "alpha", 1, lang, 20, 0)
		require.NoError(t, err)
		require.Equal(t, 1, len(out.Items))
		require.Equal(t, "Rec A", out.Items[0].Title, "canon title held for lang=%q", lang)
	}
}

// TestComposerGetRecommendations_TextsBatchFailureDegradesQuiet — the
// series_texts batch load failing must NOT 500 the recs endpoint. Canon
// titles win + warn log fires (log inspection out of scope for unit).
func TestComposerGetRecommendations_TextsBatchFailureDegradesQuiet(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42, OriginalTitle: new("Source")},
		10: {ID: 10, OriginalTitle: new("Rec A")},
	}
	recs := recFakeRecs{ids: []domain.SeriesID{10}}
	texts := &recFakeTextsBatch{err: errors.New("db down")} //nolint:err113

	c := NewComposer(Deps{
		SeriesCache:     &ovFakeCache{entries: cache},
		Series:          &ovFakeSeries{rows: canonByID},
		SeriesTexts:     texts,
		Recommendations: recs,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:             func() time.Time { return time.Now().UTC() },
	})

	out, err := c.GetRecommendations(t.Context(), "alpha", 1, "ru-RU", 20, 0)
	require.NoError(t, err, "texts batch load failure must NOT 5xx the recs endpoint")
	require.Equal(t, 1, len(out.Items))
	require.Equal(t, "Rec A", out.Items[0].Title, "canon title held when texts load failed")
}

// TestComposerGetRecommendations_LangEmptyTitleFallsBackToCanon —
// series_texts row exists but Title is nil / empty (Sonarr stub
// upsert path leaves it nil). Composer must keep canon.Title in
// that case, not blank the recommendation.
func TestComposerGetRecommendations_LangEmptyTitleFallsBackToCanon(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42, OriginalTitle: new("Source")},
		10: {ID: 10, OriginalTitle: new("Rec Canon")},
		20: {ID: 20, OriginalTitle: new("Rec Canon 2")},
	}
	recs := recFakeRecs{ids: []domain.SeriesID{10, 20}}
	texts := &recFakeTextsBatch{
		batch: map[domain.SeriesID]series.SeriesText{
			10: {SeriesID: 10, Language: "ru-RU", Title: nil},     // nil title
			20: {SeriesID: 20, Language: "ru-RU", Title: new("")}, // empty title
		},
	}
	c := NewComposer(Deps{
		SeriesCache:     &ovFakeCache{entries: cache},
		Series:          &ovFakeSeries{rows: canonByID},
		SeriesTexts:     texts,
		Recommendations: recs,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:             func() time.Time { return time.Now().UTC() },
	})

	out, err := c.GetRecommendations(t.Context(), "alpha", 1, "ru-RU", 20, 0)
	require.NoError(t, err)
	require.Equal(t, 2, len(out.Items))
	require.Equal(t, "Rec Canon", out.Items[0].Title, "nil localised title must not blank canon")
	require.Equal(t, "Rec Canon 2", out.Items[1].Title, "empty localised title must not blank canon")
}

// W15-9 — served-language contract on the recommendations list.
func TestComposerGetRecommendations_ServedLanguage(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canonByID := map[domain.SeriesID]series.Canon{
		42: {ID: 42, OriginalTitle: new("Source")},
		10: {ID: 10, OriginalTitle: new("Rec A")},
		20: {ID: 20, OriginalTitle: new("Rec B")},
	}
	newComposer := func(texts SeriesTextsPort, ids ...domain.SeriesID) *Composer {
		return NewComposer(Deps{
			SeriesCache:     &ovFakeCache{entries: cache},
			Series:          &ovFakeSeries{rows: canonByID},
			SeriesTexts:     texts,
			Recommendations: recFakeRecs{ids: ids},
			Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
			Now:             func() time.Time { return time.Now().UTC() },
		})
	}

	t.Run("all rec titles in requested lang → served=requested", func(t *testing.T) {
		t.Parallel()
		texts := &recFakeTextsBatch{batch: map[domain.SeriesID]series.SeriesText{
			10: {SeriesID: 10, Language: "ru-RU", Title: new("Рек А")},
			20: {SeriesID: 20, Language: "ru-RU", Title: new("Рек Б")},
		}}
		out, err := newComposer(texts, 10, 20).GetRecommendations(t.Context(), "alpha", 1, "ru-RU", 20, 0)
		require.NoError(t, err)
		require.Equal(t, "ru-RU", out.ServedLanguage)
	})

	t.Run("a fallback rec title → first-seen fallback lang", func(t *testing.T) {
		t.Parallel()
		texts := &recFakeTextsBatch{batch: map[domain.SeriesID]series.SeriesText{
			10: {SeriesID: 10, Language: "ru-RU", Title: new("Рек А")},
			20: {SeriesID: 20, Language: "en-US", Title: new("Rec B")},
		}}
		out, err := newComposer(texts, 10, 20).GetRecommendations(t.Context(), "alpha", 1, "ru-RU", 20, 0)
		require.NoError(t, err)
		require.Equal(t, "en-US", out.ServedLanguage)
	})

	t.Run("no localised titles → served empty", func(t *testing.T) {
		t.Parallel()
		out, err := newComposer(nil, 10).GetRecommendations(t.Context(), "alpha", 1, "ru-RU", 20, 0)
		require.NoError(t, err)
		require.Empty(t, out.ServedLanguage)
	})
}
