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
)

// mediaResolveCall records a MediaResolver.Resolve invocation for
// assertion. Path is a snapshot of the caller-supplied rawPath — the
// worker may mutate its own copy after the return, so we defensively deep
// copy the string value here.
type mediaResolveCall struct {
	Path *string
	Size string
	Kind string
}

// fakeMediaResolver counts Resolve calls with (path, size, kind) tuples.
// Under Story 347 unified-resolve contract the resolver returns a *string
// sha256-hex hash on non-empty rawPath and nil otherwise; we mirror that
// shape verbatim so A4 tests can exercise the nil-return path.
type fakeMediaResolver struct {
	calls []mediaResolveCall
}

func (f *fakeMediaResolver) Resolve(_ context.Context, rawPath *string, size, kind string) *string {
	var pathCopy *string
	if rawPath != nil {
		v := *rawPath
		pathCopy = &v
	}
	f.calls = append(f.calls, mediaResolveCall{Path: pathCopy, Size: size, Kind: kind})
	if rawPath == nil || *rawPath == "" {
		return nil
	}
	h := "sha256:mock:" + *rawPath
	return &h
}

// mediaTVPayload builds a TMDB response carrying series-level poster +
// backdrop paths + N season stubs. Empty seasonPosters means no seasons.
func mediaTVPayload(parentTMDB int64, posterPath, backdropPath string, seasons []tmdb.TVSeasonStub) *tmdb.TVResponse {
	return &tmdb.TVResponse{
		ID:           parentTMDB,
		Name:         "Media Fixture",
		Overview:     "ov",
		Status:       "Returning Series",
		FirstAirDate: "2020-01-01",
		PosterPath:   posterPath,
		BackdropPath: backdropPath,
		Seasons:      seasons,
	}
}

// fakeSeasonMediaTextsRepo records every season_media_texts Upsert for A4
// assertions. Mirrors fakeSeriesMediaTextsRepo (series_worker_refresh_text_test.go).
type fakeSeasonMediaTextsRepo struct {
	rec  *callRecord
	rows []series.SeasonMediaText
}

func (f *fakeSeasonMediaTextsRepo) Upsert(_ context.Context, t series.SeasonMediaText) error {
	if f.rec != nil {
		f.rec.add("SeasonMediaTexts.Upsert")
	}
	f.rows = append(f.rows, t)
	return nil
}

// mediaCaptures bundles the two per-lang media-text capture fakes so tests can
// assert on the rows A4 wrote.
type mediaCaptures struct {
	seriesMedia *fakeSeriesMediaTextsRepo
	seasonMedia *fakeSeasonMediaTextsRepo
}

// newMediaFixture wires a workerFixture pre-seeded for A4. canonTMDBID nil
// → Sonarr-only canon row (no TMDB fetch); attaches MediaResolver + Probe +
// the series/season media-text capture fakes via a NewSeriesWorker
// re-construction so the constructor validation still runs against the
// augmented deps.
func newMediaFixture(t *testing.T, canonTMDBID *domain.TMDBID, probe freshener.Probe, resolver MediaResolver, tv *tmdb.TVResponse) (*workerFixture, *mediaCaptures) {
	t.Helper()
	f := newWorkerFixture(t, tv, map[int]*tmdb.SeasonResponse{})
	caps := &mediaCaptures{
		seriesMedia: &fakeSeriesMediaTextsRepo{rec: f.rec},
		seasonMedia: &fakeSeasonMediaTextsRepo{rec: f.rec},
	}
	deps := f.worker.deps
	deps.Probe = probe
	deps.MediaResolver = resolver
	deps.SeriesMediaTexts = caps.seriesMedia
	deps.SeasonMediaTexts = caps.seasonMedia
	w, err := NewSeriesWorker(deps)
	require.NoError(t, err)
	f.worker = w
	f.seedCanon(1, canonTMDBID)
	return f, caps
}

