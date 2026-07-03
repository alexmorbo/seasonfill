package seriesdetail_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// discardLogger returns a logger that discards everything — used by the
// Story 532 tests to keep verbose tmdb_fallback_* domain output out of
// `go test -v` output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeMapSeriesReader satisfies seriesdetail.SeriesPort (Get +
// GetByTMDBID). Unlike stubSeriesReader which returns a single canon
// row, this one supports a map for the Recommendations test that needs
// to resolve multiple canon ids. err takes precedence over rows.
type fakeMapSeriesReader struct {
	rows map[domain.SeriesID]series.Canon
	err  error
}

func (f *fakeMapSeriesReader) Get(_ context.Context, id domain.SeriesID) (series.Canon, error) {
	if f.err != nil {
		return series.Canon{}, f.err
	}
	c, ok := f.rows[id]
	if !ok {
		return series.Canon{}, ports.ErrNotFound
	}
	return c, nil
}

func (f *fakeMapSeriesReader) GetByTMDBID(_ context.Context, _ domain.TMDBID) (series.Canon, error) {
	return series.Canon{}, ports.ErrNotFound
}

func (f *fakeMapSeriesReader) ListByIDs(_ context.Context, ids []domain.SeriesID) ([]series.Canon, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]series.Canon, 0, len(ids))
	for _, id := range ids {
		if c, ok := f.rows[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeMapSeriesReader) ListByTMDBIDs(_ context.Context, tmdbIDs []domain.TMDBID) ([]series.Canon, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]series.Canon, 0, len(tmdbIDs))
	for _, id := range tmdbIDs {
		for _, c := range f.rows {
			if c.TMDBID != nil && *c.TMDBID == id {
				out = append(out, c)
				break
			}
		}
	}
	return out, nil
}

// fakeFallbackTexts satisfies seriesdetail.SeriesTextsPort.
type fakeFallbackTexts struct {
	out   series.SeriesText
	batch map[domain.SeriesID]series.SeriesText
	err   error
}

func (f *fakeFallbackTexts) GetWithFallback(_ context.Context, _ domain.SeriesID, _ string) (series.SeriesText, error) {
	if f.err != nil {
		return series.SeriesText{}, f.err
	}
	return f.out, nil
}

// ListByIDsWithFallback — Story 565 (B-recs-lang). Returns per-id map
// when `batch` seeded; empty map otherwise.
func (f *fakeFallbackTexts) ListByIDsWithFallback(_ context.Context, ids []domain.SeriesID, _ string) (map[domain.SeriesID]series.SeriesText, error) {
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

// fakeFallbackMedia satisfies seriesdetail.SeriesMediaTextsPort. S-E3a — the
// TMDB-only cast/rec hero poster raw path comes from series_media_texts (canon
// no longer carries poster_asset).
type fakeFallbackMedia struct {
	out series.SeriesMediaText
	err error
}

func (f *fakeFallbackMedia) GetWithFallback(_ context.Context, _ domain.SeriesID, _ string) (series.SeriesMediaText, error) {
	if f.err != nil {
		return series.SeriesMediaText{}, f.err
	}
	return f.out, nil
}

func (f *fakeFallbackMedia) ListByIDsWithFallback(_ context.Context, _ []domain.SeriesID, _ string) (map[domain.SeriesID]series.SeriesMediaText, error) {
	if f.err != nil {
		return nil, f.err
	}
	return map[domain.SeriesID]series.SeriesMediaText{}, nil
}

// fakeFallbackKeywords satisfies seriesdetail.KeywordsPort.
type fakeFallbackKeywords struct {
	ids      []int64
	byID     map[int64]taxonomy.Keyword
	err      error
	batchErr error
}

func (f *fakeFallbackKeywords) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ids, nil
}

func (f *fakeFallbackKeywords) Get(_ context.Context, id int64, lang string) (taxonomy.Keyword, error) {
	if k, ok := f.byID[id]; ok {
		return k, nil
	}
	return taxonomy.Keyword{ID: id, Name: "kw", Language: lang}, nil
}

