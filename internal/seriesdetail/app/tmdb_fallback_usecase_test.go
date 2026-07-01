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
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
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

// fakeOnDemandEnricher records EnqueueIfStale calls for assertion (Story 528).
type fakeOnDemandEnricher struct {
	mu    sync.Mutex
	calls []fakeEnrichCall
}

type fakeEnrichCall struct {
	seriesID  domain.SeriesID
	hydration series.Hydration
}

func (f *fakeOnDemandEnricher) EnqueueIfStale(id domain.SeriesID, h series.Hydration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeEnrichCall{seriesID: id, hydration: h})
}

func (f *fakeOnDemandEnricher) Calls() []fakeEnrichCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeEnrichCall, len(f.calls))
	copy(out, f.calls)
	return out
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

func TestTMDBFallbackUseCase_FullHydration(t *testing.T) {
	title := "Rick and Morty"
	canon := series.Canon{ID: 140, Title: title, Hydration: series.HydrationFull}
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{canon: canon},
	})
	require.NoError(t, err)
	detail, err := uc.GetCanonical(context.Background(), 140, "en-US")
	require.NoError(t, err)
	assert.Equal(t, title, detail.Canon.Title)
	assert.Empty(t, detail.Degraded, "full hydration has no tmdb_series degraded")
	assert.NotZero(t, detail.SyncedAt)
	assert.Equal(t, []domain.InstanceName{}, detail.InLibraryInstances)
	assert.Equal(t, domain.SeriesID(140), detail.SeriesID)
}

func TestTMDBFallbackUseCase_StubHydration_DegradesTMDBSeries(t *testing.T) {
	canon := series.Canon{ID: 140, Title: "Stub", Hydration: series.HydrationStub}
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{canon: canon},
	})
	require.NoError(t, err)
	detail, err := uc.GetCanonical(context.Background(), 140, "en-US")
	require.NoError(t, err)
	assert.Equal(t, []enrichment.Source{enrichment.SourceTMDBSeries}, detail.Degraded)
}

func TestTMDBFallbackUseCase_EmptyHydration_DegradesTMDBSeries(t *testing.T) {
	// Empty hydration treated as stub by the defensive default.
	canon := series.Canon{ID: 140, Title: "Empty"}
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{canon: canon},
	})
	detail, err := uc.GetCanonical(context.Background(), 140, "en-US")
	require.NoError(t, err)
	assert.Equal(t, []enrichment.Source{enrichment.SourceTMDBSeries}, detail.Degraded)
}

func TestTMDBFallbackUseCase_CanonNotFound(t *testing.T) {
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{err: ports.ErrNotFound},
	})
	_, err := uc.GetCanonical(context.Background(), 99999, "en-US")
	assert.ErrorIs(t, err, ports.ErrNotFound)
}

func TestTMDBFallbackUseCase_SeriesReaderError_Propagates(t *testing.T) {
	wantErr := errors.New("db boom")
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{err: wantErr},
	})
	_, err := uc.GetCanonical(context.Background(), 140, "en-US")
	assert.ErrorIs(t, err, wantErr)
}

func TestNewTMDBFallbackUseCase_NilSeriesReturnsError(t *testing.T) {
	_, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{})
	require.Error(t, err)
}

func TestTMDBFallbackUseCase_EmptyLang_DefaultsToEnUS(t *testing.T) {
	canon := series.Canon{ID: 140, Title: "T", Hydration: series.HydrationFull}
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{canon: canon},
	})
	detail, err := uc.GetCanonical(context.Background(), 140, "")
	require.NoError(t, err)
	assert.Equal(t, "en-US", detail.Lang)
}

// ─── Story 528: on-demand enrichment trigger ──────────────────────

func TestTMDBFallbackUseCase_OnDemandEnrich_StubTriggers(t *testing.T) {
	t.Parallel()
	enr := &fakeOnDemandEnricher{}
	stubCanon := series.Canon{ID: 8378, Hydration: series.HydrationStub, Title: "stub"}
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:   &stubSeriesReader{canon: stubCanon},
		Enricher: enr,
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(context.Background(), domain.SeriesID(8378), "en")
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.Equal(t, []enrichment.Source{enrichment.SourceTMDBSeries}, d.Degraded)
	calls := enr.Calls()
	require.Len(t, calls, 1, "stub canon must trigger one enqueue")
	assert.Equal(t, domain.SeriesID(8378), calls[0].seriesID)
	assert.Equal(t, series.HydrationStub, calls[0].hydration)
}

