package enrichment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// ---- fakes specific to the A2 narrow methods -------------------------

// fakeProbe records the IsStale call args and returns canned verdicts.
// A single err is returned if set (covers fail-open behaviour).
type fakeProbe struct {
	verdicts []freshener.SectionVerdict
	err      error
	calls    int
}

func (f *fakeProbe) IsStale(_ context.Context, _ domain.SeriesID, _ values.LanguageTag, _ []int) ([]freshener.SectionVerdict, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.verdicts, nil
}

// errorSeriesTextsRepo wraps fakeSeriesTextsRepo and triggers an
// injected error on Upsert. Used to assert tx rollback drops the stamp.
type errorSeriesTextsRepo struct {
	*fakeSeriesTextsRepo
	err error
}

func (e *errorSeriesTextsRepo) Upsert(ctx context.Context, t series.SeriesText) error {
	if e.err != nil {
		return e.err
	}
	return e.fakeSeriesTextsRepo.Upsert(ctx, t)
}

// newRefreshFixture builds a workerFixture pre-seeded for narrow refresh
// tests. canonTMDBID nil → seedCanon installs a Sonarr-only canon row
// (no tmdb_id). probe nil OK — Worker's Probe field stays nil.
func newRefreshFixture(t *testing.T, canonTMDBID *domain.TMDBID, probe freshener.Probe) *workerFixture {
	t.Helper()
	// minimalTV is the deterministic GetTV response. Seeded into the fake
	// even when canonTMDBID is nil — the worker short-circuits before
	// dialling so the response is unused there.
	f := newWorkerFixture(t, minimalTV(), map[int]*tmdb.SeasonResponse{})
	// Re-build worker with the optional Probe field set. NewSeriesWorker
	// keeps the rest of the deps identical.
	deps := f.worker.deps
	deps.Probe = probe
	w, err := NewSeriesWorker(deps)
	require.NoError(t, err)
	f.worker = w
	// Seed canon. id=1 keeps the existing test parity.
	f.seedCanon(1, canonTMDBID)
	return f
}

// ---- tests ----------------------------------------------------------

func TestSeriesWorker_RefreshSeriesText_NoTMDBID_Skip(t *testing.T) {
	t.Parallel()
	f := newRefreshFixture(t, nil, nil)
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Zero(t, f.tmdb.getTVHit, "TMDB MUST NOT be called when canon.TMDBID is nil")
	assert.Empty(t, f.seriesTexts.rows, "series_texts MUST stay empty when canon.TMDBID is nil")
	assert.False(t, hasCall(f.rec.list(), "Series.MarkTextSynced"), "stamp MUST NOT be written")
}

func TestSeriesWorker_RefreshSeriesText_SeriesMissing_Skip(t *testing.T) {
	t.Parallel()
	f := newRefreshFixture(t, nil, nil)
	// Wipe the seeded canon so Series.Get returns SeriesNotFoundError.
	delete(f.series.rows, 1)
	// fakeSeriesRepo.Get returns ports.ErrNotFound — we need a
	// SeriesNotFoundError for the worker's errors.As branch. Wrap the
	// fake's Get via a tiny adapter.
	f.worker.deps.Series = &seriesNotFoundRepo{inner: f.series}
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Zero(t, f.tmdb.getTVHit)
	assert.Empty(t, f.seriesTexts.rows)
}

func TestSeriesWorker_RefreshSeriesText_InvalidLang_Error(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRefreshFixture(t, &tmdbID, nil)
	err := f.worker.RefreshSeriesText(context.Background(), 1, "not-a-lang", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid lang")
	assert.Zero(t, f.tmdb.getTVHit, "invalid lang MUST short-circuit before TMDB")
}

func TestSeriesWorker_RefreshSeriesText_ForceTrue_NoProbe_HappyPath(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRefreshFixture(t, &tmdbID, nil)
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit, "exactly 1 GetTV call")
	require.Len(t, f.seriesTexts.rows, 1, "exactly 1 series_texts row")
	row := f.seriesTexts.rows[0]
	assert.Equal(t, "ru-RU", row.Language)
	require.NotNil(t, row.Title)
	assert.Equal(t, "Show", *row.Title)
	assert.True(t, hasCall(f.rec.list(), "Series.MarkTextSynced"), "stamp MUST fire on success")
	// Verify the canon was stamped.
	persisted := f.series.rows[1]
	require.NotNil(t, persisted.EnrichmentTextSyncedAt)
}