func (f *fakeFallbackKeywords) ListByIDsWithFallback(_ context.Context, ids []int64, lang string) ([]taxonomy.Keyword, error) {
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	out := make([]taxonomy.Keyword, 0, len(ids))
	for _, id := range ids {
		if k, ok := f.byID[id]; ok {
			out = append(out, k)
			continue
		}
		out = append(out, taxonomy.Keyword{ID: id, Name: "kw", Language: lang})
	}
	return out, nil
}

// fakeFallbackRecsPort satisfies seriesdetail.RecommendationsPort.
type fakeFallbackRecsPort struct {
	ids []domain.SeriesID
	err error
}

func (f *fakeFallbackRecsPort) ListBySeries(_ context.Context, _ domain.SeriesID) ([]domain.SeriesID, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ids, nil
}

// stubSeriesReader satisfies seriesdetail.SeriesPort (Get +
// GetByTMDBID). The fallback only uses Get; GetByTMDBID returns
// not-found to keep the surface complete.
type stubSeriesReader struct {
	canon series.Canon
	err   error
}

func (s *stubSeriesReader) Get(_ context.Context, _ domain.SeriesID) (series.Canon, error) {
	if s.err != nil {
		return series.Canon{}, s.err
	}
	return s.canon, nil
}

func (s *stubSeriesReader) GetByTMDBID(_ context.Context, _ domain.TMDBID) (series.Canon, error) {
	return series.Canon{}, ports.ErrNotFound
}

func (s *stubSeriesReader) ListByIDs(_ context.Context, _ []domain.SeriesID) ([]series.Canon, error) {
	if s.err != nil {
		return nil, s.err
	}
	// stubSeriesReader carries a single canon row — return it if requested,
	// otherwise an empty slice. Mirrors the single-row Get contract.
	return []series.Canon{s.canon}, nil
}

func (s *stubSeriesReader) ListByTMDBIDs(_ context.Context, _ []domain.TMDBID) ([]series.Canon, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []series.Canon{s.canon}, nil
}

func TestNewTMDBFallbackUseCase_NilSeriesReturnsError(t *testing.T) {
	_, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{})
	require.Error(t, err)
}

// ─── Story 528: on-demand enrichment trigger ──────────────────────

// ─── Story 532: GetOverview + GetRecommendations canon-only siblings ──

func TestTMDBFallbackUseCase_GetOverview_StubReturnsCanonOnly(t *testing.T) {
	t.Parallel()
	awards := "Won 2 Emmys"
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			8378: {ID: 8378, Hydration: series.HydrationStub, OMDBAwards: &awards},
		}},
		SeriesTexts: &fakeFallbackTexts{out: func() series.SeriesText {
			desc := "Краткое описание"
			return series.SeriesText{Overview: &desc, Language: "ru-RU"}
		}()},
		Keywords: &fakeFallbackKeywords{
			ids: []int64{1, 2},
			byID: map[int64]taxonomy.Keyword{
				1: {ID: 1, Name: "comedy", Language: "ru-RU"},
				2: {ID: 2, Name: "sci-fi", Language: "ru-RU"},
			},
		},
		Logger: discardLogger(),
	})
	require.NoError(t, err)
	ov, err := uc.GetOverview(t.Context(), 8378, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, domain.SeriesID(8378), ov.SeriesID)
	assert.Equal(t, domain.InstanceName(""), ov.Instance)
	assert.Equal(t, "Краткое описание", ov.Description)
	assert.Equal(t, "ru-RU", ov.DescriptionLanguage)
	assert.Len(t, ov.Keywords, 2)
	require.NotNil(t, ov.Awards)
	assert.Equal(t, "Won 2 Emmys", *ov.Awards)
	assert.Equal(t, []string{"tmdb_series"}, ov.Degraded)
}