func TestTMDBFallbackUseCase_OnDemandEnrich_FullSkips(t *testing.T) {
	t.Parallel()
	enr := &fakeOnDemandEnricher{}
	fullCanon := series.Canon{ID: 8378, Hydration: series.HydrationFull, Title: "ok"}
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:   &stubSeriesReader{canon: fullCanon},
		Enricher: enr,
	})
	require.NoError(t, err)
	_, err = uc.GetCanonical(context.Background(), domain.SeriesID(8378), "en")
	require.NoError(t, err)
	assert.Empty(t, enr.Calls(), "HydrationFull must NOT enqueue")
}

func TestTMDBFallbackUseCase_OnDemandEnrich_NilEnricherSafe(t *testing.T) {
	t.Parallel()
	stubCanon := series.Canon{ID: 8378, Hydration: series.HydrationStub, Title: "stub"}
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{canon: stubCanon},
		// Enricher intentionally omitted — nil-OK contract.
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(context.Background(), domain.SeriesID(8378), "en")
	require.NoError(t, err)
	assert.Equal(t, []enrichment.Source{enrichment.SourceTMDBSeries}, d.Degraded,
		"nil enricher MUST NOT change the degraded slice (still stub)")
}

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
// invoked?" question).
func (f *fakeFreshener) EnsureFreshScope(
	_ context.Context,
	id domain.SeriesID,
	lang string,
	_ []freshener.Section,
	_ []int,
	_ bool,
	_ seriesdetail.EnsureFreshMode,
) (seriesdetail.FreshenResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeFreshenCall{seriesID: id, lang: lang})
	return f.result, nil
}

func (f *fakeFreshener) Calls() []fakeFreshenCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeFreshenCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestTMDBFallbackUseCase_Freshener_RefreshedFull_NoDegradedAppended(t *testing.T) {
	t.Parallel()
	fr := &fakeFreshener{result: seriesdetail.FreshenResult{Refreshed: true}}
	fullCanon := series.Canon{ID: 8378, Hydration: series.HydrationFull, Title: "Mentalist"}
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:    &stubSeriesReader{canon: fullCanon},
		Freshener: fr,
		Logger:    discardLogger(),
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(context.Background(), 8378, "ru-RU")
	require.NoError(t, err)
	assert.Empty(t, d.Degraded, "refreshed + full hydration must have empty Degraded")
	// EnsureFresh called once with the resolved lang.
	calls := fr.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, domain.SeriesID(8378), calls[0].seriesID)
	assert.Equal(t, "ru-RU", calls[0].lang)
}

func TestTMDBFallbackUseCase_Freshener_Degraded_AppendsTMDBSeries(t *testing.T) {
	t.Parallel()
	fr := &fakeFreshener{result: seriesdetail.FreshenResult{Degraded: true}}
	// Full canon means the stub-branch will NOT add tmdb_series; the
	// only source for the marker is the Story 533 timeout-fallback branch.
	fullCanon := series.Canon{ID: 8378, Hydration: series.HydrationFull, Title: "Mentalist"}
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:    &stubSeriesReader{canon: fullCanon},
		Freshener: fr,
		Logger:    discardLogger(),
	})
	d, err := uc.GetCanonical(context.Background(), 8378, "en-US")
	require.NoError(t, err)
	assert.Equal(t, []enrichment.Source{enrichment.SourceTMDBSeries}, d.Degraded,
		"Degraded=true must surface tmdb_series even on full canon")
}

func TestTMDBFallbackUseCase_Freshener_Degraded_DedupesWithStubBranch(t *testing.T) {
	t.Parallel()
	fr := &fakeFreshener{result: seriesdetail.FreshenResult{Degraded: true}}
	// Stub canon — the existing stub-branch adds tmdb_series; the Story
	// 533 branch must NOT double-append.
	stubCanon := series.Canon{ID: 8378, Hydration: series.HydrationStub}
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:    &stubSeriesReader{canon: stubCanon},
		Freshener: fr,
		Logger:    discardLogger(),
	})
	d, err := uc.GetCanonical(context.Background(), 8378, "en-US")
	require.NoError(t, err)
	assert.Equal(t, []enrichment.Source{enrichment.SourceTMDBSeries}, d.Degraded)
}