// helper: canonical happy-path TV payload — one series-level poster+backdrop
// + 3 seasons (season 2 has empty poster to exercise the skip-empty filter).
// Images is nil, so the base en-US series_media_texts row falls back to the root
// poster/backdrop. The non-base ru-RU poster stays STRICT (Images nil → nil
// poster), but W18-15 gives its backdrop the neutral→lang→en→root ladder, so the
// ru row is written with a root-fallback backdrop (parity with RefreshSeriesAllLangs).
func canonicalMediaTV(parentTMDB int64) *tmdb.TVResponse {
	return mediaTVPayload(parentTMDB, "/p.jpg", "/b.jpg", []tmdb.TVSeasonStub{
		{ID: 5001, SeasonNumber: 1, Name: "S1", Overview: "ov1", AirDate: "2020-01-01", PosterPath: "/s1.jpg"},
		{ID: 5002, SeasonNumber: 2, Name: "S2", Overview: "ov2", AirDate: "2020-02-01", PosterPath: ""}, // filtered
		{ID: 5003, SeasonNumber: 3, Name: "S3", Overview: "ov3", AirDate: "2020-03-01", PosterPath: "/s3.jpg"},
	})
}

// mediaTVWithImages builds a canonical payload but attaches an images[] block
// carrying a per-language poster set (en + ru) so the strict non-base picker
// has ru art to write.
func mediaTVWithImages(parentTMDB int64) *tmdb.TVResponse {
	tv := canonicalMediaTV(parentTMDB)
	en := "en"
	ru := "ru"
	tv.Images = &tmdb.TVImages{
		Posters: []tmdb.TVImage{
			{FilePath: "/en_poster.jpg", ISO6391: &en, VoteAverage: 8.0, VoteCount: 40},
			{FilePath: "/ru_poster.jpg", ISO6391: &ru, VoteAverage: 7.0, VoteCount: 20},
		},
		Backdrops: []tmdb.TVImage{
			{FilePath: "/ru_bd.jpg", ISO6391: &ru, VoteAverage: 6.0, VoteCount: 10},
		},
	}
	return tv
}

// countKind counts resolver calls of a given kind.
func countKind(calls []mediaResolveCall, kind string) int {
	n := 0
	for _, c := range calls {
		if c.Kind == kind {
			n++
		}
	}
	return n
}

// ---- behavior tests ------------------------------------------------