// Story 552 (E-1 Z3) — batched keyword fetch failure must degrade with
// tmdb_series mark, NOT fail the response. Mirrors the overview test in
// overview_test.go.
func TestTMDBFallbackUseCase_GetOverview_KeywordsBatchFailureMarksDegraded(t *testing.T) {
	t.Parallel()
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			8378: {ID: 8378, Hydration: series.HydrationStub},
		}},
		Keywords: &fakeFallbackKeywords{
			ids:      []int64{1, 2, 3},
			batchErr: errors.New("batch boom"), //nolint:err113
		},
		Logger: discardLogger(),
	})
	require.NoError(t, err)
	ov, err := uc.GetOverview(t.Context(), 8378, "ru-RU")
	require.NoError(t, err, "batch failure must degrade, not fail")
	require.NotNil(t, ov)
	assert.Empty(t, ov.Keywords)
	assert.Contains(t, ov.Degraded, "tmdb_series")
}

func TestTMDBFallbackUseCase_GetOverview_UnknownIDReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{err: ports.ErrNotFound},
		Logger: discardLogger(),
	})
	_, err := uc.GetOverview(t.Context(), 9999, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)
}

func TestTMDBFallbackUseCase_GetOverview_NilOptionalPorts_StillReturnsCanon(t *testing.T) {
	t.Parallel()
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			8378: {ID: 8378, Hydration: series.HydrationStub},
		}},
		Logger: discardLogger(),
	})
	ov, err := uc.GetOverview(t.Context(), 8378, "")
	require.NoError(t, err)
	assert.Empty(t, ov.Description)
	assert.Empty(t, ov.Keywords)
	assert.Equal(t, []string{"tmdb_series"}, ov.Degraded)
}

func TestTMDBFallbackUseCase_GetRecommendations_StubReturnsItems(t *testing.T) {
	t.Parallel()
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			8378: {ID: 8378, Hydration: series.HydrationStub},
			101:  {ID: 101},
			102:  {ID: 102},
		}},
		Recommendations: &fakeFallbackRecsPort{ids: []domain.SeriesID{101, 102}},
		Logger:          discardLogger(),
	})
	rec, err := uc.GetRecommendations(t.Context(), 8378, "", 20, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, rec.TotalCount)
	assert.Len(t, rec.Items, 2)
	assert.False(t, rec.HasMore)
	assert.Equal(t, []string{"tmdb_series"}, rec.Degraded)
}

func TestTMDBFallbackUseCase_GetRecommendations_UnknownIDReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{err: ports.ErrNotFound},
		Logger: discardLogger(),
	})
	_, err := uc.GetRecommendations(t.Context(), 9999, "", 20, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)
}

func TestTMDBFallbackUseCase_GetRecommendations_NilRecsPortReturnsEmpty(t *testing.T) {
	t.Parallel()
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			8378: {ID: 8378, Hydration: series.HydrationStub},
		}},
		Logger: discardLogger(),
	})
	rec, err := uc.GetRecommendations(t.Context(), 8378, "", 20, 0)
	require.NoError(t, err)
	assert.Empty(t, rec.Items)
	assert.Equal(t, 0, rec.TotalCount)
	assert.Equal(t, []string{"tmdb_series"}, rec.Degraded)
}

// ─── Story 533b: tighten tmdb_series degraded marker semantics ────────

// TestTMDBFallbackUseCase_GetOverview_FullHydration_NoDegraded — Story 533b.
// Mirrors prod scenario for series 25551: canon HydrationFull + healthy
// ports + lang fallback en-US → ru-RU asked → Degraded MUST be empty.
func TestTMDBFallbackUseCase_GetOverview_FullHydration_NoDegraded(t *testing.T) {
	t.Parallel()
	overview := "Description in en-US"
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			25551: {ID: 25551, Hydration: series.HydrationFull},
		}},
		SeriesTexts: &fakeFallbackTexts{
			out: series.SeriesText{Overview: &overview, Language: "en-US"},
		},
		Keywords: &fakeFallbackKeywords{ids: []int64{1}, byID: map[int64]taxonomy.Keyword{
			1: {ID: 1, Name: "drama", Language: "ru-RU"},
		}},
		Logger: discardLogger(),
	})
	ov, err := uc.GetOverview(t.Context(), 25551, "ru-RU")
	require.NoError(t, err)
	assert.Empty(t, ov.Degraded, "full hydration + lang fallback must NOT mark tmdb_series")
	assert.Equal(t, "en-US", ov.DescriptionLanguage)
}