func TestTMDBFallbackUseCase_Freshener_Nil_BehavesLikeStory532(t *testing.T) {
	t.Parallel()
	fullCanon := series.Canon{ID: 8378, Hydration: series.HydrationFull, Title: "Mentalist"}
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{canon: fullCanon},
		Logger: discardLogger(),
	})
	d, err := uc.GetCanonical(context.Background(), 8378, "en-US")
	require.NoError(t, err)
	assert.Empty(t, d.Degraded)
}

func TestTMDBFallbackUseCase_Freshener_GetOverview_Called(t *testing.T) {
	t.Parallel()
	fr := &fakeFreshener{result: seriesdetail.FreshenResult{Refreshed: true}}
	fullCanon := series.Canon{ID: 8378, Hydration: series.HydrationFull, Title: "Mentalist"}
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
	fullCanon := series.Canon{ID: 8378, Hydration: series.HydrationFull, Title: "Mentalist"}
	uc, _ := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:    &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{8378: fullCanon}},
		Freshener: fr,
		Logger:    discardLogger(),
	})
	_, err := uc.GetRecommendations(context.Background(), 8378, "", 20, 0)
	require.NoError(t, err)
	calls := fr.Calls()
	require.Len(t, calls, 1)
	// Recommendations doesn't take lang — freshener probes with en-US.
	assert.Equal(t, "en-US", calls[0].lang)
}

// ─── Story 533a: GetCanonical populates seasons + cast from DB ────

// fakeFallbackSeasonsCastSource satisfies seriesdetail.CanonicalSeasonsCastReader.
// Independent fields per method so tests can wire them separately.
type fakeFallbackSeasonsCastSource struct {
	seasons    []seriesdetail.SeasonDetail
	seasonsErr error
	cast       []seriesdetail.CastDetail
	castErr    error

	mu              sync.Mutex
	seasonsCalls    int
	castCalls       int
	lastSeasonsLang string
	lastCastLimit   int
}

func (f *fakeFallbackSeasonsCastSource) GetCanonicalSeasons(_ context.Context, _ domain.SeriesID, lang string) ([]seriesdetail.SeasonDetail, error) {
	f.mu.Lock()
	f.seasonsCalls++
	f.lastSeasonsLang = lang
	f.mu.Unlock()
	if f.seasonsErr != nil {
		return nil, f.seasonsErr
	}
	return f.seasons, nil
}

func (f *fakeFallbackSeasonsCastSource) GetCanonicalCast(_ context.Context, _ domain.SeriesID, limit int) ([]seriesdetail.CastDetail, error) {
	f.mu.Lock()
	f.castCalls++
	f.lastCastLimit = limit
	f.mu.Unlock()
	if f.castErr != nil {
		return nil, f.castErr
	}
	return f.cast, nil
}

func TestTMDBFallbackUseCase_GetCanonical_PopulatesSeasonsAndCastWhenWired(t *testing.T) {
	t.Parallel()
	seasons := []seriesdetail.SeasonDetail{
		{
			Canon: series.CanonSeason{SeasonNumber: 1, Name: new("Season 1")},
			Episodes: []seriesdetail.EpisodeDetail{
				{Canon: series.CanonEpisode{ID: 1001, SeasonNumber: 1, EpisodeNumber: 1}},
				{Canon: series.CanonEpisode{ID: 1002, SeasonNumber: 1, EpisodeNumber: 2}},
			},
		},
		{
			Canon:    series.CanonSeason{SeasonNumber: 2, Name: new("Season 2")},
			Episodes: []seriesdetail.EpisodeDetail{},
		},
	}
	cast := []seriesdetail.CastDetail{
		{
			Credit: people.SeriesCredit{PersonID: 42, CharacterName: new("Rick")},
			Person: people.Person{ID: 42, Name: "Justin Roiland"},
		},
	}
	src := &fakeFallbackSeasonsCastSource{seasons: seasons, cast: cast}
	canon := series.Canon{ID: 8378, Hydration: series.HydrationFull, Title: "Mentalist"}
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:            &stubSeriesReader{canon: canon},
		Logger:            discardLogger(),
		SeasonsCastSource: src,
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(context.Background(), 8378, "ru-RU")
	require.NoError(t, err)
	require.Len(t, d.Seasons, 2)
	assert.Equal(t, 1, d.Seasons[0].Canon.SeasonNumber)
	assert.Len(t, d.Seasons[0].Episodes, 2)
	assert.Len(t, d.Seasons[1].Episodes, 0, "second season has empty episodes — preserved")
	require.Len(t, d.Cast, 1)
	assert.Equal(t, int64(42), d.Cast[0].Person.ID)
	assert.Equal(t, "ru-RU", src.lastSeasonsLang, "lang flows through")
	assert.Equal(t, 0, src.lastCastLimit, "default limit (0) reaches Composer; Composer applies CastDefaultLimit")
	assert.Empty(t, d.Degraded, "full hydration + healthy source → no degraded")
}

