package enrichment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakePeopleTexts captures the people_texts rows RefreshCast writes.
type fakePeopleTexts struct {
	rows []people.PersonText
}

func (f *fakePeopleTexts) BatchUpsert(_ context.Context, texts []people.PersonText) error {
	f.rows = append(f.rows, texts...)
	return nil
}

func newRefreshCastWorker(t *testing.T, resp *tmdb.MovieResponse, canon *fakeMovieCanon) (*MovieWorker, *fakePeopleUpsert, *fakePeopleTexts, *passthroughTx) {
	t.Helper()
	pu := &fakePeopleUpsert{}
	pt := &fakePeopleTexts{}
	tx := &passthroughTx{}
	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:        &fakeMovieTMDB{resp: resp},
		Movies:      canon,
		People:      pu,
		PeopleTexts: pt,
		Tx:          tx,
		Clock:       func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)
	return w, pu, pt, tx
}

// The core proof: RefreshCast writes people_texts carrying the CYRILLIC localized
// cast names from the GetMovie(lang) payload (GATE-ZERO F-04 shape), and stamps cast.
func TestMovieWorker_RefreshCast_WritesLocalizedCyrillicNames(t *testing.T) {
	tmdbID := domain.TMDBID(360920)
	canon := &fakeMovieCanon{getResp: movie.Canon{ID: 7, TMDBID: &tmdbID}}
	resp := &tmdb.MovieResponse{
		ID:    360920,
		Title: "Гринч",
		Credits: &tmdb.MovieCredits{Cast: []tmdb.MovieCastMember{
			{ID: 71580, Name: "Бенедикт Камбербэтч", OriginalName: "Benedict Cumberbatch", CreditID: "c0", Order: 0},
			{ID: 80591, Name: "Рашида Джонс", OriginalName: "Rashida Jones", CreditID: "c1", Order: 1},
		}},
	}
	w, pu, pt, tx := newRefreshCastWorker(t, resp, canon)

	require.NoError(t, w.RefreshCast(context.Background(), 7, "ru-RU"))

	assert.Equal(t, 1, tx.calls, "people_texts write ran inside the Transactor")
	assert.Len(t, pu.upserted, 2, "two cast person stubs upserted")
	require.Len(t, pt.rows, 2, "two people_texts rows written")
	names := map[int64]string{}
	for i, r := range pt.rows {
		require.NotNil(t, pt.rows[i].Name)
		names[r.PersonID] = *r.Name
		assert.Equal(t, "ru-RU", r.Language)
	}
	// fakePeopleUpsert returns ids 1,2 in tmdb-id ASC order → person 71580=id1, 80591=id2.
	assert.Equal(t, "Бенедикт Камбербэтч", names[1])
	assert.Equal(t, "Рашида Джонс", names[2])
	assert.Equal(t, 1, canon.castMarkCalls, "enrichment_cast_synced_at stamped once")
	assert.Equal(t, domain.MovieID(7), canon.castMarkedID)
}

// Blank/whitespace localized name → skipped (COALESCE-safe), but the cast clock is
// still stamped (checked-empty anti-storm).
func TestMovieWorker_RefreshCast_SkipsBlankNames(t *testing.T) {
	tmdbID := domain.TMDBID(42)
	canon := &fakeMovieCanon{getResp: movie.Canon{ID: 7, TMDBID: &tmdbID}}
	resp := &tmdb.MovieResponse{
		ID: 42, Title: "T",
		Credits: &tmdb.MovieCredits{Cast: []tmdb.MovieCastMember{
			{ID: 1, Name: "  ", CreditID: "c0", Order: 0},            // whitespace → skip
			{ID: 2, Name: "Настоящее Имя", CreditID: "c1", Order: 1}, // kept
		}},
	}
	w, pu, pt, _ := newRefreshCastWorker(t, resp, canon)

	require.NoError(t, w.RefreshCast(context.Background(), 7, "ru-RU"))

	assert.Len(t, pu.upserted, 2, "both stubs still upserted (person rows exist)")
	require.Len(t, pt.rows, 1, "blank name produces no people_texts row")
	require.NotNil(t, pt.rows[0].Name)
	assert.Equal(t, "Настоящее Имя", *pt.rows[0].Name)
	assert.Equal(t, 1, canon.castMarkCalls, "cast clock stamped even with a skipped blank")
}

// nil PeopleTexts / Tx / People → clean no-op (no panic, no stamp).
func TestMovieWorker_RefreshCast_UnwiredIsNoop(t *testing.T) {
	tmdbID := domain.TMDBID(42)
	canon := &fakeMovieCanon{getResp: movie.Canon{ID: 7, TMDBID: &tmdbID}}
	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:   &fakeMovieTMDB{resp: &tmdb.MovieResponse{ID: 42}},
		Movies: canon,
		Clock:  func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)
	require.NoError(t, w.RefreshCast(context.Background(), 7, "ru-RU"))
	assert.Zero(t, canon.castMarkCalls, "unwired RefreshCast is a no-op")
}

// A movie with no tmdb id → skip (Radarr orphan), no stamp.
func TestMovieWorker_RefreshCast_NoTMDBIDSkips(t *testing.T) {
	canon := &fakeMovieCanon{getResp: movie.Canon{ID: 7, TMDBID: nil}}
	w, _, _, _ := newRefreshCastWorker(t, &tmdb.MovieResponse{}, canon)
	require.NoError(t, w.RefreshCast(context.Background(), 7, "ru-RU"))
	assert.Zero(t, canon.castMarkCalls)
}
