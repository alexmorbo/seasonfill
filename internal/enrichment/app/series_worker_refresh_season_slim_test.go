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

// ---- fakes specific to A3a narrow refresh -----------------------------

// errorEpisodesRepo wraps fakeEpisodesRepo and triggers an injected
// error on BatchUpsert. Used to assert tx rollback drops the stamp.
type errorEpisodesRepo struct {
	*fakeEpisodesRepo
	err error
}

func (e *errorEpisodesRepo) BatchUpsert(ctx context.Context, eps []series.CanonEpisode) ([]int64, error) {
	if e.err != nil {
		return nil, e.err
	}
	return e.fakeEpisodesRepo.BatchUpsert(ctx, eps)
}

// errorEpisodeTextsRepo wraps fakeEpisodeTextsRepo and triggers an
// injected error on Upsert. Used to assert tx rollback drops the stamp.
type errorEpisodeTextsRepo struct {
	*fakeEpisodeTextsRepo
	err error
}

func (e *errorEpisodeTextsRepo) Upsert(ctx context.Context, t series.EpisodeText) error {
	if e.err != nil {
		return e.err
	}
	return e.fakeEpisodeTextsRepo.Upsert(ctx, t)
}

// minimalSeason8 returns a SeasonResponse for season 8 with 3 episodes.
// Used by the worker tests to verify ONE-SEASON-ONLY discipline.
func minimalSeason8() *tmdb.SeasonResponse {
	return &tmdb.SeasonResponse{
		ID:           555,
		Name:         "Season 8",
		Overview:     "Season 8 overview",
		AirDate:      "2026-01-01",
		SeasonNumber: 8,
		PosterPath:   "/poster.jpg",
		Episodes: []tmdb.SeasonEpisode{
			{ID: 1001, Name: "Ep 1", Overview: "Ep 1 overview", SeasonNumber: 8, EpisodeNumber: 1, AirDate: "2026-01-01", EpisodeType: "standard"},
			{ID: 1002, Name: "Ep 2", Overview: "Ep 2 overview", SeasonNumber: 8, EpisodeNumber: 2, AirDate: "2026-01-08", EpisodeType: "standard"},
			{ID: 1003, Name: "Ep 3", Overview: "Ep 3 overview", SeasonNumber: 8, EpisodeNumber: 3, AirDate: "2026-01-15", EpisodeType: "standard"},
		},
	}
}

// newSlimFixture builds a workerFixture pre-seeded for narrow refresh
// tests. canonTMDBID nil → seedCanon installs a Sonarr-only canon row
// (no tmdb_id). probe nil OK — Worker's Probe field stays nil.
func newSlimFixture(t *testing.T, canonTMDBID *domain.TMDBID, probe freshener.Probe) *workerFixture {
	t.Helper()
	f := newWorkerFixture(t, minimalTV(), map[int]*tmdb.SeasonResponse{8: minimalSeason8()})
	// Re-build worker with the optional Probe field set. NewSeriesWorker
	// keeps the rest of the deps identical.
	deps := f.worker.deps
	deps.Probe = probe
	w, err := NewSeriesWorker(deps)
	require.NoError(t, err)
	f.worker = w
	f.seedCanon(1, canonTMDBID)
	return f
}

// ---- tests ----------------------------------------------------------

func TestSeriesWorker_RefreshSeasonSlim_InvalidLang_Error(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	err := f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "not-a-lang", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid lang")
	assert.NotContains(t, callsOfFake(f.tmdb), "GetSeason",
		"invalid lang MUST short-circuit before TMDB")
}

func TestSeriesWorker_RefreshSeasonSlim_NoTMDBID_Skip(t *testing.T) {
	t.Parallel()
	f := newSlimFixture(t, nil, nil)
	err := f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", false)
	require.NoError(t, err)
	assert.NotContains(t, callsOfFake(f.tmdb), "GetSeason",
		"TMDB MUST NOT be called when canon.TMDBID is nil")
	assert.False(t, hasCall(f.rec.list(), "Episodes.BatchUpsert"))
	assert.False(t, hasCall(f.rec.list(), "Seasons.MarkSeasonEpisodesSynced"),
		"stamp MUST NOT be written")
}