// TestTMDBFallbackUseCase_GetOverview_TextsLoadError_Degrades — real port
// failure (NOT ports.ErrNotFound) marks tmdb_series.
func TestTMDBFallbackUseCase_GetOverview_TextsLoadError_Degrades(t *testing.T) {
	t.Parallel()
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			25551: {ID: 25551, Hydration: series.HydrationFull},
		}},
		SeriesTexts: &fakeFallbackTexts{err: errors.New("db down")},
		Logger:      discardLogger(),
	})
	ov, err := uc.GetOverview(t.Context(), 25551, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, []string{"tmdb_series"}, ov.Degraded)
}

// TestTMDBFallbackUseCase_GetOverview_TextsNotFound_NoDegrade — ErrNotFound
// from SeriesTexts is a soft miss; MUST NOT mark tmdb_series alone.
func TestTMDBFallbackUseCase_GetOverview_TextsNotFound_NoDegrade(t *testing.T) {
	t.Parallel()
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			25551: {ID: 25551, Hydration: series.HydrationFull},
		}},
		SeriesTexts: &fakeFallbackTexts{err: ports.ErrNotFound},
		Logger:      discardLogger(),
	})
	ov, err := uc.GetOverview(t.Context(), 25551, "ru-RU")
	require.NoError(t, err)
	assert.Empty(t, ov.Degraded)
}

// TestTMDBFallbackUseCase_GetRecommendations_FullHydration_NoDegraded — Story 533b.
func TestTMDBFallbackUseCase_GetRecommendations_FullHydration_NoDegraded(t *testing.T) {
	t.Parallel()
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			25551: {ID: 25551, Hydration: series.HydrationFull},
			101:   {ID: 101, Hydration: series.HydrationFull},
		}},
		Recommendations: &fakeFallbackRecsPort{ids: []domain.SeriesID{101}},
		Logger:          discardLogger(),
	})
	rec, err := uc.GetRecommendations(t.Context(), 25551, "", 20, 0)
	require.NoError(t, err)
	assert.Empty(t, rec.Degraded)
}

// TestTMDBFallbackUseCase_GetRecommendations_ListError_Degrades — Story 533b.
func TestTMDBFallbackUseCase_GetRecommendations_ListError_Degrades(t *testing.T) {
	t.Parallel()
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			25551: {ID: 25551, Hydration: series.HydrationFull},
		}},
		Recommendations: &fakeFallbackRecsPort{err: errors.New("tmdb down")},
		Logger:          discardLogger(),
	})
	rec, err := uc.GetRecommendations(t.Context(), 25551, "", 20, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"tmdb_series"}, rec.Degraded)
}

// ─── Story 533: read-through TMDB freshener ──────────────────────────

// fakeFreshener records EnsureFresh calls + returns a canned result.
type fakeFreshener struct {
	mu     sync.Mutex
	calls  []fakeFreshenCall
	result seriesdetail.FreshenResult
}

type fakeFreshenCall struct {
	seriesID domain.SeriesID
	lang     string
	sections []freshener.Section
}

func (f *fakeFreshener) EnsureFresh(_ context.Context, id domain.SeriesID, lang string) seriesdetail.FreshenResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeFreshenCall{seriesID: id, lang: lang})
	return f.result
}

// EnsureFreshScope — Story 563 A5 method. Records the call under the
// same calls[] slice so existing test assertions on `Calls()` stay green
// (both entry points count identically for the "was the freshener
// invoked?" question). B-recs-probe-lang follow-up: records `sections`
// so recs endpoint tests can pin the SectionRecommendations scope.
func (f *fakeFreshener) EnsureFreshScope(
	_ context.Context,
	id domain.SeriesID,
	lang string,
	sections []freshener.Section,
	_ []int,
	_ bool,
	_ seriesdetail.EnsureFreshMode,
) (seriesdetail.FreshenResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeFreshenCall{seriesID: id, lang: lang, sections: sections})
	return f.result, nil
}