func TestSeriesWorker_RefreshMediaAssets_InvalidLang_Error(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f, _ := newMediaFixture(t, &tmdbID, nil, &fakeMediaResolver{}, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "not-a-lang", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid lang")
	assert.Zero(t, f.tmdb.getTVHit, "invalid lang MUST short-circuit before TMDB")
	assert.False(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"))
}

func TestSeriesWorker_RefreshMediaAssets_NoTMDBID_Skip(t *testing.T) {
	t.Parallel()
	f, _ := newMediaFixture(t, nil, nil, &fakeMediaResolver{}, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Zero(t, f.tmdb.getTVHit, "TMDB MUST NOT be called when canon.TMDBID is nil")
	assert.False(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"))
}

func TestSeriesWorker_RefreshMediaAssets_SeriesMissing_Skip(t *testing.T) {
	t.Parallel()
	f, _ := newMediaFixture(t, nil, nil, &fakeMediaResolver{}, canonicalMediaTV(42))
	delete(f.series.rows, 1)
	f.worker.deps.Series = &seriesNotFoundRepo{inner: f.series}
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Zero(t, f.tmdb.getTVHit)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"))
}

func TestSeriesWorker_RefreshMediaAssets_TTLGate_Skip(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{verdicts: []freshener.SectionVerdict{
		{Section: freshener.SectionMedia, Stale: false, Reason: "fresh"},
	}}
	resolver := &fakeMediaResolver{}
	f, caps := newMediaFixture(t, &tmdbID, probe, resolver, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Equal(t, 1, probe.calls, "Probe MUST be consulted")
	assert.Zero(t, f.tmdb.getTVHit, "Probe fresh + force=false → skip TMDB")
	assert.False(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"))
	assert.Empty(t, resolver.calls, "no resolver calls when TTL gates the fetch")
	assert.Empty(t, caps.seriesMedia.rows, "no media rows when gated")
	assert.Empty(t, caps.seasonMedia.rows)
}

func TestSeriesWorker_RefreshMediaAssets_ForceTrue_ProbeFresh_Bypass(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{verdicts: []freshener.SectionVerdict{
		{Section: freshener.SectionMedia, Stale: false, Reason: "fresh"},
	}}
	resolver := &fakeMediaResolver{}
	f, _ := newMediaFixture(t, &tmdbID, probe, resolver, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err)
	assert.Zero(t, probe.calls, "force=true MUST NOT consult Probe")
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.True(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"))
}

func TestSeriesWorker_RefreshMediaAssets_NoProbe_HappyPath(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	resolver := &fakeMediaResolver{}
	f, _ := newMediaFixture(t, &tmdbID, nil, resolver, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit, "Probe nil → unconditional fetch")
	assert.True(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"))
}

// HappyPath — canonicalMediaTV has Images=nil, PosterPath=/p.jpg,
// BackdropPath=/b.jpg, seasons /s1.jpg + /s3.jpg (season 2 empty → filtered).
//   - base en-US series_media_texts row: poster /p.jpg (root fallback) +
//     backdrop /b.jpg → resolver poster_w342 + backdrop_w1280.
//   - ru-RU strict: Images nil → poster+backdrop nil → row SKIPPED (anti-poison).
//   - season_media_texts en-US: season 1 /s1.jpg + season 3 /s3.jpg → 2× poster_w342.
//
// So resolver calls = 4 (series poster + series backdrop + 2 season posters),
// series_media rows = 1 (en-US), season_media rows = 2. Code order is
// series-row(s) first, then seasons — assert exact ordering.
func TestSeriesWorker_RefreshMediaAssets_HappyPath(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	resolver := &fakeMediaResolver{}
	f, caps := newMediaFixture(t, &tmdbID, nil, resolver, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit, "exactly 1 GetTVAllLangs call")

	// Series canon preservation upsert fired once + stamp landed.
	require.Equal(t, 1, f.series.upsertN, "exactly 1 Series.Upsert call")
	persisted := f.series.rows[1]
	require.NotNil(t, persisted.EnrichmentMediaSyncedAt)

	// Seasons: 2 canon upserts (season 2 dropped by skip-empty filter).
	seasonUpsertCount := 0
	for _, c := range f.rec.list() {
		if c == "Seasons.Upsert" {
			seasonUpsertCount++
		}
	}
	assert.Equal(t, 2, seasonUpsertCount, "seasons with empty PosterPath MUST be skipped")
	require.Contains(t, f.seasons.rows, 1)
	require.Contains(t, f.seasons.rows, 3)
	require.NotContains(t, f.seasons.rows, 2, "season 2 empty PosterPath → row skipped entirely")

	s1 := f.seasons.rows[1]
	require.NotNil(t, s1.AirDate)
	require.NotNil(t, s1.TMDBSeasonID)
	assert.Equal(t, 5001, *s1.TMDBSeasonID)

	// series_media_texts: en-US (root poster+backdrop) + ru-RU (poster strict
	// nil, backdrop root-fallback /b.jpg — W18-15 non-base backdrop parity).
	require.Len(t, caps.seriesMedia.rows, 2, "en-US base row + ru-RU root-fallback backdrop row")
	byLang := map[string]series.SeriesMediaText{}
	for _, r := range caps.seriesMedia.rows {
		byLang[r.Language] = r
	}
	require.Contains(t, byLang, "en-US")
	require.Contains(t, byLang, "ru-RU")
	smt := byLang["en-US"]
	require.NotNil(t, smt.PosterAsset)
	assert.Equal(t, "/p.jpg", *smt.PosterAsset, "base poster from root fallback")
	require.NotNil(t, smt.BackdropAsset)
	assert.Equal(t, "/b.jpg", *smt.BackdropAsset, "base backdrop from root fallback")
	require.NotNil(t, smt.EnrichedAt)
	// ru-RU: strict poster finds nothing (Images nil) → nil; backdrop falls to
	// the root (W18-15) so the row is written backdrop-only.
	assert.Nil(t, byLang["ru-RU"].PosterAsset, "no ru poster art → nil (poster stays strict)")
	require.NotNil(t, byLang["ru-RU"].BackdropAsset, "ru backdrop from root fallback (W18-15)")
	assert.Equal(t, "/b.jpg", *byLang["ru-RU"].BackdropAsset)

	// season_media_texts: 2 en-US rows (season 1 + 3).
	require.Len(t, caps.seasonMedia.rows, 2)
	byNum := map[int]series.SeasonMediaText{}
	for _, r := range caps.seasonMedia.rows {
		byNum[r.SeasonNumber] = r
	}
	require.Contains(t, byNum, 1)
	require.Contains(t, byNum, 3)
	assert.Equal(t, "en-US", byNum[1].Language)
	require.NotNil(t, byNum[1].PosterAsset)
	assert.Equal(t, "/s1.jpg", *byNum[1].PosterAsset)

	// MediaResolver calls: en poster + en backdrop + ru backdrop (root) + 2
	// season posters = 5 (ru poster is nil → no poster resolve).
	require.Len(t, resolver.calls, 5)
	assert.Equal(t, "poster_w342", resolver.calls[0].Kind)
	require.NotNil(t, resolver.calls[0].Path)
	assert.Equal(t, "/p.jpg", *resolver.calls[0].Path)
	assert.Equal(t, "backdrop_w1280", resolver.calls[1].Kind)
	require.NotNil(t, resolver.calls[1].Path)
	assert.Equal(t, "/b.jpg", *resolver.calls[1].Path)
	assert.Equal(t, "backdrop_w1280", resolver.calls[2].Kind, "ru-RU root-fallback backdrop resolve (W18-15)")
	require.NotNil(t, resolver.calls[2].Path)
	assert.Equal(t, "/b.jpg", *resolver.calls[2].Path)
	assert.Equal(t, "poster_w342", resolver.calls[3].Kind)
	require.NotNil(t, resolver.calls[3].Path)
	assert.Equal(t, "/s1.jpg", *resolver.calls[3].Path)
	assert.Equal(t, "poster_w342", resolver.calls[4].Kind)
	require.NotNil(t, resolver.calls[4].Path)
	assert.Equal(t, "/s3.jpg", *resolver.calls[4].Path)
}

// A non-base language with a strict poster in images[] gets its OWN
// series_media_texts row (no poison, real ru art). Base en-US picks the en
// poster; ru-RU picks the ru poster + ru backdrop.
func TestSeriesWorker_RefreshMediaAssets_RuStrictImages_RuRowWritten(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	resolver := &fakeMediaResolver{}
	f, caps := newMediaFixture(t, &tmdbID, nil, resolver, mediaTVWithImages(42))
	require.NoError(t, f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true))

	require.Len(t, caps.seriesMedia.rows, 2, "en-US + ru-RU rows both written")
	byLang := map[string]series.SeriesMediaText{}
	for _, r := range caps.seriesMedia.rows {
		byLang[r.Language] = r
	}
	require.Contains(t, byLang, "en-US")
	require.Contains(t, byLang, "ru-RU")

	require.NotNil(t, byLang["en-US"].PosterAsset)
	assert.Equal(t, "/en_poster.jpg", *byLang["en-US"].PosterAsset, "base picks en poster")
	require.NotNil(t, byLang["ru-RU"].PosterAsset)
	assert.Equal(t, "/ru_poster.jpg", *byLang["ru-RU"].PosterAsset, "ru strict picks ru poster")
	require.NotNil(t, byLang["ru-RU"].BackdropAsset)
	assert.Equal(t, "/ru_bd.jpg", *byLang["ru-RU"].BackdropAsset, "ru strict picks ru backdrop")
}

// Story 552 mirror class — season_media_texts rows must always carry a
// non-empty poster; the skip-if-empty filter guarantees no blank path reaches
// the writer. Season poster kind is now poster_w342 (was season_poster_w154).
func TestSeriesWorker_RefreshMediaAssets_SeasonsPosterAlwaysPopulated(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tv := canonicalMediaTV(42)
	resolver := &fakeMediaResolver{}
	f, caps := newMediaFixture(t, &tmdbID, nil, resolver, tv)
	require.NoError(t, f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true))

	require.Len(t, caps.seasonMedia.rows, 2, "season 2 empty PosterPath must be skipped")
	for _, r := range caps.seasonMedia.rows {
		require.NotNilf(t, r.PosterAsset, "season media row with nil poster — Story 552 regression!")
		assert.NotEmptyf(t, *r.PosterAsset, "season media row with empty poster — Story 552 regression!")
		assert.Equal(t, "en-US", r.Language)
	}
	require.NotContains(t, f.seasons.rows, 2, "empty-poster season must not reach seasons_repo")
	// Season-poster resolves are w342 now (2 season posters) + series poster w342.
	assert.Equal(t, 3, countKind(resolver.calls, "poster_w342"),
		"1 series poster + 2 season posters, all w342")
}

// Universal narrow-writer audit — proves the 6 RISK fields (tvdb_id,
// imdb_id, next_air_date, year, runtime_minutes, in_production) are
// preserved from canon.Get into canonPayload, defending against bare
// excluded.X in seriesUpsertAssignments blanking previously-merged values.
func TestSeriesWorker_RefreshMediaAssets_PreservesCanonFields(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tvdbID := domain.TVDBID(7777)
	imdbID := domain.IMDBID("tt1234567")
	nextAir := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	year := 2024
	runtime := 47
	tv := canonicalMediaTV(42)

	f, _ := newMediaFixture(t, &tmdbID, nil, &fakeMediaResolver{}, tv)
	// Enrich the seeded canon with the 6 RISK fields; A4's writer must
	// copy each one into canonPayload so bare excluded doesn't blank.
	c := f.series.rows[1]
	c.TVDBID = &tvdbID
	c.IMDBID = &imdbID
	c.NextAirDate = &nextAir
	c.Year = &year
	c.RuntimeMinutes = &runtime
	c.InProduction = true
	f.series.rows[1] = c

	require.NoError(t, f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true))

	// The 6 preservation fields must be present in the payload passed to
	// Series.Upsert (fakeSeriesRepo stores the last-write value verbatim).
	written := f.series.rows[1]
	require.NotNil(t, written.TVDBID)
	assert.Equal(t, tvdbID, *written.TVDBID, "tvdb_id preservation copy missing — bare excluded would blank")
	require.NotNil(t, written.IMDBID)
	assert.Equal(t, imdbID, *written.IMDBID, "imdb_id preservation copy missing")
	require.NotNil(t, written.NextAirDate)
	assert.Equal(t, nextAir.Unix(), written.NextAirDate.Unix(), "next_air_date preservation copy missing")
	require.NotNil(t, written.Year)
	assert.Equal(t, year, *written.Year, "year preservation copy missing (Sonarr year authority still overridden by next scan)")
	require.NotNil(t, written.RuntimeMinutes)
	assert.Equal(t, runtime, *written.RuntimeMinutes, "runtime_minutes preservation copy missing")
	assert.True(t, written.InProduction, "in_production preservation copy missing (bool zero-value overwrite class)")
}

func TestSeriesWorker_RefreshMediaAssets_TMDBError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	resolver := &fakeMediaResolver{}
	f, caps := newMediaFixture(t, &tmdbID, nil, resolver, canonicalMediaTV(42))
	f.tmdb.tvErr = errors.New("tmdb 500")
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true)
	require.Error(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"), "stamp MUST NOT fire on TMDB error")
	assert.Empty(t, resolver.calls, "resolver MUST NOT be called on TMDB error (returns before build)")
	assert.Empty(t, caps.seriesMedia.rows)
	assert.Nil(t, f.series.rows[1].EnrichmentMediaSyncedAt)
}

// erroringSeriesRepo wraps fakeSeriesRepo to inject an Upsert error while
// leaving Get/MarkMediaSynced intact — exercises the rollback branch.
type erroringSeriesRepo struct {
	inner *fakeSeriesRepo
	err   error
}

func (e *erroringSeriesRepo) Get(ctx context.Context, id domain.SeriesID) (series.Canon, error) {
	return e.inner.Get(ctx, id)
}
func (e *erroringSeriesRepo) Upsert(ctx context.Context, c series.Canon) (domain.SeriesID, error) {
	if e.err != nil {
		return 0, e.err
	}
	return e.inner.Upsert(ctx, c)
}
func (e *erroringSeriesRepo) UpsertStub(ctx context.Context, c series.Canon) (domain.SeriesID, error) {
	return e.inner.UpsertStub(ctx, c)
}
func (e *erroringSeriesRepo) MarkTMDBSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return e.inner.MarkTMDBSynced(ctx, id, now)
}
func (e *erroringSeriesRepo) MarkOMDBSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return e.inner.MarkOMDBSynced(ctx, id, now)
}
func (e *erroringSeriesRepo) MarkTextSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return e.inner.MarkTextSynced(ctx, id, now)
}
func (e *erroringSeriesRepo) MarkCastSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return e.inner.MarkCastSynced(ctx, id, now)
}
func (e *erroringSeriesRepo) MarkRecsSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return e.inner.MarkRecsSynced(ctx, id, now)
}
func (e *erroringSeriesRepo) MarkMediaSynced(ctx context.Context, id domain.SeriesID, now time.Time) error {
	return e.inner.MarkMediaSynced(ctx, id, now)
}