func TestSeriesWorker_RefreshSeriesText_ForceFalse_NoProbe_HappyPath(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRefreshFixture(t, &tmdbID, nil)
	// force=false + Probe nil → fetch unconditionally.
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit, "Probe nil → unconditional fetch")
	require.NotNil(t, f.series.rows[1].EnrichmentTextSyncedAt)
}

func TestSeriesWorker_RefreshSeriesText_ForceFalse_ProbeFresh_Skip(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{verdicts: []freshener.SectionVerdict{
		{Section: freshener.SectionOverview, Stale: false, Reason: "fresh"},
	}}
	f := newRefreshFixture(t, &tmdbID, probe)
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Equal(t, 1, probe.calls, "Probe MUST be consulted")
	assert.Zero(t, f.tmdb.getTVHit, "Probe fresh + force=false → skip TMDB")
	assert.Empty(t, f.seriesTexts.rows)
}

func TestSeriesWorker_RefreshSeriesText_ForceTrue_ProbeFresh_Bypass(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{verdicts: []freshener.SectionVerdict{
		{Section: freshener.SectionOverview, Stale: false, Reason: "fresh"},
	}}
	f := newRefreshFixture(t, &tmdbID, probe)
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err)
	assert.Zero(t, probe.calls, "force=true MUST NOT consult Probe")
	assert.Equal(t, 1, f.tmdb.getTVHit, "force=true bypasses gate")
}

func TestSeriesWorker_RefreshSeriesText_ProbeError_FailOpen(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{err: errors.New("probe boom")}
	f := newRefreshFixture(t, &tmdbID, probe)
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err, "probe error → fail-open (no error propagated)")
	assert.Equal(t, 1, probe.calls)
	assert.Equal(t, 1, f.tmdb.getTVHit, "fail-open → still fetches")
	require.NotNil(t, f.series.rows[1].EnrichmentTextSyncedAt)
}

func TestSeriesWorker_RefreshSeriesText_OneLangOnly(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRefreshFixture(t, &tmdbID, nil)
	require.NoError(t, f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true))
	require.Equal(t, 1, f.tmdb.getTVHit, "exactly 1 GetTV call")
	require.Len(t, f.tmdb.getTVLangs, 1)
	assert.Equal(t, "ru-RU", f.tmdb.getTVLangs[0])
}

func TestSeriesWorker_RefreshSeriesText_TMDBError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRefreshFixture(t, &tmdbID, nil)
	f.tmdb.tvErr = errors.New("tmdb 500")
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true)
	require.Error(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.Empty(t, f.seriesTexts.rows, "tx never started")
	assert.False(t, hasCall(f.rec.list(), "Series.MarkTextSynced"), "stamp NOT written")
	assert.Nil(t, f.series.rows[1].EnrichmentTextSyncedAt)
}

func TestSeriesWorker_RefreshSeriesText_UpsertError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRefreshFixture(t, &tmdbID, nil)
	// Wrap the SeriesTexts repo to fail.
	f.worker.deps.SeriesTexts = &errorSeriesTextsRepo{
		fakeSeriesTextsRepo: f.seriesTexts,
		err:                 errors.New("upsert boom"),
	}
	err := f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true)
	require.Error(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkTextSynced"), "stamp NEVER written without successful UPSERT")
	assert.Nil(t, f.series.rows[1].EnrichmentTextSyncedAt)
}

// ---- C-posters-A (Story 584a) per-lang media populate --------------

// fakeSeriesMediaTextsRepo records every Upsert. `last` exposes the most
// recent row for single-write assertions.
type fakeSeriesMediaTextsRepo struct {
	rec  *callRecord
	rows []series.SeriesMediaText
}