func (f *fakeFreshener) Calls() []fakeFreshenCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeFreshenCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestTMDBFallbackUseCase_Freshener_GetOverview_Called(t *testing.T) {
	t.Parallel()
	fr := &fakeFreshener{result: seriesdetail.FreshenResult{Refreshed: true}}
	fullCanon := series.Canon{ID: 8378, Hydration: series.HydrationFull, OriginalTitle: new("Mentalist")}
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:    &stubSeriesReader{canon: fullCanon},
		Freshener: fr,
		Logger:    discardLogger(),
	})
	_, err := uc.GetOverview(context.Background(), 8378, "ru-RU")
	require.NoError(t, err)
	calls := fr.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "ru-RU", calls[0].lang)
}

func TestTMDBFallbackUseCase_Freshener_GetRecommendations_Called(t *testing.T) {
	t.Parallel()
	fr := &fakeFreshener{result: seriesdetail.FreshenResult{Refreshed: true}}
	fullCanon := series.Canon{ID: 8378, Hydration: series.HydrationFull, OriginalTitle: new("Mentalist")}
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:    &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{8378: fullCanon}},
		Freshener: fr,
		Logger:    discardLogger(),
	})
	_, err := uc.GetRecommendations(context.Background(), 8378, "", 20, 0)
	require.NoError(t, err)
	calls := fr.Calls()
	require.Len(t, calls, 1)
	// Empty lang normalises to en-US via resolveLang.
	assert.Equal(t, "en-US", calls[0].lang)
}

// TestTMDBFallbackUseCase_Freshener_GetRecommendations_ScopeIsRecommendations
// pins the B-recs-probe-lang follow-up contract: the recs endpoint
// dispatches EnsureFreshScope scoped to SectionRecommendations, NOT
// the main-composer 4-section list (Skeleton+Overview+Cast+Media).
// This is the only site that triggers the recommendation-lang
// coverage probe on TMDB-only series.
func TestTMDBFallbackUseCase_Freshener_GetRecommendations_ScopeIsRecommendations(t *testing.T) {
	t.Parallel()
	fr := &fakeFreshener{result: seriesdetail.FreshenResult{Refreshed: true}}
	fullCanon := series.Canon{ID: 8378, Hydration: series.HydrationFull, OriginalTitle: new("Mentalist")}
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:    &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{8378: fullCanon}},
		Freshener: fr,
		Logger:    discardLogger(),
	})
	_, err := uc.GetRecommendations(context.Background(), 8378, "ru-RU", 20, 0)
	require.NoError(t, err)
	calls := fr.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "ru-RU", calls[0].lang)
	assert.Equal(t, []freshener.Section{freshener.SectionRecommendations}, calls[0].sections,
		"recs endpoint MUST probe SectionRecommendations scope only")
}

// ─── Story 533a: GetCanonical populates seasons + cast from DB ────

// ─── Story 533d: localized hero resolves via series_texts ─────────────

// ─── Story 541 — canon-language preference for series_texts fallback ──

// TestTMDBFallbackUseCase_GetOverview_CanonPreferred_SkipsEnUSRow —
// same canon-preference logic for the overview surface.
func TestTMDBFallbackUseCase_GetOverview_CanonPreferred_SkipsEnUSRow(t *testing.T) {
	t.Parallel()
	enOverview := "A rookie cop story."
	originalLang := "ru"
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			25551: {
				ID:               25551,
				Hydration:        series.HydrationFull,
				OriginalTitle:    new("Новичок"),
				OriginalLanguage: &originalLang,
			},
		}},
		SeriesTexts: &fakeFallbackTexts{
			out: series.SeriesText{
				SeriesID: 25551,
				Language: "en-US",
				Overview: &enOverview,
			},
		},
		Logger: discardLogger(),
	})
	require.NoError(t, err)
	out, err := uc.GetOverview(t.Context(), domain.SeriesID(25551), "ru-RU")
	require.NoError(t, err)
	assert.Empty(t, out.Description, "canon.OriginalLanguage=ru matches request — en-US overview MUST be skipped")
	assert.Empty(t, out.DescriptionLanguage)
}