func TestTMDBFallbackUseCase_GetCanonical_NilSeasonsCastSource_LeavesEmpty(t *testing.T) {
	t.Parallel()
	canon := series.Canon{ID: 8378, Hydration: series.HydrationFull, Title: "Mentalist"}
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &stubSeriesReader{canon: canon},
		Logger: discardLogger(),
		// SeasonsCastSource intentionally nil — nil-OK.
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(context.Background(), 8378, "en-US")
	require.NoError(t, err)
	assert.Empty(t, d.Seasons, "nil source → nil/empty seasons")
	assert.Empty(t, d.Cast, "nil source → nil/empty cast")
}

func TestTMDBFallbackUseCase_GetCanonical_SeasonsErrorAppendsDegraded(t *testing.T) {
	t.Parallel()
	src := &fakeFallbackSeasonsCastSource{
		seasonsErr: errors.New("db boom"),
		cast:       []seriesdetail.CastDetail{},
	}
	canon := series.Canon{ID: 8378, Hydration: series.HydrationFull, Title: "Mentalist"}
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:            &stubSeriesReader{canon: canon},
		Logger:            discardLogger(),
		SeasonsCastSource: src,
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(context.Background(), 8378, "en-US")
	require.NoError(t, err)
	assert.Empty(t, d.Seasons)
	assert.Contains(t, d.Degraded, enrichment.SourceTMDBSeries, "seasons failure must mark fallback degraded")
}

func TestTMDBFallbackUseCase_GetCanonical_CastErrorAppendsDegraded(t *testing.T) {
	t.Parallel()
	src := &fakeFallbackSeasonsCastSource{
		seasons: []seriesdetail.SeasonDetail{},
		castErr: errors.New("people boom"),
	}
	canon := series.Canon{ID: 8378, Hydration: series.HydrationFull, Title: "Mentalist"}
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:            &stubSeriesReader{canon: canon},
		Logger:            discardLogger(),
		SeasonsCastSource: src,
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(context.Background(), 8378, "en-US")
	require.NoError(t, err)
	assert.Empty(t, d.Cast)
	assert.Contains(t, d.Degraded, enrichment.SourceTMDBSeries, "cast failure must mark fallback degraded")
}

func TestTMDBFallbackUseCase_GetCanonical_StubHydration_DegradedNotDuplicated(t *testing.T) {
	t.Parallel()
	src := &fakeFallbackSeasonsCastSource{
		seasons: []seriesdetail.SeasonDetail{},
		castErr: errors.New("boom"),
	}
	canon := series.Canon{ID: 8378, Hydration: series.HydrationStub, Title: "stub"}
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:            &stubSeriesReader{canon: canon},
		Logger:            discardLogger(),
		SeasonsCastSource: src,
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(context.Background(), 8378, "en-US")
	require.NoError(t, err)
	count := 0
	for _, src := range d.Degraded {
		if src == enrichment.SourceTMDBSeries {
			count++
		}
	}
	assert.Equal(t, 1, count, "stub-branch + cast-error both want tmdb_series; must dedupe")
}

// ─── Story 533d: localized hero resolves via series_texts ─────────────