func (f *fakeSeriesMediaTextsRepo) Upsert(_ context.Context, t series.SeriesMediaText) error {
	f.rec.add("SeriesMediaTexts.Upsert")
	f.rows = append(f.rows, t)
	return nil
}

func (f *fakeSeriesMediaTextsRepo) last() series.SeriesMediaText {
	return f.rows[len(f.rows)-1]
}

// newMediaTextFixture wires a refresh fixture with the per-lang media
// write port + a MediaResolver spy attached, plus a custom TV payload so
// the poster/backdrop paths are controllable. resolver / mediaTexts may be
// nil to exercise the nil-OK branches.
func newMediaTextFixture(
	t *testing.T,
	tmdbID *domain.TMDBID,
	tv *tmdb.TVResponse,
	resolver MediaResolver,
	mediaTexts SeriesMediaTextsRepo,
) *workerFixture {
	t.Helper()
	f := newWorkerFixture(t, tv, map[int]*tmdb.SeasonResponse{})
	deps := f.worker.deps
	deps.MediaResolver = resolver
	deps.SeriesMediaTexts = mediaTexts
	w, err := NewSeriesWorker(deps)
	require.NoError(t, err)
	f.worker = w
	f.seedCanon(1, tmdbID)
	return f
}

func TestSeriesWorker_RefreshSeriesText_PopulatesMediaRow(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tv := mediaTVPayload(42, "/ru.jpg", "/ru-bd.jpg", nil)
	resolver := &fakeMediaResolver{}
	media := &fakeSeriesMediaTextsRepo{rec: nil}
	f := newMediaTextFixture(t, &tmdbID, tv, resolver, media)
	media.rec = f.rec

	require.NoError(t, f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true))

	// series_texts + series_media_texts + stamp all committed in the same tx.
	require.Len(t, f.seriesTexts.rows, 1, "series_texts still written")
	require.Len(t, media.rows, 1, "exactly one series_media_texts row")
	assert.True(t, hasCall(f.rec.list(), "SeriesTexts.Upsert"))
	assert.True(t, hasCall(f.rec.list(), "SeriesMediaTexts.Upsert"))
	assert.True(t, hasCall(f.rec.list(), "Series.MarkTextSynced"))

	row := media.last()
	assert.Equal(t, domain.SeriesID(1), row.SeriesID)
	assert.Equal(t, "ru-RU", row.Language)
	require.NotNil(t, row.PosterAsset)
	assert.Equal(t, "/ru.jpg", *row.PosterAsset)
	require.NotNil(t, row.BackdropAsset)
	assert.Equal(t, "/ru-bd.jpg", *row.BackdropAsset)
	require.NotNil(t, row.EnrichedAt, "EnrichedAt stamped on TMDB write")
	// Under the unified-resolve contract the spy returns a non-nil hash.
	require.NotNil(t, row.PosterHash)
	require.NotNil(t, row.BackdropHash)

	// MediaResolver called with the poster (w342) + backdrop (w1280) sizes.
	require.Len(t, resolver.calls, 2)
	assert.Equal(t, "w342", resolver.calls[0].Size)
	assert.Equal(t, "poster_w342", resolver.calls[0].Kind)
	require.NotNil(t, resolver.calls[0].Path)
	assert.Equal(t, "/ru.jpg", *resolver.calls[0].Path)
	assert.Equal(t, "w1280", resolver.calls[1].Size)
	assert.Equal(t, "backdrop_w1280", resolver.calls[1].Kind)
	require.NotNil(t, resolver.calls[1].Path)
	assert.Equal(t, "/ru-bd.jpg", *resolver.calls[1].Path)
}

func TestSeriesWorker_RefreshSeriesText_NilMediaTexts_NoPanic(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tv := mediaTVPayload(42, "/ru.jpg", "/ru-bd.jpg", nil)
	// SeriesMediaTexts nil → media step skipped; series_texts still written.
	f := newMediaTextFixture(t, &tmdbID, tv, &fakeMediaResolver{}, nil)
	require.NoError(t, f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true))
	require.Len(t, f.seriesTexts.rows, 1, "series_texts written despite nil media port")
	assert.False(t, hasCall(f.rec.list(), "SeriesMediaTexts.Upsert"), "media upsert skipped when port nil")
	assert.True(t, hasCall(f.rec.list(), "Series.MarkTextSynced"))
}

