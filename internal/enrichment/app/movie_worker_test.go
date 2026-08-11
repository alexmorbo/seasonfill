package enrichment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeMovieTMDB struct {
	resp    *tmdb.MovieResponse
	err     error
	calls   int
	gotID   int64
	gotLang string
}

func (f *fakeMovieTMDB) GetMovie(_ context.Context, id int64, language string) (*tmdb.MovieResponse, error) {
	f.calls++
	f.gotID = id
	f.gotLang = language
	return f.resp, f.err
}

type fakeMovieCanon struct {
	getResp     movie.Canon
	getErr      error
	upsertCalls int
	upserted    movie.Canon
	markCalls   int
	markedID    domain.MovieID
}

func (f *fakeMovieCanon) Get(_ context.Context, _ domain.MovieID) (movie.Canon, error) {
	return f.getResp, f.getErr
}

func (f *fakeMovieCanon) Upsert(_ context.Context, c movie.Canon) (domain.MovieID, error) {
	f.upsertCalls++
	f.upserted = c
	return c.ID, nil
}

func (f *fakeMovieCanon) MarkTMDBSynced(_ context.Context, id domain.MovieID, _ time.Time) error {
	f.markCalls++
	f.markedID = id
	return nil
}

type fakeMovieI18n struct {
	calls    int
	movieID  domain.MovieID
	lang     string
	title    string
	overview string
	tagline  string
}

func (f *fakeMovieI18n) UpsertEnriched(_ context.Context, movieID domain.MovieID, lang, title, overview, tagline string, _, _ *string, _ time.Time) error {
	f.calls++
	f.movieID = movieID
	f.lang = lang
	f.title = title
	f.overview = overview
	f.tagline = tagline
	return nil
}

func movieCanonWithTMDB(id domain.MovieID, tmdbID int) movie.Canon {
	tid := domain.TMDBID(tmdbID)
	return movie.Canon{ID: id, TMDBID: &tid, Title: "stub", Hydration: movie.HydrationStub}
}

// TestMovieWorker_HandleForced_HydratesStubToFull asserts the worker fetches
// with the base language, upserts a FULL canon targeting the existing row,
// writes the localized i18n row under the base lang, and stamps MarkTMDBSynced —
// and that the mapped canon leaves OMDb-owned columns nil (COALESCE preserve).
func TestMovieWorker_HandleForced_HydratesStubToFull(t *testing.T) {
	tmdbClient := &fakeMovieTMDB{resp: &tmdb.MovieResponse{
		ID:            693134,
		IMDBID:        "tt15239678",
		Title:         "Dune: Part Two",
		OriginalTitle: "Dune: Part Two",
		Overview:      "overview text",
		Tagline:       "tagline text",
		Status:        "Released",
		ReleaseDate:   "2024-02-27",
		VoteAverage:   8.2,
		PosterPath:    "/p.jpg",
	}}
	canonRepo := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 693134)}
	i18n := &fakeMovieI18n{}

	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:   tmdbClient,
		Movies: canonRepo,
		I18n:   i18n,
		Clock:  func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) },
	})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 7))

	// language-aware fetch (#1184): default base lang en-US, keyed by tmdb_id.
	assert.Equal(t, 1, tmdbClient.calls)
	assert.Equal(t, int64(693134), tmdbClient.gotID)
	assert.Equal(t, tmdb.DefaultLanguage, tmdbClient.gotLang)

	// canon upsert: full hydration, targets the existing row by PK, real title.
	require.Equal(t, 1, canonRepo.upsertCalls)
	assert.Equal(t, movie.HydrationFull, canonRepo.upserted.Hydration)
	assert.Equal(t, domain.MovieID(7), canonRepo.upserted.ID)
	assert.Equal(t, "Dune: Part Two", canonRepo.upserted.Title)
	// OMDb-owned columns MUST be nil so the COALESCE Upsert preserves them.
	assert.Nil(t, canonRepo.upserted.IMDBRating)
	assert.Nil(t, canonRepo.upserted.OMDBRated)
	// tmdb_changed_at MUST be nil (sole-writer is the changes marker).
	assert.Nil(t, canonRepo.upserted.TMDBChangedAt)

	// localized i18n row under the base lang.
	require.Equal(t, 1, i18n.calls)
	assert.Equal(t, domain.MovieID(7), i18n.movieID)
	assert.Equal(t, tmdb.DefaultLanguage, i18n.lang)
	assert.Equal(t, "Dune: Part Two", i18n.title)
	assert.Equal(t, "overview text", i18n.overview)
	assert.Equal(t, "tagline text", i18n.tagline)

	// freshness stamp.
	require.Equal(t, 1, canonRepo.markCalls)
	assert.Equal(t, domain.MovieID(7), canonRepo.markedID)
}

// TestMovieWorker_HandleForced_NilTMDBSkips asserts a tmdb-less movie is a clean
// skip (no fetch, no upsert, no stamp, no error).
func TestMovieWorker_HandleForced_NilTMDBSkips(t *testing.T) {
	tmdbClient := &fakeMovieTMDB{}
	canonRepo := &fakeMovieCanon{getResp: movie.Canon{ID: 9, Title: "orphan", Hydration: movie.HydrationStub}}

	w, err := NewMovieWorker(MovieWorkerDeps{TMDB: tmdbClient, Movies: canonRepo})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 9))
	assert.Equal(t, 0, tmdbClient.calls, "no TMDB fetch for a tmdb-less movie")
	assert.Equal(t, 0, canonRepo.upsertCalls)
	assert.Equal(t, 0, canonRepo.markCalls)
}

// TestMovieWorker_HandleForced_FetchErrorNoStamp asserts a GetMovie error
// bubbles up and the freshness stamp is NOT written (so the movie re-picks).
func TestMovieWorker_HandleForced_FetchErrorNoStamp(t *testing.T) {
	tmdbClient := &fakeMovieTMDB{err: errors.New("boom")}
	canonRepo := &fakeMovieCanon{getResp: movieCanonWithTMDB(3, 111)}

	w, err := NewMovieWorker(MovieWorkerDeps{TMDB: tmdbClient, Movies: canonRepo})
	require.NoError(t, err)

	err = w.HandleForced(context.Background(), 3)
	require.Error(t, err)
	assert.Equal(t, 0, canonRepo.upsertCalls)
	assert.Equal(t, 0, canonRepo.markCalls, "no stamp on fetch failure")
}

// TestNewMovieWorker_RequiresPorts asserts the constructor rejects missing deps.
func TestNewMovieWorker_RequiresPorts(t *testing.T) {
	_, err := NewMovieWorker(MovieWorkerDeps{Movies: &fakeMovieCanon{}})
	require.Error(t, err)
	_, err = NewMovieWorker(MovieWorkerDeps{TMDB: &fakeMovieTMDB{}})
	require.Error(t, err)
}
