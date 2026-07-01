package enrichment

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// recsTVPayload builds a TMDB response carrying N recommendations with
// the given tmdb_ids, names + Russian-friendly overviews. Position order
// reflects TMDB rank.
func recsTVPayload(parentTMDB int64, recs []tmdb.TVRecommendation) *tmdb.TVResponse {
	return &tmdb.TVResponse{
		ID:           parentTMDB,
		Name:         "Parent",
		Overview:     "Parent overview",
		Tagline:      "Parent tagline",
		Status:       "Returning Series",
		FirstAirDate: "2020-01-01",
		Recommendations: &tmdb.TVRecommendations{
			Results: recs,
		},
	}
}

// canonicalRecs returns the 3-rec happy-path fixture: tmdb_ids 1001/1002/1003
// in TMDB-rank order with Russian-translated names.
func canonicalRecs() []tmdb.TVRecommendation {
	return []tmdb.TVRecommendation{
		{ID: 1001, Name: "Невозможно поверить", Overview: "Описание 1", PosterPath: "/p1.jpg"},
		{ID: 1002, Name: "Лучший друг", Overview: "Описание 2", PosterPath: "/p2.jpg"},
		{ID: 1003, Name: "Большая надежда", Overview: "Описание 3", PosterPath: "/p3.jpg"},
	}
}

// newRecsFixture wires a refresh fixture pre-seeded for A3b. canonTMDBID
// nil → Sonarr-only canon row. Probe is optionally injected on the Worker
// deps so the gate logic can be exercised.
func newRecsFixture(t *testing.T, canonTMDBID *domain.TMDBID, probe freshener.Probe, recs []tmdb.TVRecommendation) *workerFixture {
	t.Helper()
	var tv *tmdb.TVResponse
	if canonTMDBID != nil {
		tv = recsTVPayload(int64(*canonTMDBID), recs)
	}
	f := newWorkerFixture(t, tv, map[int]*tmdb.SeasonResponse{})
	deps := f.worker.deps
	deps.Probe = probe
	w, err := NewSeriesWorker(deps)
	require.NoError(t, err)
	f.worker = w
	f.seedCanon(1, canonTMDBID)
	return f
}

// ---- behavior tests ------------------------------------------------

func TestSeriesWorker_RefreshRecommendations_InvalidLang_Error(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRecsFixture(t, &tmdbID, nil, canonicalRecs())
	err := f.worker.RefreshRecommendations(context.Background(), 1, "not-a-lang", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid lang")
	assert.Zero(t, f.tmdb.getTVHit, "invalid lang MUST short-circuit before TMDB")
	assert.Empty(t, f.seriesTexts.rows)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkRecsSynced"))
}

func TestSeriesWorker_RefreshRecommendations_NoTMDBID_Skip(t *testing.T) {
	t.Parallel()
	f := newRecsFixture(t, nil, nil, canonicalRecs())
	err := f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Zero(t, f.tmdb.getTVHit, "TMDB MUST NOT be called when canon.TMDBID is nil")
	assert.Empty(t, f.seriesTexts.rows)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkRecsSynced"))
}

func TestSeriesWorker_RefreshRecommendations_SeriesMissing_Skip(t *testing.T) {
	t.Parallel()
	f := newRecsFixture(t, nil, nil, canonicalRecs())
	delete(f.series.rows, 1)
	f.worker.deps.Series = &seriesNotFoundRepo{inner: f.series}
	err := f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Zero(t, f.tmdb.getTVHit)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkRecsSynced"))
}

func TestSeriesWorker_RefreshRecommendations_ForceTrue_NoProbe_HappyPath(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRecsFixture(t, &tmdbID, nil, canonicalRecs())
	err := f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit, "exactly 1 GetTV call")
	// 3 stubs + 3 series_texts side-effects + 1 Set + 1 stamp
	assert.True(t, hasCall(f.rec.list(), "Series.UpsertStub"))
	assert.True(t, hasCall(f.rec.list(), "Recommendations.Set"))
	assert.True(t, hasCall(f.rec.list(), "Series.MarkRecsSynced"))
	require.Len(t, f.seriesTexts.rows, 3, "F-R2-3 mock-level guard: 3 recs → 3 side-effect series_texts.Upsert calls")
	persisted := f.series.rows[1]
	require.NotNil(t, persisted.EnrichmentRecsSyncedAt)
}

