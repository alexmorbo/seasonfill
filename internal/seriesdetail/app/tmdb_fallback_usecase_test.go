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
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
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

// fakeFallbackTexts satisfies seriesdetail.SeriesTextsPort.
type fakeFallbackTexts struct {
	out series.SeriesText
	err error
}

func (f *fakeFallbackTexts) GetWithFallback(_ context.Context, _ domain.SeriesID, _ string) (series.SeriesText, error) {
	if f.err != nil {
		return series.SeriesText{}, f.err
	}
	return f.out, nil
}

// fakeFallbackKeywords satisfies seriesdetail.KeywordsPort.
type fakeFallbackKeywords struct {
	ids  []int64
	byID map[int64]taxonomy.Keyword
	err  error
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
	rec, err := uc.GetRecommendations(t.Context(), 8378, 20, 0)
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
	_, err := uc.GetRecommendations(t.Context(), 9999, 20, 0)
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
	rec, err := uc.GetRecommendations(t.Context(), 8378, 20, 0)
	require.NoError(t, err)
	assert.Empty(t, rec.Items)
	assert.Equal(t, 0, rec.TotalCount)
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
	_, err := uc.GetRecommendations(context.Background(), 8378, 20, 0)
	require.NoError(t, err)
	calls := fr.Calls()
	require.Len(t, calls, 1)
	// Recommendations doesn't take lang — freshener probes with en-US.
	assert.Equal(t, "en-US", calls[0].lang)
}