func TestTMDBFallbackUseCase_GetCanonical_LocalizedTitle(t *testing.T) {
	t.Parallel()
	title := "Менталист"
	tagline := "Чтение между строк"
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			8378: {ID: 8378, Hydration: series.HydrationFull, Title: "The Mentalist"},
		}},
		SeriesTexts: &fakeFallbackTexts{
			out: series.SeriesText{
				SeriesID: 8378,
				Language: "ru-RU",
				Title:    &title,
				Tagline:  &tagline,
			},
		},
		Logger: discardLogger(),
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(t.Context(), domain.SeriesID(8378), "ru-RU")
	require.NoError(t, err)
	require.NotNil(t, d.Text, "Detail.Text must be populated from series_texts")
	require.NotNil(t, d.Text.Title)
	assert.Equal(t, "Менталист", *d.Text.Title)
	assert.Equal(t, "ru-RU", d.Text.Language)
	require.NotNil(t, d.Text.Tagline)
	assert.Equal(t, "Чтение между строк", *d.Text.Tagline)
	assert.Empty(t, d.Degraded, "successful texts read MUST NOT mark tmdb_series")
}

func TestTMDBFallbackUseCase_GetCanonical_FallbackToEnUS(t *testing.T) {
	t.Parallel()
	// SeriesTexts.GetWithFallback returns en-US when ru-RU is missing.
	// The fake returns whatever `out` says — we model "fallback hit en-US".
	title := "The Mentalist"
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			8378: {ID: 8378, Hydration: series.HydrationFull, Title: "The Mentalist"},
		}},
		SeriesTexts: &fakeFallbackTexts{
			out: series.SeriesText{
				SeriesID: 8378,
				Language: "en-US",
				Title:    &title,
			},
		},
		Logger: discardLogger(),
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(t.Context(), domain.SeriesID(8378), "ru-RU")
	require.NoError(t, err)
	require.NotNil(t, d.Text)
	assert.Equal(t, "en-US", d.Text.Language)
	assert.Empty(t, d.Degraded)
}

func TestTMDBFallbackUseCase_GetCanonical_NoTextRow_KeepsCanon(t *testing.T) {
	t.Parallel()
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			8378: {ID: 8378, Hydration: series.HydrationFull, Title: "The Mentalist"},
		}},
		SeriesTexts: &fakeFallbackTexts{err: ports.ErrNotFound},
		Logger:      discardLogger(),
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(t.Context(), domain.SeriesID(8378), "ru-RU")
	require.NoError(t, err)
	assert.Nil(t, d.Text, "ErrNotFound = no row, leave Text nil so mapHero uses canon.title")
	assert.Empty(t, d.Degraded, "ErrNotFound is a soft miss, MUST NOT mark tmdb_series alone")
}

func TestTMDBFallbackUseCase_GetCanonical_TextsPortError_Degrades(t *testing.T) {
	t.Parallel()
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			8378: {ID: 8378, Hydration: series.HydrationFull, Title: "The Mentalist"},
		}},
		SeriesTexts: &fakeFallbackTexts{err: errors.New("db down")},
		Logger:      discardLogger(),
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(t.Context(), domain.SeriesID(8378), "ru-RU")
	require.NoError(t, err)
	assert.Nil(t, d.Text)
	assert.Equal(t, []enrichment.Source{enrichment.SourceTMDBSeries}, d.Degraded)
}

func TestTMDBFallbackUseCase_GetCanonical_NilSeriesTextsSafe(t *testing.T) {
	t.Parallel()
	// Regression-safety: existing fakes that omit SeriesTexts must still work.
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			8378: {ID: 8378, Hydration: series.HydrationFull, Title: "The Mentalist"},
		}},
		Logger: discardLogger(),
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(t.Context(), domain.SeriesID(8378), "ru-RU")
	require.NoError(t, err)
	assert.Nil(t, d.Text, "nil SeriesTexts port leaves Text nil — canon.title wins in DTO")
	assert.Empty(t, d.Degraded)
}

// ─── Story 541 — canon-language preference for series_texts fallback ──