func TestSeriesWorker_RefreshSeriesText_EmptyPaths_ResolverNotCalled(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tv := mediaTVPayload(42, "", "", nil) // no poster/backdrop
	resolver := &fakeMediaResolver{}
	media := &fakeSeriesMediaTextsRepo{}
	f := newMediaTextFixture(t, &tmdbID, tv, resolver, media)
	media.rec = f.rec

	require.NoError(t, f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true))
	assert.Empty(t, resolver.calls, "Resolve NOT called for empty paths")
	require.Len(t, media.rows, 1, "media row still written (nil asset/hash)")
	row := media.last()
	assert.Nil(t, row.PosterAsset)
	assert.Nil(t, row.BackdropAsset)
	assert.Nil(t, row.PosterHash)
	assert.Nil(t, row.BackdropHash)
	require.NotNil(t, row.EnrichedAt)
}

func TestSeriesWorker_RefreshSeriesText_NilResolver_AssetStoredHashNil(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tv := mediaTVPayload(42, "/ru.jpg", "/ru-bd.jpg", nil)
	media := &fakeSeriesMediaTextsRepo{}
	// nil MediaResolver → asset paths stored, hashes stay nil.
	f := newMediaTextFixture(t, &tmdbID, tv, nil, media)
	media.rec = f.rec

	require.NoError(t, f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true))
	require.Len(t, media.rows, 1)
	row := media.last()
	require.NotNil(t, row.PosterAsset)
	assert.Equal(t, "/ru.jpg", *row.PosterAsset)
	require.NotNil(t, row.BackdropAsset)
	assert.Equal(t, "/ru-bd.jpg", *row.BackdropAsset)
	assert.Nil(t, row.PosterHash, "nil resolver → poster_hash nil")
	assert.Nil(t, row.BackdropHash, "nil resolver → backdrop_hash nil")
}

// seriesNotFoundRepo wraps fakeSeriesRepo and returns
// SeriesNotFoundError on Get to exercise the errors.As branch in the
// worker. Other methods delegate to the inner fake.
type seriesNotFoundRepo struct {
	inner *fakeSeriesRepo
}

func (s *seriesNotFoundRepo) Get(_ context.Context, id domain.SeriesID) (series.Canon, error) {
	return series.Canon{}, &sharedErrors.SeriesNotFoundError{ID: id}
}

func (s *seriesNotFoundRepo) Upsert(ctx context.Context, c series.Canon) (domain.SeriesID, error) {
	return s.inner.Upsert(ctx, c)
}

func (s *seriesNotFoundRepo) UpsertStub(ctx context.Context, c series.Canon) (domain.SeriesID, error) {
	return s.inner.UpsertStub(ctx, c)
}

func (s *seriesNotFoundRepo) MarkTMDBSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return s.inner.MarkTMDBSynced(ctx, id, now)
}

func (s *seriesNotFoundRepo) MarkOMDBSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return s.inner.MarkOMDBSynced(ctx, id, now)
}

func (s *seriesNotFoundRepo) MarkTextSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return s.inner.MarkTextSynced(ctx, id, now)
}

func (s *seriesNotFoundRepo) MarkCastSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return s.inner.MarkCastSynced(ctx, id, now)
}

func (s *seriesNotFoundRepo) MarkRecsSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return s.inner.MarkRecsSynced(ctx, id, now)
}

func (s *seriesNotFoundRepo) MarkMediaSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return s.inner.MarkMediaSynced(ctx, id, now)
}

func hasCall(list []string, target string) bool {
	for _, c := range list {
		if c == target {
			return true
		}
	}
	return false
}