// UpdateOMDbColumns — W18-6: no-op stub to satisfy SeriesRepo.
func (e *erroringSeriesRepo) UpdateOMDbColumns(_ context.Context, _ domain.SeriesID, _ *float64, _ *int, _ *string, _ *string) error {
	return nil
}

func TestSeriesWorker_RefreshMediaAssets_SeriesUpsertError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	resolver := &fakeMediaResolver{}
	f, _ := newMediaFixture(t, &tmdbID, nil, resolver, canonicalMediaTV(42))
	f.worker.deps.Series = &erroringSeriesRepo{
		inner: f.series,
		err:   errors.New("series upsert boom"),
	}
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true)
	require.Error(t, err)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"), "stamp MUST NEVER be written without successful canon upsert")
	// Resolver now runs during the OUTSIDE-tx build step (mirrors
	// RefreshSeriesAllLangs), so it HAS fired before the rollback — the stamp
	// guard, not the resolver count, is the rollback invariant here.
	assert.NotEmpty(t, resolver.calls, "resolver runs pre-tx during media-row build")
	assert.Nil(t, f.series.rows[1].EnrichmentMediaSyncedAt)
}

func TestSeriesWorker_RefreshMediaAssets_NilMediaResolver_HappyPath(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f, caps := newMediaFixture(t, &tmdbID, nil, nil, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err, "nil MediaResolver MUST be tolerated")
	assert.True(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"), "writes still fire without resolver")
	require.NotNil(t, f.series.rows[1].EnrichmentMediaSyncedAt)
	// en-US + ru-RU (W18-15 root-fallback backdrop) rows, raw paths only (no hashes).
	require.Len(t, caps.seriesMedia.rows, 2)
	byLang := map[string]series.SeriesMediaText{}
	for _, r := range caps.seriesMedia.rows {
		byLang[r.Language] = r
	}
	require.NotNil(t, byLang["en-US"].PosterAsset)
	assert.Nil(t, byLang["en-US"].PosterHash, "no resolver → no hash, raw path only")
	require.NotNil(t, byLang["ru-RU"].BackdropAsset, "ru backdrop from root fallback (W18-15)")
	assert.Equal(t, "/b.jpg", *byLang["ru-RU"].BackdropAsset)
	require.Len(t, caps.seasonMedia.rows, 2)
}

// EmptyPaths — payload with no poster/backdrop/images and no seasons: the base
// row would have poster nil AND backdrop nil → skip (a row with no art is
// useless). So zero media rows + zero resolver calls, but the stamp still fires
// (a no-work-needed sync).
func TestSeriesWorker_RefreshMediaAssets_EmptyPaths_NoRows(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tv := mediaTVPayload(42, "", "", nil)
	resolver := &fakeMediaResolver{}
	f, caps := newMediaFixture(t, &tmdbID, nil, resolver, tv)
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err)
	assert.True(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"),
		"stamp MUST fire on empty TMDB media response (no-work-needed sync)")
	assert.Empty(t, resolver.calls, "no non-empty paths → zero resolver calls")
	assert.Empty(t, caps.seriesMedia.rows, "base row with poster+backdrop both nil → skipped")
	assert.Empty(t, caps.seasonMedia.rows)
}

