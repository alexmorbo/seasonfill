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
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// --- minimal port fakes (composer-internal package) ---

type ovFakeCache struct {
	entries map[string]series.CacheEntry
}

func (f *ovFakeCache) Get(_ context.Context, inst domain.InstanceName, sid domain.SonarrSeriesID) (series.CacheEntry, error) {
	e, ok := f.entries[string(inst)+"|"+itoaOV(int(sid))]
	if !ok {
		return series.CacheEntry{}, ports.ErrNotFound
	}
	return e, nil
}

func (f *ovFakeCache) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	return nil, nil
}

type ovFakeSeries struct {
	rows map[domain.SeriesID]series.Canon
	err  error
}

func (f *ovFakeSeries) Get(_ context.Context, id domain.SeriesID) (series.Canon, error) {
	if f.err != nil {
		return series.Canon{}, f.err
	}
	c, ok := f.rows[id]
	if !ok {
		return series.Canon{}, ports.ErrNotFound
	}
	return c, nil
}

func (f *ovFakeSeries) GetByTMDBID(_ context.Context, _ domain.TMDBID) (series.Canon, error) {
	if f.err != nil {
		return series.Canon{}, f.err
	}
	return series.Canon{}, ports.ErrNotFound
}

func (f *ovFakeSeries) ListByIDs(_ context.Context, ids []domain.SeriesID) ([]series.Canon, error) {
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

func (f *ovFakeSeries) ListByTMDBIDs(_ context.Context, tmdbIDs []domain.TMDBID) ([]series.Canon, error) {
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

type ovFakeTexts struct {
	text series.SeriesText
	err  error
}

func (f ovFakeTexts) GetWithFallback(_ context.Context, _ domain.SeriesID, _ string) (series.SeriesText, error) {
	if f.err != nil {
		return series.SeriesText{}, f.err
	}
	return f.text, nil
}

func (f ovFakeTexts) ListByIDsWithFallback(_ context.Context, ids []domain.SeriesID, _ string) (map[domain.SeriesID]series.SeriesText, error) {
	if f.err != nil {
		return nil, f.err
	}
	// Default fake: return the single seeded text for every requested id.
	// Tests that need per-id control should embed / wrap this fake.
	out := make(map[domain.SeriesID]series.SeriesText, len(ids))
	for _, id := range ids {
		out[id] = f.text
	}
	return out, nil
}

type ovFakeKeywords struct {
	ids    []int64
	listEr error
	get    map[int64]taxonomy.Keyword
}

func (f ovFakeKeywords) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	if f.listEr != nil {
		return nil, f.listEr
	}
	return f.ids, nil
}

func (f ovFakeKeywords) Get(_ context.Context, id int64, lang string) (taxonomy.Keyword, error) {
	if k, ok := f.get[id]; ok {
		return k, nil
	}
	return taxonomy.Keyword{ID: id, Language: lang}, nil
}

func (f ovFakeKeywords) ListByIDsWithFallback(_ context.Context, ids []int64, lang string) ([]taxonomy.Keyword, error) {
	out := make([]taxonomy.Keyword, 0, len(ids))
	for _, id := range ids {
		if k, ok := f.get[id]; ok {
			out = append(out, k)
			continue
		}
		out = append(out, taxonomy.Keyword{ID: id, Language: lang})
	}
	return out, nil
}

func itoaOV(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	out := ""
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	if neg {
		out = "-" + out
	}
	return out
}

func i64ptrOV(v int64) *domain.SeriesID { sid := domain.SeriesID(v); return &sid }

func newOverviewComposer(canon series.Canon, cache map[string]series.CacheEntry, texts SeriesTextsPort, kw KeywordsPort) *Composer {
	return NewComposer(Deps{
		SeriesCache: &ovFakeCache{entries: cache},
		Series:      &ovFakeSeries{rows: map[domain.SeriesID]series.Canon{canon.ID: canon}},
		SeriesTexts: texts,
		Keywords:    kw,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:         func() time.Time { return time.Now().UTC() },
	})
}

func TestComposerGetOverview_HappyPath(t *testing.T) {
	t.Parallel()
	awards := "Won 16 Emmys"
	overview := "Walter White cooks meth."
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canon := series.Canon{ID: 42, OriginalTitle: new("Breaking Bad"), OMDBAwards: &awards}
	texts := ovFakeTexts{text: series.SeriesText{Overview: &overview, Language: "en-US"}}
	kw := ovFakeKeywords{
		ids: []int64{1, 2},
		get: map[int64]taxonomy.Keyword{
			1: {ID: 1, Name: "drug", Language: "en-US"},
			2: {ID: 2, Name: "crime", Language: "en-US"},
		},
	}
	c := newOverviewComposer(canon, cache, texts, kw)

	ov, err := c.GetOverview(t.Context(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.NotNil(t, ov)
	require.Equal(t, "Walter White cooks meth.", ov.Description)
	require.Equal(t, "en-US", ov.DescriptionLanguage)
	require.Equal(t, 2, len(ov.Keywords))
	require.Equal(t, "drug", ov.Keywords[0].Name)
	require.NotNil(t, ov.Awards)
	require.Equal(t, "Won 16 Emmys", *ov.Awards)
	require.Equal(t, 0, len(ov.Degraded))
	require.Equal(t, domain.InstanceName("alpha"), ov.Instance)
	require.Equal(t, domain.SonarrSeriesID(1), ov.SonarrSeriesID)
	require.Equal(t, domain.SeriesID(42), ov.SeriesID)
	require.Equal(t, "en-US", ov.Lang)
}

func TestComposerGetOverview_NoCacheRow(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: nil},
	}
	canon := series.Canon{ID: 42}
	c := newOverviewComposer(canon, cache, ovFakeTexts{err: ports.ErrNotFound}, ovFakeKeywords{})

	ov, err := c.GetOverview(t.Context(), "alpha", 1, "en-US")
	require.Nil(t, ov)
	require.Error(t, err)
	require.True(t, errors.Is(err, ports.ErrNotFound), "expected ports.ErrNotFound, got %v", err)
}

func TestComposerGetOverview_OMDbAwardsNotApplicable(t *testing.T) {
	t.Parallel()
	awards := "N/A"
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canon := series.Canon{ID: 42, OMDBAwards: &awards}
	c := newOverviewComposer(canon, cache, ovFakeTexts{err: ports.ErrNotFound}, ovFakeKeywords{})

	ov, err := c.GetOverview(t.Context(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.NotNil(t, ov)
	require.Nil(t, ov.Awards, "N/A awards must be suppressed to nil")
}

func TestComposerGetOverview_TextsLoadFailsDegrades(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canon := series.Canon{ID: 42}
	c := newOverviewComposer(canon, cache,
		ovFakeTexts{err: errors.New("db down")}, //nolint:err113
		ovFakeKeywords{})

	ov, err := c.GetOverview(t.Context(), "alpha", 1, "en-US")
	require.NoError(t, err, "non-ErrNotFound texts failure must NOT fail the response")
	require.NotNil(t, ov)
	require.Equal(t, "", ov.Description)
	require.Contains(t, ov.Degraded, "tmdb_series")
}

// Story 552 (E-1 Z3) — batched keyword fetch failure path. When
// ListByIDsWithFallback errors (separate from ListBySeries), overview
// must still degrade with tmdb_series tag, NOT fail the response.
type ovFakeKeywordsBatchErr struct {
	ids []int64
}

func (f ovFakeKeywordsBatchErr) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return f.ids, nil
}
func (f ovFakeKeywordsBatchErr) Get(_ context.Context, id int64, lang string) (taxonomy.Keyword, error) {
	return taxonomy.Keyword{ID: id, Language: lang}, nil
}
func (f ovFakeKeywordsBatchErr) ListByIDsWithFallback(_ context.Context, _ []int64, _ string) ([]taxonomy.Keyword, error) {
	return nil, errors.New("batch boom") //nolint:err113
}

func TestComposerGetOverview_KeywordsBatchFailureMarksDegraded(t *testing.T) {
	t.Parallel()
	cache := map[string]series.CacheEntry{
		"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64ptrOV(42)},
	}
	canon := series.Canon{ID: 42}
	c := newOverviewComposer(canon, cache,
		ovFakeTexts{err: ports.ErrNotFound},
		ovFakeKeywordsBatchErr{ids: []int64{1, 2, 3}})

	ov, err := c.GetOverview(t.Context(), "alpha", 1, "en-US")
	require.NoError(t, err, "batch failure must degrade, not fail")
	require.NotNil(t, ov)
	require.Contains(t, ov.Degraded, "tmdb_series")
	require.Empty(t, ov.Keywords)
}