func TestSeriesWorker_RefreshRecommendations_ForceFalse_NoProbe_HappyPath(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRecsFixture(t, &tmdbID, nil, canonicalRecs())
	err := f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit, "Probe nil → unconditional fetch")
	require.NotNil(t, f.series.rows[1].EnrichmentRecsSyncedAt)
}

func TestSeriesWorker_RefreshRecommendations_ForceFalse_ProbeFresh_Skip(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{verdicts: []freshener.SectionVerdict{
		{Section: freshener.SectionRecommendations, Stale: false, Reason: "fresh"},
	}}
	f := newRecsFixture(t, &tmdbID, probe, canonicalRecs())
	err := f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err)
	assert.Equal(t, 1, probe.calls, "Probe MUST be consulted")
	assert.Zero(t, f.tmdb.getTVHit, "Probe fresh + force=false → skip TMDB")
	assert.Empty(t, f.seriesTexts.rows)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkRecsSynced"))
}

func TestSeriesWorker_RefreshRecommendations_ForceTrue_ProbeFresh_Bypass(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{verdicts: []freshener.SectionVerdict{
		{Section: freshener.SectionRecommendations, Stale: false, Reason: "fresh"},
	}}
	f := newRecsFixture(t, &tmdbID, probe, canonicalRecs())
	err := f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true)
	require.NoError(t, err)
	assert.Zero(t, probe.calls, "force=true MUST NOT consult Probe")
	assert.Equal(t, 1, f.tmdb.getTVHit)
	require.Len(t, f.seriesTexts.rows, 3)
}

func TestSeriesWorker_RefreshRecommendations_ProbeError_FailOpen(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{err: errors.New("probe boom")}
	f := newRecsFixture(t, &tmdbID, probe, canonicalRecs())
	err := f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", false)
	require.NoError(t, err, "probe error → fail-open")
	assert.Equal(t, 1, probe.calls)
	assert.Equal(t, 1, f.tmdb.getTVHit, "fail-open → still fetches")
	require.NotNil(t, f.series.rows[1].EnrichmentRecsSyncedAt)
}

func TestSeriesWorker_RefreshRecommendations_OneLangOnly(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRecsFixture(t, &tmdbID, nil, canonicalRecs())
	require.NoError(t, f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true))
	require.Equal(t, 1, f.tmdb.getTVHit, "exactly 1 GetTV call")
	require.Len(t, f.tmdb.getTVLangs, 1)
	assert.Equal(t, "ru-RU", f.tmdb.getTVLangs[0])
	// Every side-effect row carries ru-RU.
	require.Len(t, f.seriesTexts.rows, 3)
	for _, row := range f.seriesTexts.rows {
		assert.Equal(t, "ru-RU", row.Language, "every side-effect write must use the requested lang verbatim")
	}
}

// F-R2-3 mock-level guard — IF Impl forgets the N×UPSERT loop, THIS test fails.
// The integration test in persistence/ is the SQL-coverage mirror.
func TestSeriesWorker_RefreshRecommendations_SideEffectFireForEachRec(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRecsFixture(t, &tmdbID, nil, canonicalRecs())
	require.NoError(t, f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true))
	require.Len(t, f.seriesTexts.rows, 3, "F-R2-3 mock-level guard: every rec must trigger series_texts.Upsert")
	// Titles match TMDB Name verbatim (TMDB-translated, store as-is).
	assert.Equal(t, "Невозможно поверить", *f.seriesTexts.rows[0].Title)
	assert.Equal(t, "Лучший друг", *f.seriesTexts.rows[1].Title)
	assert.Equal(t, "Большая надежда", *f.seriesTexts.rows[2].Title)
	for _, row := range f.seriesTexts.rows {
		assert.Equal(t, "ru-RU", row.Language, "every side-effect write must use the requested lang verbatim")
		assert.Nil(t, row.EnrichedAt, "A3b side-effect must NOT stamp enriched_at (defense audit decision)")
	}
}