func TestSeriesWorker_RefreshMediaAssets_ProbeError_FailOpen(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{err: errors.New("probe boom")}
	resolver := &fakeMediaResolver{}
	f, _ := newMediaFixture(t, &tmdbID, probe, resolver, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err, "probe error → fail-open")
	assert.Equal(t, 1, probe.calls)
	assert.Equal(t, 1, f.tmdb.getTVHit, "fail-open → still fetches")
	assert.True(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"))
}

// TestSeriesWorker_RefreshMediaAssets_NilTMDBResponse_Skip — GetTVAllLangs
// returns (nil, nil) → worker WARN + no tx, no stamp, no resolver.
func TestSeriesWorker_RefreshMediaAssets_NilTMDBResponse_Skip(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	resolver := &fakeMediaResolver{}
	f, caps := newMediaFixture(t, &tmdbID, nil, resolver, nil)
	// fakeTMDB.tv nil → GetTVAllLangs returns (nil, nil) per current stub shape.
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"), "no stamp on nil response")
	assert.Empty(t, resolver.calls)
	assert.Empty(t, caps.seriesMedia.rows)
}

// W18-15 — non-base backdrop parity: RefreshMediaAssets used a STRICT backdrop
// pick, leaving a poster-only ru-RU row (backdrop NULL) whenever TMDB carried a
// ru poster but only a neutral/en backdrop. The fix uses the same
// neutral→lang→en→root ladder as RefreshSeriesAllLangs, so the ru row carries a
// backdrop; the POSTER stays strict (per-lang art intentional).
func TestSeriesWorker_RefreshMediaAssets_NonBaseBackdrop_NeutralFallback(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	resolver := &fakeMediaResolver{}
	tv := mediaTVPayload(42, "/root_p.jpg", "/root_b.jpg", nil)
	en, ru := "en", "ru"
	tv.Images = &tmdb.TVImages{
		Posters: []tmdb.TVImage{
			{FilePath: "/en_poster.jpg", ISO6391: &en, VoteAverage: 8, VoteCount: 40},
			{FilePath: "/ru_poster.jpg", ISO6391: &ru, VoteAverage: 7, VoteCount: 20},
		},
		Backdrops: []tmdb.TVImage{
			{FilePath: "/neutral_bd.jpg", ISO6391: nil, VoteAverage: 6, VoteCount: 10}, // NO ru backdrop
		},
	}
	f, caps := newMediaFixture(t, &tmdbID, nil, resolver, tv)
	require.NoError(t, f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true))

	byLang := map[string]series.SeriesMediaText{}
	for _, r := range caps.seriesMedia.rows {
		byLang[r.Language] = r
	}
	require.Contains(t, byLang, "ru-RU", "ru-RU row must be written (poster present)")
	require.NotNil(t, byLang["ru-RU"].PosterAsset)
	assert.Equal(t, "/ru_poster.jpg", *byLang["ru-RU"].PosterAsset, "poster stays strict ru")
	require.NotNil(t, byLang["ru-RU"].BackdropAsset, "W18-15 — ru backdrop must NOT be NULL")
	assert.Equal(t, "/neutral_bd.jpg", *byLang["ru-RU"].BackdropAsset, "ru backdrop from neutral tier")
	require.NotNil(t, byLang["en-US"].PosterAsset)
	assert.Equal(t, "/en_poster.jpg", *byLang["en-US"].PosterAsset)
	require.NotNil(t, byLang["en-US"].BackdropAsset)
	assert.Equal(t, "/neutral_bd.jpg", *byLang["en-US"].BackdropAsset)
}

