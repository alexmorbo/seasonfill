package enrichment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func newRefreshRecsWorker(t *testing.T, resp *tmdb.MovieResponse, canon *fakeMovieCanon) (*MovieWorker, *fakeMovieI18n, *fakeMovieRecs, *passthroughTx) {
	t.Helper()
	i18n := &fakeMovieI18n{}
	recs := &fakeMovieRecs{}
	tx := &passthroughTx{}
	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:   &fakeMovieTMDB{resp: resp},
		Movies: canon,
		I18n:   i18n,
		Recs:   recs,
		Tx:     tx,
		Clock:  func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)
	return w, i18n, recs, tx
}

func recsRuResp() *tmdb.MovieResponse {
	return &tmdb.MovieResponse{
		ID:    787,
		Title: "Мистер и миссис Смит",
		Recommendations: &tmdb.MovieRecommendations{Results: []tmdb.MovieRecommendation{
			{ID: 1233575, Title: "Чёрный чемодан – двойная игра"},
			{ID: 522931, Title: "Телохранитель жены киллера"},
		}},
	}
}

// The core proof: RefreshRecommendations writes movie_i18n rows carrying the CYRILLIC
// localized rec titles from the GetMovie(lang) payload (GATE-ZERO F-05 shape), replaces
// the join, and stamps recs — ALL in one tx.
func TestMovieWorker_RefreshRecommendations_WritesLocalizedCyrillicTitles(t *testing.T) {
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 787)}
	w, i18n, recs, tx := newRefreshRecsWorker(t, recsRuResp(), canon)

	require.NoError(t, w.RefreshRecommendations(context.Background(), 7, "ru-RU"))

	assert.Equal(t, 1, tx.calls, "movie_i18n + join write ran inside the Transactor")
	// fakeMovieCanon.UpsertStub assigns ids 1,2 in tmdb-id ASC order:
	// tmdb 522931 → id1, tmdb 1233575 → id2.
	require.Len(t, i18n.writes, 2, "one movie_i18n title write per rec")
	byTitle := map[string]movieI18nWrite{}
	for _, wr := range i18n.writes {
		byTitle[wr.title] = wr
		assert.Equal(t, "ru-RU", wr.lang)
		assert.Empty(t, wr.overview, "TITLE-only drain — overview stays NULL (F-06)")
		assert.Empty(t, wr.tagline)
		assert.Nil(t, wr.poster, "rec posters untouched (F-06)")
		assert.Nil(t, wr.backdrop)
	}
	assert.Contains(t, byTitle, "Чёрный чемодан – двойная игра")
	assert.Contains(t, byTitle, "Телохранитель жены киллера")

	require.Equal(t, 1, recs.setCalls, "movie_recommendations join replaced once")
	assert.Equal(t, domain.MovieID(7), recs.setMovie)
	assert.Equal(t, []domain.MovieID{2, 1}, recs.setIDs, "join in TMDB-rank order (1233575→id2, 522931→id1)")
	assert.Equal(t, 1, canon.recsMarkCalls, "enrichment_recs_synced_at stamped once")
}

// Blank rec title → skipped (COALESCE-safe), but the rec still joins and the recs
// clock is stamped (checked-empty anti-storm).
func TestMovieWorker_RefreshRecommendations_SkipsBlankTitle(t *testing.T) {
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 787)}
	resp := &tmdb.MovieResponse{
		ID: 787, Title: "T",
		Recommendations: &tmdb.MovieRecommendations{Results: []tmdb.MovieRecommendation{
			{ID: 10, Title: ""},          // blank → no i18n write, still joins
			{ID: 20, Title: "Настоящее"}, // kept
		}},
	}
	w, i18n, recs, _ := newRefreshRecsWorker(t, resp, canon)

	require.NoError(t, w.RefreshRecommendations(context.Background(), 7, "ru-RU"))

	require.Len(t, i18n.writes, 1, "blank title produces no movie_i18n row")
	assert.Equal(t, "Настоящее", i18n.writes[0].title)
	require.Equal(t, 1, recs.setCalls)
	assert.Len(t, recs.setIDs, 2, "both recs still join even when a title was blank")
	assert.Equal(t, 1, canon.recsMarkCalls, "recs clock stamped even with a skipped blank")
}

// Self-ref (TMDB lists the parent among its own recs) is dropped from BOTH the title
// drain and the join set.
func TestMovieWorker_RefreshRecommendations_SelfRefDropped(t *testing.T) {
	canon := &fakeMovieCanon{
		getResp:      movieCanonWithTMDB(7, 787),
		stubIDByTMDB: map[int64]domain.MovieID{1233575: 7, 522931: 9}, // 1233575 resolves to parent
	}
	w, i18n, recs, _ := newRefreshRecsWorker(t, recsRuResp(), canon)

	require.NoError(t, w.RefreshRecommendations(context.Background(), 7, "ru-RU"))

	require.Equal(t, 1, recs.setCalls)
	assert.Equal(t, []domain.MovieID{9}, recs.setIDs, "self-ref (id 7) dropped, only 522931→9 kept")
	require.Len(t, i18n.writes, 1, "no title drain for the self-ref rec")
	assert.Equal(t, domain.MovieID(9), i18n.writes[0].movieID)
}

// Empty recs → join cleared + recs stamped (anti-storm), no title writes.
func TestMovieWorker_RefreshRecommendations_EmptyRecsStampsAndClears(t *testing.T) {
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 787)}
	resp := &tmdb.MovieResponse{ID: 787, Title: "T", Recommendations: nil}
	w, i18n, recs, _ := newRefreshRecsWorker(t, resp, canon)

	require.NoError(t, w.RefreshRecommendations(context.Background(), 7, "ru-RU"))

	assert.Empty(t, i18n.writes)
	require.Equal(t, 1, recs.setCalls)
	assert.Empty(t, recs.setIDs, "empty recs clears the join set")
	assert.Equal(t, 1, canon.recsMarkCalls, "stamp even on empty recs (anti-storm)")
}

// nil I18n / Recs / Tx → clean no-op (no panic, no stamp).
func TestMovieWorker_RefreshRecommendations_UnwiredIsNoop(t *testing.T) {
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 787)}
	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:   &fakeMovieTMDB{resp: recsRuResp()},
		Movies: canon,
		Clock:  func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)
	require.NoError(t, w.RefreshRecommendations(context.Background(), 7, "ru-RU"))
	assert.Zero(t, canon.recsMarkCalls, "unwired RefreshRecommendations is a no-op")
}

// A movie with no tmdb id → skip (Radarr orphan), no stamp.
func TestMovieWorker_RefreshRecommendations_NoTMDBIDSkips(t *testing.T) {
	canon := &fakeMovieCanon{getResp: movie.Canon{ID: 7, TMDBID: nil}}
	w, _, _, _ := newRefreshRecsWorker(t, &tmdb.MovieResponse{}, canon)
	require.NoError(t, w.RefreshRecommendations(context.Background(), 7, "ru-RU"))
	assert.Zero(t, canon.recsMarkCalls)
}