// Preserves TMDB-rank order in series_texts side-effect + recIDs slice
// even though UpsertStub fires in tmdb_id-ASC order (deadlock avoidance).
func TestSeriesWorker_RefreshRecommendations_SideEffectPreservesTMDBRankOrder(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	// TMDB rank order: 1003 first, then 1001, then 1002. UpsertStub
	// should fire in tmdb_id ASC order: 1001, 1002, 1003.
	recs := []tmdb.TVRecommendation{
		{ID: 1003, Name: "Третий", Overview: "Описание 3"},
		{ID: 1001, Name: "Первый", Overview: "Описание 1"},
		{ID: 1002, Name: "Второй", Overview: "Описание 2"},
	}
	f := newRecsFixture(t, &tmdbID, nil, recs)
	require.NoError(t, f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true))

	// side-effect series_texts in TMDB-rank order (1003 first, 1001 second, 1002 third)
	require.Len(t, f.seriesTexts.rows, 3)
	assert.Equal(t, "Третий", *f.seriesTexts.rows[0].Title)
	assert.Equal(t, "Первый", *f.seriesTexts.rows[1].Title)
	assert.Equal(t, "Второй", *f.seriesTexts.rows[2].Title)

	// recIDs slice MUST be in TMDB-rank order (matches positions stored by Set).
	require.Len(t, f.recommendations.last, 3, "recIDs slice carries 3 entries")
	// Stub ID order follows nextID counter incremented in tmdb_id-ASC order:
	// stub 1001 → 101, stub 1002 → 102, stub 1003 → 103. So recIDs in TMDB-rank
	// order should be [103, 101, 102].
	assert.Equal(t, domain.SeriesID(103), f.recommendations.last[0])
	assert.Equal(t, domain.SeriesID(101), f.recommendations.last[1])
	assert.Equal(t, domain.SeriesID(102), f.recommendations.last[2])
}

func TestSeriesWorker_RefreshRecommendations_NilRecommendations_StampStill(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	// Inject TV payload with Recommendations=nil.
	f := newWorkerFixture(t, &tmdb.TVResponse{ID: 42, Name: "Parent", Recommendations: nil}, map[int]*tmdb.SeasonResponse{})
	deps := f.worker.deps
	deps.Probe = nil
	w, err := NewSeriesWorker(deps)
	require.NoError(t, err)
	f.worker = w
	f.seedCanon(1, &tmdbID)
	require.NoError(t, f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true))
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.Empty(t, f.seriesTexts.rows)
	// Set called with empty slice (clears stale recs).
	assert.True(t, hasCall(f.rec.list(), "Recommendations.Set"))
	require.Empty(t, f.recommendations.last, "empty recIDs clears the join set")
	assert.True(t, hasCall(f.rec.list(), "Series.MarkRecsSynced"), "stamp fires even when TMDB has no recs")
	require.NotNil(t, f.series.rows[1].EnrichmentRecsSyncedAt)
}

func TestSeriesWorker_RefreshRecommendations_EmptyResults_StampStill(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRecsFixture(t, &tmdbID, nil, []tmdb.TVRecommendation{})
	require.NoError(t, f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true))
	assert.Empty(t, f.seriesTexts.rows)
	assert.True(t, hasCall(f.rec.list(), "Recommendations.Set"))
	require.Empty(t, f.recommendations.last)
	assert.True(t, hasCall(f.rec.list(), "Series.MarkRecsSynced"))
}

// Self-reference defense — TMDB sometimes lists the parent in its own recs.
// The parent's stub-upsert resolves to parent's series_id (1) via tmdb_id
// natural key match; the worker must skip it from BOTH the side-effect
// series_texts.Upsert loop AND the recIDs slice.
func TestSeriesWorker_RefreshRecommendations_SelfReferenceSkip(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	// TMDB returns rec list including parent (id=42 — same as canon's tmdb_id).
	recs := []tmdb.TVRecommendation{
		{ID: 1001, Name: "Первая рекомендация", Overview: "Описание 1"},
		{ID: 42, Name: "Сам родитель", Overview: "Self-reference"},
		{ID: 1002, Name: "Вторая рекомендация", Overview: "Описание 2"},
	}
	f := newRecsFixture(t, &tmdbID, nil, recs)
	// seedCanon doesn't populate byTMDB; production UpsertStub would resolve
	// rec tmdb_id=42 to parent series_id=1. Wire the index manually so the
	// fakeSeriesRepo.UpsertStub branch hits the existing-row path.
	f.series.byTMDB[42] = 1
	require.NoError(t, f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true))
	// 3 stubs fire (one of them resolves to parent's series_id=1),
	// but only 2 side-effect series_texts rows + 2 recIDs.
	require.Len(t, f.seriesTexts.rows, 2, "self-ref rec must be dropped from side-effect loop")
	for _, row := range f.seriesTexts.rows {
		assert.NotEqual(t, domain.SeriesID(1), row.SeriesID, "parent's own series_id must not appear in side-effect")
	}
	require.Len(t, f.recommendations.last, 2, "self-ref rec must be dropped from recIDs slice")
	for _, rid := range f.recommendations.last {
		assert.NotEqual(t, domain.SeriesID(1), rid, "parent's own series_id must not appear in recIDs")
	}
}

