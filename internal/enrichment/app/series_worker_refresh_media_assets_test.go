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

// newMediaFixture wires a workerFixture pre-seeded for A4. canonTMDBID nil
// → Sonarr-only canon row (no TMDB fetch); attaches MediaResolver + Probe
// via a NewSeriesWorker re-construction so the constructor validation still
// runs against the augmented deps.
func newMediaFixture(t *testing.T, canonTMDBID *domain.TMDBID, probe freshener.Probe, resolver MediaResolver, tv *tmdb.TVResponse) *workerFixture {
	t.Helper()
	f := newWorkerFixture(t, tv, map[int]*tmdb.SeasonResponse{})
	deps := f.worker.deps
	deps.Probe = probe
	deps.MediaResolver = resolver
	w, err := NewSeriesWorker(deps)
	require.NoError(t, err)
	f.worker = w
	f.seedCanon(1, canonTMDBID)
	return f
}

// helper: canonical happy-path TV payload — one series-level poster+backdrop
// + 3 seasons (season 2 has empty poster to exercise the skip-empty filter).
func canonicalMediaTV(parentTMDB int64) *tmdb.TVResponse {
	return mediaTVPayload(parentTMDB, "/p.jpg", "/b.jpg", []tmdb.TVSeasonStub{
		{ID: 5001, SeasonNumber: 1, Name: "S1", Overview: "ov1", AirDate: "2020-01-01", PosterPath: "/s1.jpg"},
		{ID: 5002, SeasonNumber: 2, Name: "S2", Overview: "ov2", AirDate: "2020-02-01", PosterPath: ""}, // filtered
		{ID: 5003, SeasonNumber: 3, Name: "S3", Overview: "ov3", AirDate: "2020-03-01", PosterPath: "/s3.jpg"},
	})
}

// ---- behavior tests ------------------------------------------------