// ─── Story 545 (Bug #3): cast hero poster resolves through MediaResolver ───

// fakeCastHashLookup satisfies media.HashLookupPort. Hit on URL match
// returns a stored hash; miss returns ports.ErrNotFound (resolver
// treats as degrade path — eager-hash + EnsurePending under the unified
// contract). EnsurePending is a no-op — the tested invariant is "raw
// path doesn't leak", not the pending-row side-effect.
type fakeCastHashLookup struct {
	byURL map[string]string
}

func (f *fakeCastHashLookup) HashForSourceURL(_ context.Context, url string) (string, error) {
	if h, ok := f.byURL[url]; ok {
		return h, nil
	}
	return "", ports.ErrNotFound
}

func (f *fakeCastHashLookup) EnsurePending(_ context.Context, _, _, _ string) error {
	return nil
}

// TestTMDBFallbackUseCase_GetCanonicalCast_ResolvesPosterAsset locks in
// the Bug #3 fix: the TMDB-only cast fallback MUST funnel the canon
// PosterAsset through MediaResolver.ResolveSync so the wire emits a
// content-addressed sha256 hash (or sentinel), never the raw TMDB path.
// Without this the FE built `/api/v1/media/%2Fabc.jpg` and 404'd.
func TestTMDBFallbackUseCase_GetCanonicalCast_ResolvesPosterAsset(t *testing.T) {
	t.Parallel()
	const wantHash = "deadbeef00000000000000000000000000000000000000000000000000000001"
	rawPath := "/hero.jpg"
	canon := series.Canon{
		ID:            25551,
		Hydration:     series.HydrationFull,
		OriginalTitle: new("Новичок"),
	}
	resolver := media.NewResolver(&fakeCastHashLookup{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w342/hero.jpg": wantHash,
	}}, nil, nil, discardLogger())
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{canon: canon},
		// S-E3a — hero poster raw path comes from series_media_texts.
		SeriesMediaTexts: &fakeFallbackMedia{out: series.SeriesMediaText{
			SeriesID: 25551, Language: "en-US", PosterAsset: &rawPath,
		}},
		MediaResolver: resolver,
		Logger:        discardLogger(),
	})
	require.NoError(t, err)

	out, err := uc.GetCanonicalCast(t.Context(), 25551, "en-US", 0)
	require.NoError(t, err)
	// S-E3a — CastFallbackResult.PosterAsset is the staged, resolved hero poster.
	require.NotNil(t, out.PosterAsset, "PosterAsset must not be nil — raw path should resolve")
	assert.Equal(t, wantHash, *out.PosterAsset, "PosterAsset must carry the resolved hash, not the raw TMDB path")
	// Regression invariant: a leading slash on the wire is the exact
	// shape that broke the FE. Lock it down explicitly.
	assert.NotEqual(t, byte('/'), (*out.PosterAsset)[0], "PosterAsset must not start with '/' — raw TMDB path leaked")
}

// ─── Canon-only single-season fallback (non-library series) ──────────

// stubSeasonsSource satisfies seriesdetail.CanonicalSeasonsCastReader —
// only GetCanonicalSeason is exercised by the GetSeason tests; the other
// two methods return empty so the fake stays a full interface value.
type stubSeasonsSource struct {
	season series.CanonSeason
	found  bool
	err    error
}

func (s *stubSeasonsSource) GetCanonicalSeasons(_ context.Context, _ domain.SeriesID, _ string) ([]seriesdetail.SeasonDetail, error) {
	return nil, nil
}