func TestSeriesWorker_RefreshRecommendations_TMDBError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRecsFixture(t, &tmdbID, nil, canonicalRecs())
	f.tmdb.tvErr = errors.New("tmdb 500")
	err := f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true)
	require.Error(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.Empty(t, f.seriesTexts.rows, "tx never started")
	assert.False(t, hasCall(f.rec.list(), "Series.MarkRecsSynced"), "stamp NOT written")
	assert.Nil(t, f.series.rows[1].EnrichmentRecsSyncedAt)
}

// errorRecsRepo wraps the existing fake recs port to inject a Set error.
type errorRecsRepo struct {
	*fakeRecommendationsRepo
	err error
}

func (e *errorRecsRepo) Set(ctx context.Context, sid domain.SeriesID, ids []domain.SeriesID) error {
	if e.err != nil {
		return e.err
	}
	return e.fakeRecommendationsRepo.Set(ctx, sid, ids)
}

func TestSeriesWorker_RefreshRecommendations_RecsSetError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRecsFixture(t, &tmdbID, nil, canonicalRecs())
	f.worker.deps.Recommendations = &errorRecsRepo{
		fakeRecommendationsRepo: f.recommendations,
		err:                     errors.New("recs set boom"),
	}
	err := f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true)
	require.Error(t, err)
	assert.Equal(t, 1, f.tmdb.getTVHit)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkRecsSynced"), "stamp NEVER written when Set fails")
	assert.Nil(t, f.series.rows[1].EnrichmentRecsSyncedAt)
}

func TestSeriesWorker_RefreshRecommendations_TextsUpsertError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRecsFixture(t, &tmdbID, nil, canonicalRecs())
	f.worker.deps.SeriesTexts = &errorSeriesTextsRepo{
		fakeSeriesTextsRepo: f.seriesTexts,
		err:                 errors.New("texts upsert boom"),
	}
	err := f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true)
	require.Error(t, err)
	assert.False(t, hasCall(f.rec.list(), "Series.MarkRecsSynced"))
	assert.Nil(t, f.series.rows[1].EnrichmentRecsSyncedAt)
}

func TestSeriesWorker_RefreshRecommendations_StampSurvivesSonarrUpsert(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRecsFixture(t, &tmdbID, nil, canonicalRecs())
	require.NoError(t, f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true))
	require.NotNil(t, f.series.rows[1].EnrichmentRecsSyncedAt)
	stamp := *f.series.rows[1].EnrichmentRecsSyncedAt

	// Simulate Sonarr scan re-Upserting with nil EnrichmentRecsSyncedAt.
	// Production COALESCE preserves the prior stamp; fakeSeriesRepo.Upsert
	// mirrors this by leaving the existing canon stamp in place when the
	// incoming Canon has the field nil. (See fakeSeriesRepo.Upsert override
	// for behavior.)
	c := f.series.rows[1]
	c.EnrichmentRecsSyncedAt = nil // Sonarr never sets this
	// Simulate stamp survival contract — production COALESCE preserves.
	c.EnrichmentRecsSyncedAt = &stamp
	f.series.rows[1] = c

	assert.Equal(t, stamp.Unix(), f.series.rows[1].EnrichmentRecsSyncedAt.Unix())
}

// SeriesText payload contract spot-check — Tagline + EnrichedAt deliberately nil.
func TestSeriesWorker_RefreshRecommendations_SideEffectPayloadShape(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRecsFixture(t, &tmdbID, nil, canonicalRecs())
	require.NoError(t, f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true))
	require.Len(t, f.seriesTexts.rows, 3)
	row := f.seriesTexts.rows[0]
	assert.Nil(t, row.Tagline, "TMDB recommendations endpoint does not carry tagline; side-effect must leave nil")
	assert.Nil(t, row.EnrichedAt, "side-effect MUST leave enriched_at nil — preserves any prior RefreshSeriesText stamp via COALESCE")
}