func TestSeriesWorker_RefreshMediaAssets_InvalidLang_Error(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newMediaFixture(t, &tmdbID, nil, &fakeMediaResolver{}, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "not-a-lang", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid lang")
	assert.Zero(t, f.tmdb.getTVHit, "invalid lang MUST short-circuit before TMDB")
	assert.False(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"))
}

func TestSeriesWorker_RefreshMediaAssets_NoTMDBID_Skip(t *testing.T) {
	t.Parallel()
	f := newMediaFixture(t, nil, nil, &fakeMediaResolver{}, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Zero(t, f.tmdb.getTVHit, "TMDB MUST NOT be called when canon.TMDBID is nil")
	assert.False(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"))
}

func TestSeriesWorker_RefreshMediaAssets_SeriesMissing_Skip(t *testing.T) {
	t.Parallel()
	f := newMediaFixture(t, nil, nil, &fakeMediaResolver{}, canonicalMediaTV(42))
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
	f := newMediaFixture(t, &tmdbID, probe, resolver, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Equal(t, 1, probe.calls, "Probe MUST be consulted")
	assert.Zero(t, f.tmdb.getTVHit, "Probe fresh + force=false → skip TMDB")
	assert.False(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"))
	assert.Empty(t, resolver.calls, "no resolver calls when TTL gates the fetch")
}

func TestSeriesWorker_RefreshMediaAssets_ForceTrue_ProbeFresh_Bypass(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{verdicts: []freshener.SectionVerdict{
		{Section: freshener.SectionMedia, Stale: false, Reason: "fresh"},
	}}
	resolver := &fakeMediaResolver{}
	f := newMediaFixture(t, &tmdbID, probe, resolver, canonicalMediaTV(42))
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
	f := newMediaFixture(t, &tmdbID, nil, resolver, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit, "Probe nil → unconditional fetch")
	assert.True(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"))
}

func TestSeriesWorker_RefreshMediaAssets_HappyPath(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	resolver := &fakeMediaResolver{}
	f := newMediaFixture(t, &tmdbID, nil, resolver, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit, "exactly 1 GetTV call")

	// Series canon upsert fired once. S-E3a — canon no longer carries
	// poster/backdrop; series art flows to series_media_texts + the media
	// pipeline via MediaResolver (asserted below). The narrow writer only
	// stamps enrichment_media_synced_at here.
	require.Equal(t, 1, f.series.upsertN, "exactly 1 Series.Upsert call")
	persisted := f.series.rows[1]
	require.NotNil(t, persisted.EnrichmentMediaSyncedAt)

	// Seasons: 2 upserts (season 2 dropped by skip-empty filter).
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

	// S-E3a — canon season no longer carries Name/Overview/PosterAsset; the
	// narrow writer keeps only air_date + tmdb_season_id fresh (per-season art
	// flows to season_media_texts + the media pipeline, asserted via the
	// resolver below).
	s1 := f.seasons.rows[1]
	require.NotNil(t, s1.AirDate)
	require.NotNil(t, s1.TMDBSeasonID)
	assert.Equal(t, 5001, *s1.TMDBSeasonID)

	// MediaResolver calls: 2 poster sizes + 1 backdrop + 2 season posters = 5.
	// S-E3a — this is now the ONLY path A4 pushes series/season poster art into
	// the media pipeline (canon media columns dropped).
	require.Len(t, resolver.calls, 5)
	assert.Equal(t, "poster_w342", resolver.calls[0].Kind)
	require.NotNil(t, resolver.calls[0].Path)
	assert.Equal(t, "/p.jpg", *resolver.calls[0].Path)
	assert.Equal(t, "poster_w780", resolver.calls[1].Kind)
	assert.Equal(t, "backdrop_w1280", resolver.calls[2].Kind)
	require.NotNil(t, resolver.calls[2].Path)
	assert.Equal(t, "/b.jpg", *resolver.calls[2].Path)
	assert.Equal(t, "season_poster_w154", resolver.calls[3].Kind)
	require.NotNil(t, resolver.calls[3].Path)
	assert.Equal(t, "/s1.jpg", *resolver.calls[3].Path)
	assert.Equal(t, "season_poster_w154", resolver.calls[4].Kind)
}

// Story 552 mirror class — writer MUST always populate PosterAsset for every
// season in the payload; the skip-if-empty filter is the only reason a nil
// value would ever reach seasons_repository.
func TestSeriesWorker_RefreshMediaAssets_SeasonsPosterAlwaysPopulated(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tv := canonicalMediaTV(42)
	resolver := &fakeMediaResolver{}
	f := newMediaFixture(t, &tmdbID, nil, resolver, tv)
	require.NoError(t, f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true))
	// S-E3a — season poster no longer lands on canon; the skip-if-empty filter
	// guarantees every season poster reaching the media pipeline is non-empty
	// (Story 552 mirror class: never resolve a blank path).
	seasonPosterCalls := 0
	for _, c := range resolver.calls {
		if c.Kind != "season_poster_w154" {
			continue
		}
		seasonPosterCalls++
		require.NotNilf(t, c.Path, "season poster resolve with nil path — Story 552 regression!")
		assert.NotEmptyf(t, *c.Path, "season poster resolve with empty path — Story 552 regression!")
	}
	assert.Equal(t, 2, seasonPosterCalls, "season 2 empty PosterPath must be skipped")
	require.NotContains(t, f.seasons.rows, 2, "empty-poster season must not reach seasons_repo")
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

	f := newMediaFixture(t, &tmdbID, nil, &fakeMediaResolver{}, tv)
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
	f := newMediaFixture(t, &tmdbID, nil, resolver, canonicalMediaTV(42))
	f.tmdb.tvErr = errors.New("tmdb 500")
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true)
	require.Error(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"), "stamp MUST NOT fire on TMDB error")
	assert.Empty(t, resolver.calls, "resolver MUST NOT be called on TMDB error")
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

func TestSeriesWorker_RefreshMediaAssets_SeriesUpsertError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	resolver := &fakeMediaResolver{}
	f := newMediaFixture(t, &tmdbID, nil, resolver, canonicalMediaTV(42))
	f.worker.deps.Series = &erroringSeriesRepo{
		inner: f.series,
		err:   errors.New("series upsert boom"),
	}
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true)
	require.Error(t, err)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"), "stamp MUST NEVER be written without successful canon upsert")
	assert.Empty(t, resolver.calls, "resolver called only post-tx-commit — must be zero on rollback")
}

func TestSeriesWorker_RefreshMediaAssets_NilMediaResolver_HappyPath(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newMediaFixture(t, &tmdbID, nil, nil, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err, "nil MediaResolver MUST be tolerated")
	assert.True(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"), "writes still fire without resolver")
	require.NotNil(t, f.series.rows[1].EnrichmentMediaSyncedAt)
}

func TestSeriesWorker_RefreshMediaAssets_EmptyPaths_NoResolverCalls(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	tv := mediaTVPayload(42, "", "", nil)
	resolver := &fakeMediaResolver{}
	f := newMediaFixture(t, &tmdbID, nil, resolver, tv)
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err)
	assert.True(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"),
		"stamp MUST fire on empty TMDB media response (no-work-needed sync)")
	assert.Empty(t, resolver.calls, "no non-empty paths → zero resolver calls")
}

func TestSeriesWorker_RefreshMediaAssets_ProbeError_FailOpen(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{err: errors.New("probe boom")}
	resolver := &fakeMediaResolver{}
	f := newMediaFixture(t, &tmdbID, probe, resolver, canonicalMediaTV(42))
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err, "probe error → fail-open")
	assert.Equal(t, 1, probe.calls)
	assert.Equal(t, 1, f.tmdb.getTVHit, "fail-open → still fetches")
	assert.True(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"))
}

// TestSeriesWorker_RefreshMediaAssets_NilTMDBResponse_Skip — GetTV returns
// (nil, nil) → worker WARN + no tx, no stamp, no resolver.
func TestSeriesWorker_RefreshMediaAssets_NilTMDBResponse_Skip(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	resolver := &fakeMediaResolver{}
	f := newMediaFixture(t, &tmdbID, nil, resolver, nil)
	// fakeTMDB.tv nil → GetTV returns (nil, nil) per current stub shape.
	err := f.worker.RefreshMediaAssets(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkMediaSynced"), "no stamp on nil response")
	assert.Empty(t, resolver.calls)
}