func (s *stubSeasonsSource) GetCanonicalSeason(_ context.Context, _ domain.SeriesID, _ int, _ string) (seriesdetail.SeasonDetail, bool, error) {
	if s.err != nil {
		return seriesdetail.SeasonDetail{}, false, s.err
	}
	if !s.found {
		return seriesdetail.SeasonDetail{}, false, nil
	}
	return seriesdetail.SeasonDetail{Canon: s.season, Episodes: []seriesdetail.EpisodeDetail{
		{Canon: series.CanonEpisode{SeasonNumber: s.season.SeasonNumber, EpisodeNumber: 1}},
	}}, true, nil
}

func (s *stubSeasonsSource) GetCanonicalCast(_ context.Context, _ domain.SeriesID, _ string, _ int) ([]seriesdetail.CastDetail, error) {
	return nil, nil
}

// GetSeason on a stub-hydration canon returns the canon season episodes
// with instance="", sonarr_series_id=0 and degraded=["tmdb_series"].
func TestTMDBFallbackUseCase_GetSeason_ReturnsCanonSeasonWithDegraded(t *testing.T) {
	t.Parallel()
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:            &stubSeriesReader{canon: series.Canon{ID: 3646, Hydration: series.HydrationStub}},
		SeasonsCastSource: &stubSeasonsSource{found: true, season: series.CanonSeason{SeriesID: 3646, SeasonNumber: 2}},
		Logger:            discardLogger(),
	})
	require.NoError(t, err)

	got, err := uc.GetSeason(t.Context(), 3646, 2, "ru-RU")
	require.NoError(t, err)
	require.Len(t, got.Seasons, 1)
	assert.Equal(t, 2, got.Seasons[0].Canon.SeasonNumber)
	assert.Len(t, got.Seasons[0].Episodes, 1)
	assert.Nil(t, got.Seasons[0].Episodes[0].State, "canon-only path must not carry per-instance state")
	assert.Equal(t, domain.InstanceName(""), got.Instance)
	assert.Zero(t, got.SonarrSeriesID)
	assert.Contains(t, got.Degraded, enrichment.SourceTMDBSeries)
}

// A fully-hydrated canon with a successful season load surfaces NO
// tmdb_series marker (matches GetOverview's Story 533b posture).
func TestTMDBFallbackUseCase_GetSeason_FullHydrationNoDegraded(t *testing.T) {
	t.Parallel()
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:            &stubSeriesReader{canon: series.Canon{ID: 3646, Hydration: series.HydrationFull}},
		SeasonsCastSource: &stubSeasonsSource{found: true, season: series.CanonSeason{SeriesID: 3646, SeasonNumber: 1}},
		Logger:            discardLogger(),
	})
	require.NoError(t, err)

	got, err := uc.GetSeason(t.Context(), 3646, 1, "en-US")
	require.NoError(t, err)
	require.Len(t, got.Seasons, 1)
	assert.NotContains(t, got.Degraded, enrichment.SourceTMDBSeries)
}

// Unknown canonical id → the SeriesPort error (ports.ErrNotFound
// wrapped) propagates so the handler can dispatch 404 series_not_found.
func TestTMDBFallbackUseCase_GetSeason_UnknownSeriesPropagatesErr(t *testing.T) {
	t.Parallel()
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{err: errors.Join(errors.New("canon load"), ports.ErrNotFound)},
		Logger: discardLogger(),
	})
	require.NoError(t, err)

	_, gerr := uc.GetSeason(t.Context(), 9999, 1, "en-US")
	require.Error(t, gerr)
	assert.ErrorIs(t, gerr, ports.ErrNotFound)
}

// A season the series doesn't have → empty (non-nil) Seasons, no error
// (the handler maps that to 404 season_not_found).
func TestTMDBFallbackUseCase_GetSeason_MissingSeasonReturnsEmpty(t *testing.T) {
	t.Parallel()
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:            &stubSeriesReader{canon: series.Canon{ID: 3646, Hydration: series.HydrationFull}},
		SeasonsCastSource: &stubSeasonsSource{found: false},
		Logger:            discardLogger(),
	})
	require.NoError(t, err)

	got, gerr := uc.GetSeason(t.Context(), 3646, 8, "en-US")
	require.NoError(t, gerr)
	require.NotNil(t, got.Seasons)
	assert.Empty(t, got.Seasons)
}