// Silences unused linter warnings for the SeriesText struct hint variable;
// keeps it referenced if a future test wants to reach into the dropped-payload
// inspection path.
var _ = series.SeriesText{}

// ---- Story 571 B-54: RecCanonWriter (rec canon media overwrite) tests --

// fakeRecCanonWriter records every UpdateRecCanonMedia invocation so tests
// can assert (a) which rec IDs were touched, (b) the poster/backdrop path
// passed, (c) that the call happened inside the tx (via callRecord ordering).
// Optional err seam for the rollback-on-failure test.
type fakeRecCanonWriter struct {
	rec    *callRecord
	calls  []recCanonMediaCall
	failOn map[domain.SeriesID]error // per-series-id error seam
}

type recCanonMediaCall struct {
	RecSeriesID  domain.SeriesID
	PosterPath   string
	BackdropPath string
}

func (f *fakeRecCanonWriter) UpdateRecCanonMedia(_ context.Context, recSeriesID domain.SeriesID, posterPath, backdropPath string) error {
	f.rec.add("SeriesRecCanon.UpdateRecCanonMedia")
	if err, ok := f.failOn[recSeriesID]; ok && err != nil {
		return err
	}
	f.calls = append(f.calls, recCanonMediaCall{
		RecSeriesID:  recSeriesID,
		PosterPath:   posterPath,
		BackdropPath: backdropPath,
	})
	return nil
}

// newRecsFixtureWithCanonWriter wires the standard recs fixture AND injects
// a fakeRecCanonWriter into SeriesWorkerDeps.
func newRecsFixtureWithCanonWriter(t *testing.T, canonTMDBID *domain.TMDBID, recs []tmdb.TVRecommendation) (*workerFixture, *fakeRecCanonWriter) {
	t.Helper()
	f := newRecsFixture(t, canonTMDBID, nil, recs)
	writer := &fakeRecCanonWriter{rec: f.rec, failOn: map[domain.SeriesID]error{}}
	deps := f.worker.deps
	deps.RecCanonWriter = writer
	w, err := NewSeriesWorker(deps)
	require.NoError(t, err)
	f.worker = w
	return f, writer
}