func TestSeriesWorker_RefreshSeasonSlim_SeriesMissing_Skip(t *testing.T) {
	t.Parallel()
	f := newSlimFixture(t, nil, nil)
	// Wipe the seeded canon so Series.Get returns SeriesNotFoundError.
	delete(f.series.rows, 1)
	f.worker.deps.Series = &seriesNotFoundRepo{inner: f.series}
	err := f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", false)
	require.NoError(t, err)
	assert.NotContains(t, callsOfFake(f.tmdb), "GetSeason")
	assert.False(t, hasCall(f.rec.list(), "Seasons.MarkSeasonEpisodesSynced"))
}

func TestSeriesWorker_RefreshSeasonSlim_ForceTrue_NoProbe_HappyPath(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	err := f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true)
	require.NoError(t, err)
	assert.Equal(t, 1, countCall(f.tmdb, "GetSeason"), "exactly 1 GetSeason call")
	assert.True(t, hasCall(f.rec.list(), "Seasons.Upsert"),
		"defensive in-tx Seasons.Upsert MUST run")
	assert.True(t, hasCall(f.rec.list(), "Episodes.BatchUpsert"),
		"episodes MUST be written")
	assert.True(t, hasCall(f.rec.list(), "EpisodeTexts.Upsert"),
		"episode_texts MUST be written")
	assert.True(t, hasCall(f.rec.list(), "Seasons.MarkSeasonEpisodesSynced"),
		"stamp MUST fire on success")
	// Verify the per-episode texts payload (3 episodes -> 3 EpisodeText
	// rows for the requested lang).
	require.Len(t, f.episodeTexts.rows, 3)
	for _, txt := range f.episodeTexts.rows {
		assert.Equal(t, "ru-RU", txt.Language)
		require.NotNil(t, txt.Title)
	}
}

func TestSeriesWorker_RefreshSeasonSlim_ForceFalse_NoProbe_HappyPath(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	// force=false + Probe nil → fetch unconditionally.
	err := f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", false)
	require.NoError(t, err)
	assert.Equal(t, 1, countCall(f.tmdb, "GetSeason"))
	assert.True(t, hasCall(f.rec.list(), "Seasons.MarkSeasonEpisodesSynced"))
}

func TestSeriesWorker_RefreshSeasonSlim_ForceFalse_ProbeFresh_Skip(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{verdicts: []freshener.SectionVerdict{
		{Section: freshener.SeasonSection(8), Stale: false, Reason: "fresh"},
	}}
	f := newSlimFixture(t, &tmdbID, probe)
	err := f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", false)
	require.NoError(t, err)
	assert.Equal(t, 1, probe.calls, "Probe MUST be consulted")
	assert.Zero(t, countCall(f.tmdb, "GetSeason"), "Probe fresh + force=false → skip TMDB")
	assert.False(t, hasCall(f.rec.list(), "Seasons.MarkSeasonEpisodesSynced"))
}

func TestSeriesWorker_RefreshSeasonSlim_ForceTrue_ProbeFresh_Bypass(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{verdicts: []freshener.SectionVerdict{
		{Section: freshener.SeasonSection(8), Stale: false, Reason: "fresh"},
	}}
	f := newSlimFixture(t, &tmdbID, probe)
	err := f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true)
	require.NoError(t, err)
	assert.Zero(t, probe.calls, "force=true MUST NOT consult Probe")
	assert.Equal(t, 1, countCall(f.tmdb, "GetSeason"), "force=true bypasses gate")
	assert.True(t, hasCall(f.rec.list(), "Seasons.MarkSeasonEpisodesSynced"))
}

func TestSeriesWorker_RefreshSeasonSlim_ProbeError_FailOpen(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	probe := &fakeProbe{err: errors.New("probe boom")}
	f := newSlimFixture(t, &tmdbID, probe)
	err := f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", false)
	require.NoError(t, err, "probe error → fail-open (no error propagated)")
	assert.Equal(t, 1, probe.calls)
	assert.Equal(t, 1, countCall(f.tmdb, "GetSeason"), "fail-open → still fetches")
	assert.True(t, hasCall(f.rec.list(), "Seasons.MarkSeasonEpisodesSynced"))
}