// W18-15 — when TMDB carries NO tagged backdrop at all, the non-base row still
// gets the ROOT backdrop (root-fallback parity with RefreshSeriesAllLangs).
func TestSeriesWorker_RefreshMediaAssets_NonBaseBackdrop_RootFallback(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	resolver := &fakeMediaResolver{}
	tv := mediaTVPayload(42, "/root_p.jpg", "/root_b.jpg", nil)
	ru := "ru"
	tv.Images = &tmdb.TVImages{
		Posters:   []tmdb.TVImage{{FilePath: "/ru_poster.jpg", ISO6391: &ru, VoteAverage: 7, VoteCount: 20}},
		Backdrops: nil, // no tagged backdrops anywhere
	}
	f, caps := newMediaFixture(t, &tmdbID, nil, resolver, tv)
	require.NoError(t, f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true))

	byLang := map[string]series.SeriesMediaText{}
	for _, r := range caps.seriesMedia.rows {
		byLang[r.Language] = r
	}
	require.Contains(t, byLang, "ru-RU")
	require.NotNil(t, byLang["ru-RU"].BackdropAsset, "root-fallback backdrop for non-base lang")
	assert.Equal(t, "/root_b.jpg", *byLang["ru-RU"].BackdropAsset)
	require.NotNil(t, byLang["ru-RU"].PosterAsset)
	assert.Equal(t, "/ru_poster.jpg", *byLang["ru-RU"].PosterAsset, "poster still strict ru")
}