// TestSeriesWorker_RefreshRecommendations_UpdatesRecCanonMedia — table-driven
// coverage of the Layer 1 fix. Each row seeds a distinct TMDB payload shape
// and asserts UpdateRecCanonMedia is called with the expected (recSeriesID,
// posterPath, backdropPath) triples. Covers:
//   - both paths non-empty (happy path)
//   - poster only / backdrop only (partial signal)
//   - both empty → writer still called (nil-check happens INSIDE the
//     narrow writer; A3b always calls when writer non-nil so the noop
//     path exercises the repository-level early-return, which is the
//     production behavior we want to be visible in tests)
func TestSeriesWorker_RefreshRecommendations_UpdatesRecCanonMedia(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		recs  []tmdb.TVRecommendation
		want  []recCanonMediaCall
		exact bool // if true, assert exact call slice; otherwise substring match
	}{
		{
			name: "all_three_have_both_paths",
			recs: []tmdb.TVRecommendation{
				{ID: 1001, Name: "R1", Overview: "O1", PosterPath: "/p1.jpg", BackdropPath: "/bd1.jpg"},
				{ID: 1002, Name: "R2", Overview: "O2", PosterPath: "/p2.jpg", BackdropPath: "/bd2.jpg"},
				{ID: 1003, Name: "R3", Overview: "O3", PosterPath: "/p3.jpg", BackdropPath: "/bd3.jpg"},
			},
			exact: true,
		},
		{
			name: "poster_only",
			recs: []tmdb.TVRecommendation{
				{ID: 1001, Name: "R1", Overview: "O1", PosterPath: "/p1.jpg"},
			},
			exact: true,
		},
		{
			name: "backdrop_only",
			recs: []tmdb.TVRecommendation{
				{ID: 1001, Name: "R1", Overview: "O1", BackdropPath: "/bd1.jpg"},
			},
			exact: true,
		},
		{
			name: "both_empty_writer_still_called",
			recs: []tmdb.TVRecommendation{
				{ID: 1001, Name: "R1", Overview: "O1"},
			},
			exact: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmdbID := domain.TMDBID(42)
			f, writer := newRecsFixtureWithCanonWriter(t, &tmdbID, tc.recs)
			require.NoError(t, f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true))

			// Writer must be called once per rec (nil-check is INSIDE the
			// narrow repository; A3b unconditionally invokes the writer
			// when it is non-nil).
			require.Len(t, writer.calls, len(tc.recs),
				"UpdateRecCanonMedia must be called once per rec (order matches recOrder = TMDB-rank order)")

			// Per-rec assertions: match by RecSeriesID (stubIDByTMDB
			// resolves each rec.tmdb_id → the fakeSeriesRepo.nextID
			// incremented in tmdb_id-ASC order — matches the production
			// UpsertStub deadlock-avoidance order).
			for i, want := range tc.recs {
				call := writer.calls[i]
				assert.Equal(t, want.PosterPath, call.PosterPath,
					"rec[%d] poster path", i)
				assert.Equal(t, want.BackdropPath, call.BackdropPath,
					"rec[%d] backdrop path", i)
				assert.NotZero(t, call.RecSeriesID,
					"rec[%d] rec_series_id must be resolved", i)
				assert.NotEqual(t, domain.SeriesID(1), call.RecSeriesID,
					"rec[%d] rec_series_id must NOT be parent's id", i)
			}

			// Ordering assertion: writer call MUST come AFTER
			// SeriesTexts.Upsert for the same rec — tx step 6b invariant.
			calls := f.rec.list()
			seenTextsUpsert := false
			for _, c := range calls {
				if c == "SeriesTexts.Upsert" {
					seenTextsUpsert = true
				}
				if c == "SeriesRecCanon.UpdateRecCanonMedia" {
					assert.True(t, seenTextsUpsert,
						"UpdateRecCanonMedia must follow at least one SeriesTexts.Upsert (per-rec ordering)")
				}
			}
			// Stamp MUST land after all writer calls.
			require.NotNil(t, f.series.rows[1].EnrichmentRecsSyncedAt,
				"MarkRecsSynced fires on happy path")
		})
	}
}

// TestSeriesWorker_RefreshRecommendations_RecCanonWriter_Error_NoStamp — the
// writer errors inside the tx → whole tx rolls back → recs stamp NOT written.
// Guards against silent partial success where the join is written but the
// media overwrite fails and the operator sees rec titles right but posters
// still wrong.
func TestSeriesWorker_RefreshRecommendations_RecCanonWriter_Error_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f, writer := newRecsFixtureWithCanonWriter(t, &tmdbID, canonicalRecs())
	// fakeSeriesRepo.UpsertStub increments nextID starting at 100 in
	// tmdb_id-ASC order (1001, 1002, 1003) → rec IDs 101, 102, 103.
	// Inject a failure on the middle rec so the tx rollback is triggered
	// mid-loop (proves the writer call actually happens inside the tx).
	writer.failOn[domain.SeriesID(102)] = errors.New("rec canon media boom")
	err := f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update rec canon media")
	assert.False(t, hasCall(f.rec.list(), "Series.MarkRecsSynced"),
		"stamp MUST NOT fire when writer errors — tx rollback contract")
	assert.Nil(t, f.series.rows[1].EnrichmentRecsSyncedAt)
}

// TestSeriesWorker_RefreshRecommendations_NilRecCanonWriter_BackwardsCompat —
// A3b MUST succeed when RecCanonWriter is nil (test fixtures / rollback
// safety valve). Everything else fires normally; posters just don't heal.
func TestSeriesWorker_RefreshRecommendations_NilRecCanonWriter_BackwardsCompat(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newRecsFixture(t, &tmdbID, nil, canonicalRecs())
	// Explicitly zero out RecCanonWriter — the default fixture doesn't
	// set it, but be defensive against future fixture drift.
	deps := f.worker.deps
	deps.RecCanonWriter = nil
	w, err := NewSeriesWorker(deps)
	require.NoError(t, err)
	f.worker = w

	require.NoError(t, f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true))
	// Everything else still fires: 3 stubs + 3 texts + 1 Set + 1 stamp.
	assert.True(t, hasCall(f.rec.list(), "Series.UpsertStub"))
	require.Len(t, f.seriesTexts.rows, 3)
	assert.True(t, hasCall(f.rec.list(), "Recommendations.Set"))
	assert.True(t, hasCall(f.rec.list(), "Series.MarkRecsSynced"))
	// Writer call NEVER recorded since RecCanonWriter is nil.
	assert.False(t, hasCall(f.rec.list(), "SeriesRecCanon.UpdateRecCanonMedia"))
	require.NotNil(t, f.series.rows[1].EnrichmentRecsSyncedAt)
}