func TestSeriesWorker_RefreshSeasonSlim_OneLangOnly(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	require.NoError(t, f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true))
	require.Equal(t, 1, countCall(f.tmdb, "GetSeason"), "exactly 1 GetSeason call")
	// Every persisted episode_text row uses the requested lang.
	require.NotEmpty(t, f.episodeTexts.rows)
	for _, txt := range f.episodeTexts.rows {
		assert.Equal(t, "ru-RU", txt.Language, "ONE LANG ONLY: every row language must match request")
	}
}

func TestSeriesWorker_RefreshSeasonSlim_OneSeasonOnly(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	// Pre-seed a season 9 row so we can assert it stays untouched.
	f.seasons.rows[9] = series.CanonSeason{
		ID: 999, SeriesID: 1, SeasonNumber: 9,
	}
	require.NoError(t, f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true))
	// Episodes batch must contain ONLY season=8 rows.
	require.NotEmpty(t, f.episodes.rows)
	for _, ep := range f.episodes.rows {
		assert.Equal(t, 8, ep.SeasonNumber,
			"ONE SEASON ONLY: every persisted episode must be season=8")
	}
	// Pre-existing season 9 row must remain untouched.
	require.Equal(t, int64(999), f.seasons.rows[9].ID)
}

func TestSeriesWorker_RefreshSeasonSlim_TMDBError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	f.tmdb.seasErr = map[int]error{8: errors.New("tmdb 500")}
	err := f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true)
	require.Error(t, err)
	assert.Equal(t, 1, countCall(f.tmdb, "GetSeason"))
	assert.False(t, hasCall(f.rec.list(), "Episodes.BatchUpsert"), "tx never started")
	assert.False(t, hasCall(f.rec.list(), "Seasons.MarkSeasonEpisodesSynced"), "stamp NOT written")
}

func TestSeriesWorker_RefreshSeasonSlim_EpisodesUpsertError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	f.worker.deps.Episodes = &errorEpisodesRepo{
		fakeEpisodesRepo: f.episodes,
		err:              errors.New("episodes boom"),
	}
	err := f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true)
	require.Error(t, err)
	assert.False(t, hasCall(f.rec.list(), "EpisodeTexts.Upsert"), "tx rolled back before texts write")
	assert.False(t, hasCall(f.rec.list(), "Seasons.MarkSeasonEpisodesSynced"), "stamp NOT written")
}

func TestSeriesWorker_RefreshSeasonSlim_TextsUpsertError_NoStamp(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	f.worker.deps.EpisodeTexts = &errorEpisodeTextsRepo{
		fakeEpisodeTextsRepo: f.episodeTexts,
		err:                  errors.New("texts boom"),
	}
	err := f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true)
	require.Error(t, err)
	assert.True(t, hasCall(f.rec.list(), "Episodes.BatchUpsert"), "episodes wrote before texts error")
	assert.False(t, hasCall(f.rec.list(), "Seasons.MarkSeasonEpisodesSynced"), "stamp NOT written")
}

func TestSeriesWorker_RefreshSeasonSlim_EmptyEpisodes_StampStill(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	// Override TMDB season response with empty Episodes[] (future-scheduled).
	f.tmdb.seasons[8] = &tmdb.SeasonResponse{
		ID:           555,
		Name:         "Season 8",
		AirDate:      "2027-01-01",
		SeasonNumber: 8,
	}
	err := f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true)
	require.NoError(t, err)
	assert.Equal(t, 1, countCall(f.tmdb, "GetSeason"))
	assert.Empty(t, f.episodeTexts.rows, "no episode_texts rows for empty Episodes[]")
	assert.True(t, hasCall(f.rec.list(), "Seasons.MarkSeasonEpisodesSynced"),
		"stamp still fires on empty-episodes branch to prevent probe storm")
}

// ---- helpers --------------------------------------------------------

// countCall counts how many times the named method appears in f.calls.
func countCall(f *fakeTMDB, name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == name {
			n++
		}
	}
	return n
}

// callsOfFake returns a copy of f.calls (lock-respecting).
func callsOfFake(f *fakeTMDB) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}