// mediaTVPayloadWithImages builds a TV payload whose ROOT poster/backdrop are
// Russian while images[] carries EN + RU + language-agnostic entries — the
// S-A (#977) scenario. Used to prove the en-US writer selects the EN poster.
func mediaTVPayloadWithImages(parentTMDB int64) *tmdb.TVResponse {
	en := "en"
	ru := "ru"
	return &tmdb.TVResponse{
		ID:           parentTMDB,
		Name:         "FROM",
		Overview:     "ov",
		Status:       "Returning Series",
		FirstAirDate: "2022-02-20",
		PosterPath:   "/ru_root_ferma.jpg",
		BackdropPath: "/ru_root_backdrop.jpg",
		Images: &tmdb.TVImages{
			Posters: []tmdb.TVImage{
				{FilePath: "/en_high.jpg", ISO6391: &en, VoteAverage: 8.1, VoteCount: 40},
				{FilePath: "/ru_ferma.jpg", ISO6391: &ru, VoteAverage: 7.2, VoteCount: 11},
				{FilePath: "/neutral.jpg", ISO6391: nil, VoteAverage: 6.0, VoteCount: 9},
			},
			Backdrops: []tmdb.TVImage{
				{FilePath: "/neutral_bd.jpg", ISO6391: nil, VoteAverage: 5.5, VoteCount: 12},
				{FilePath: "/en_bd.jpg", ISO6391: &en, VoteAverage: 7.0, VoteCount: 30},
			},
		},
	}
}

// S-A (#977): en-US writer must store the EN poster from images[], NOT the
// Russian root poster_path.
func TestSeriesWorker_RefreshSeriesText_EnLang_PicksEnPosterFromImages(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(117648)
	tv := mediaTVPayloadWithImages(117648)
	resolver := &fakeMediaResolver{}
	media := &fakeSeriesMediaTextsRepo{}
	f := newMediaTextFixture(t, &tmdbID, tv, resolver, media)
	media.rec = f.rec

	require.NoError(t, f.worker.RefreshSeriesText(context.Background(), 1, "en-US", true))

	require.Len(t, media.rows, 1)
	row := media.last()
	assert.Equal(t, "en-US", row.Language)
	require.NotNil(t, row.PosterAsset)
	assert.Equal(t, "/en_high.jpg", *row.PosterAsset, "EN poster from images[], not RU root")
	require.NotNil(t, row.BackdropAsset)
	assert.Equal(t, "/neutral_bd.jpg", *row.BackdropAsset, "neutral backdrop preferred")
	// Resolver saw the EN poster path, not the RU root.
	require.NotEmpty(t, resolver.calls)
	require.NotNil(t, resolver.calls[0].Path)
	assert.Equal(t, "/en_high.jpg", *resolver.calls[0].Path)
}

// S-A: ru-RU writer stores the RU poster from images[].
func TestSeriesWorker_RefreshSeriesText_RuLang_PicksRuPosterFromImages(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(117648)
	tv := mediaTVPayloadWithImages(117648)
	media := &fakeSeriesMediaTextsRepo{}
	f := newMediaTextFixture(t, &tmdbID, tv, &fakeMediaResolver{}, media)
	media.rec = f.rec

	require.NoError(t, f.worker.RefreshSeriesText(context.Background(), 1, "ru-RU", true))
	require.Len(t, media.rows, 1)
	row := media.last()
	require.NotNil(t, row.PosterAsset)
	assert.Equal(t, "/ru_ferma.jpg", *row.PosterAsset)
}

// S-A negative: no images[] → per-lang writer falls back to the root path
// (not empty), so a TMDB-minimal series still gets a poster.
func TestSeriesWorker_RefreshSeriesText_NoImages_RootFallback(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tv := mediaTVPayload(42, "/root.jpg", "/root-bd.jpg", nil) // Images nil
	media := &fakeSeriesMediaTextsRepo{}
	f := newMediaTextFixture(t, &tmdbID, tv, &fakeMediaResolver{}, media)
	media.rec = f.rec

	require.NoError(t, f.worker.RefreshSeriesText(context.Background(), 1, "en-US", true))
	require.Len(t, media.rows, 1)
	row := media.last()
	require.NotNil(t, row.PosterAsset)
	assert.Equal(t, "/root.jpg", *row.PosterAsset, "no images[] → root poster fallback")
	require.NotNil(t, row.BackdropAsset)
	assert.Equal(t, "/root-bd.jpg", *row.BackdropAsset)
}