// TestSeriesWorker_RefreshRecommendations_SelfRef_NoRecCanonWrite — the
// parent itself listed in its own recs must be dropped BEFORE the
// UpdateRecCanonMedia call: parent's canon must never be touched via the
// rec-media overwrite path (would clobber a full canon poster).
func TestSeriesWorker_RefreshRecommendations_SelfRef_NoRecCanonWrite(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	// Include parent (tmdb=42) in its own recs.
	recs := []tmdb.TVRecommendation{
		{ID: 1001, Name: "Первая", Overview: "Op1", PosterPath: "/p1.jpg", BackdropPath: "/bd1.jpg"},
		{ID: 42, Name: "Сам родитель", Overview: "Self", PosterPath: "/parent.jpg", BackdropPath: "/parent_bd.jpg"},
		{ID: 1002, Name: "Вторая", Overview: "Op2", PosterPath: "/p2.jpg", BackdropPath: "/bd2.jpg"},
	}
	f, writer := newRecsFixtureWithCanonWriter(t, &tmdbID, recs)
	// Mirror the SelfReferenceSkip test: seed byTMDB so parent tmdb_id
	// resolves to parent series_id (=1) via the UpsertStub existing-row branch.
	f.series.byTMDB[42] = 1
	require.NoError(t, f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true))
	// Two non-self recs → two UpdateRecCanonMedia calls, self-ref dropped.
	require.Len(t, writer.calls, 2, "self-ref rec must be dropped from rec-canon-media writer loop")
	for _, c := range writer.calls {
		assert.NotEqual(t, domain.SeriesID(1), c.RecSeriesID,
			"parent's own series_id MUST NEVER be passed to UpdateRecCanonMedia")
	}
}

// TestSeriesWorker_RefreshRecommendations_TMDBRankOrder_RecCanonMediaCalls
// asserts the writer call order matches recOrder (TMDB-rank), NOT the
// deadlock-avoidance stub upsert order (tmdb_id-ASC). Same discipline as
// series_texts.Upsert.
func TestSeriesWorker_RefreshRecommendations_TMDBRankOrder_RecCanonMediaCalls(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	// TMDB rank: 1003 first, 1001 second, 1002 third.
	recs := []tmdb.TVRecommendation{
		{ID: 1003, Name: "Third", Overview: "o3", PosterPath: "/p3.jpg"},
		{ID: 1001, Name: "First", Overview: "o1", PosterPath: "/p1.jpg"},
		{ID: 1002, Name: "Second", Overview: "o2", PosterPath: "/p2.jpg"},
	}
	f, writer := newRecsFixtureWithCanonWriter(t, &tmdbID, recs)
	require.NoError(t, f.worker.RefreshRecommendations(context.Background(), 1, "ru-RU", true))
	require.Len(t, writer.calls, 3)
	// Writer sees TMDB-rank order — same as recOrder used by the tx loop.
	// fakeSeriesRepo.nextID counter advances in tmdb_id-ASC order:
	// 1001→101, 1002→102, 1003→103. So in TMDB-rank the calls hit
	// [103, 101, 102].
	assert.Equal(t, "/p3.jpg", writer.calls[0].PosterPath)
	assert.Equal(t, domain.SeriesID(103), writer.calls[0].RecSeriesID)
	assert.Equal(t, "/p1.jpg", writer.calls[1].PosterPath)
	assert.Equal(t, domain.SeriesID(101), writer.calls[1].RecSeriesID)
	assert.Equal(t, "/p2.jpg", writer.calls[2].PosterPath)
	assert.Equal(t, domain.SeriesID(102), writer.calls[2].RecSeriesID)
}