// TestTMDBFallbackUseCase_GetCanonical_CanonPreferred_DropsEnUSFallback —
// the live bug for series 25551 ("Новичок" / "The Rookie"). canon's
// OriginalLanguage="ru" matches request "ru-RU"; series_texts has only
// en-US row → fake returns en-US → usecase MUST drop d.Text so DTO
// renders canon.Title.
func TestTMDBFallbackUseCase_GetCanonical_CanonPreferred_DropsEnUSFallback(t *testing.T) {
	t.Parallel()
	enTitle := "The Rookie"
	originalLang := "ru"
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			25551: {
				ID:               25551,
				Hydration:        series.HydrationFull,
				Title:            "Новичок",
				OriginalLanguage: &originalLang,
			},
		}},
		SeriesTexts: &fakeFallbackTexts{
			// fallback hit en-US row (no ru-RU row exists)
			out: series.SeriesText{
				SeriesID: 25551,
				Language: "en-US",
				Title:    &enTitle,
			},
		},
		Logger: discardLogger(),
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(t.Context(), domain.SeriesID(25551), "ru-RU")
	require.NoError(t, err)
	assert.Nil(t, d.Text, "canon.OriginalLanguage=ru matches request ru-RU, en-US row MUST be ignored → mapHero renders canon.Title")
	assert.Empty(t, d.Degraded, "preference is a normal hit, not a degradation")
}

// TestTMDBFallbackUseCase_GetCanonical_CanonPreferred_AcceptsMatchingRow —
// regression-safety: when SeriesTexts DOES return a matching row, keep it.
func TestTMDBFallbackUseCase_GetCanonical_CanonPreferred_AcceptsMatchingRow(t *testing.T) {
	t.Parallel()
	ruTitle := "Новичок"
	originalLang := "ru"
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			25551: {
				ID:               25551,
				Hydration:        series.HydrationFull,
				Title:            "The Rookie",
				OriginalLanguage: &originalLang,
			},
		}},
		SeriesTexts: &fakeFallbackTexts{
			out: series.SeriesText{
				SeriesID: 25551,
				Language: "ru-RU",
				Title:    &ruTitle,
			},
		},
		Logger: discardLogger(),
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(t.Context(), domain.SeriesID(25551), "ru-RU")
	require.NoError(t, err)
	require.NotNil(t, d.Text, "matching ru-RU row MUST be used")
	require.NotNil(t, d.Text.Title)
	assert.Equal(t, "Новичок", *d.Text.Title)
}

// TestTMDBFallbackUseCase_GetCanonical_CanonPreferred_NoCanonOriginalLang —
// when canon.OriginalLanguage is nil/empty, preference doesn't fire — en-US
// fallback wins as before (current behavior preserved).
func TestTMDBFallbackUseCase_GetCanonical_CanonPreferred_NoCanonOriginalLang(t *testing.T) {
	t.Parallel()
	enTitle := "The Mentalist"
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series: &fakeMapSeriesReader{rows: map[domain.SeriesID]series.Canon{
			8378: {ID: 8378, Hydration: series.HydrationFull, Title: "The Mentalist"},
		}},
		SeriesTexts: &fakeFallbackTexts{
			out: series.SeriesText{
				SeriesID: 8378,
				Language: "en-US",
				Title:    &enTitle,
			},
		},
		Logger: discardLogger(),
	})
	require.NoError(t, err)
	d, err := uc.GetCanonical(t.Context(), domain.SeriesID(8378), "ru-RU")
	require.NoError(t, err)
	require.NotNil(t, d.Text, "no canon.OriginalLanguage → fallback en-US wins as before")
	assert.Equal(t, "en-US", d.Text.Language)
}

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
				Title:            "Новичок",
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
		ID:          25551,
		Hydration:   series.HydrationFull,
		Title:       "Новичок",
		PosterAsset: &rawPath,
	}
	resolver := media.NewResolver(&fakeCastHashLookup{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w342/hero.jpg": wantHash,
	}}, nil, nil, discardLogger())
	uc, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:        &stubSeriesReader{canon: canon},
		MediaResolver: resolver,
		Logger:        discardLogger(),
	})
	require.NoError(t, err)

	out, err := uc.GetCanonicalCast(t.Context(), 25551, "en-US", 0)
	require.NoError(t, err)
	require.NotNil(t, out.Canon.PosterAsset, "PosterAsset must not be nil — raw path should resolve")
	assert.Equal(t, wantHash, *out.Canon.PosterAsset, "PosterAsset must carry the resolved hash, not the raw TMDB path")
	// Regression invariant: a leading slash on the wire is the exact
	// shape that broke the FE. Lock it down explicitly.
	assert.NotEqual(t, byte('/'), (*out.Canon.PosterAsset)[0], "PosterAsset must not start with '/' — raw TMDB path leaked")
}
