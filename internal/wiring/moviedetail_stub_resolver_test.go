package wiring

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeStubTMDB is a canned movieDetailStubTMDB.
type fakeStubTMDB struct {
	resp *tmdb.MovieResponse
	err  error
}

func (f fakeStubTMDB) GetMovie(_ context.Context, _ int64, _ string) (*tmdb.MovieResponse, error) {
	return f.resp, f.err
}

// spyStubWriter records EnsureMovieStub invocations so a test can assert whether
// a row would have been written.
type spyStubWriter struct {
	calls   int
	lastTID domain.TMDBID
	lastArg struct {
		title, originalTitle, originalLanguage string
		poster, backdrop                       *string
	}
	err error
}

func (s *spyStubWriter) EnsureMovieStub(_ context.Context, tmdbID domain.TMDBID, _, title, originalTitle, originalLanguage string, poster, backdrop *string) (domain.MovieID, error) {
	s.calls++
	s.lastTID = tmdbID
	s.lastArg.title = title
	s.lastArg.originalTitle = originalTitle
	s.lastArg.originalLanguage = originalLanguage
	s.lastArg.poster = poster
	s.lastArg.backdrop = backdrop
	if s.err != nil {
		return 0, s.err
	}
	return domain.MovieID(101), nil
}

func newStubResolver(tmdbFake fakeStubTMDB, writer *spyStubWriter) *movieStubResolverAdapter {
	return &movieStubResolverAdapter{
		tmdb:   tmdbFake,
		writer: writer,
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// TestMovieStubResolver_TMDBResolves_SeedsStub — a resolving tmdb id seeds the
// stub with the TMDB identity fields and returns nil.
func TestMovieStubResolver_TMDBResolves_SeedsStub(t *testing.T) {
	t.Parallel()
	poster := "/p.jpg"
	backdrop := "/b.jpg"
	tmdbFake := fakeStubTMDB{resp: &tmdb.MovieResponse{
		ID: 1315772, Title: "Deadpool & Wolverine", OriginalTitle: "Deadpool & Wolverine",
		OriginalLanguage: "en", PosterPath: poster, BackdropPath: backdrop,
	}}
	writer := &spyStubWriter{}
	r := newStubResolver(tmdbFake, writer)

	err := r.EnsureStub(context.Background(), domain.TMDBID(1315772), "ru-RU")
	require.NoError(t, err)
	require.Equal(t, 1, writer.calls, "resolving tmdb → exactly one seed insert")
	assert.Equal(t, domain.TMDBID(1315772), writer.lastTID)
	assert.Equal(t, "Deadpool & Wolverine", writer.lastArg.title)
	assert.Equal(t, "en", writer.lastArg.originalLanguage)
	require.NotNil(t, writer.lastArg.poster)
	assert.Equal(t, poster, *writer.lastArg.poster)
}

// TestMovieStubResolver_TMDB404_NoRow_ErrNotFound — the guard: a TMDB 404 maps to
// ports.ErrNotFound and NO seed insert is attempted (no junk row).
func TestMovieStubResolver_TMDB404_NoRow_ErrNotFound(t *testing.T) {
	t.Parallel()
	tmdbFake := fakeStubTMDB{err: &tmdb.APIError{Status: 404, Body: `{"status_code":34}`}}
	writer := &spyStubWriter{}
	r := newStubResolver(tmdbFake, writer)

	err := r.EnsureStub(context.Background(), domain.TMDBID(999999999), "ru-RU")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound), "TMDB 404 → ErrNotFound (→ 404)")
	assert.Equal(t, 0, writer.calls, "NO junk row — the seed insert is never attempted")
}

// TestMovieStubResolver_TMDBEmptyPayload_NoRow_ErrNotFound — a 200 with an empty
// title is treated as not-found (no row).
func TestMovieStubResolver_TMDBEmptyPayload_NoRow_ErrNotFound(t *testing.T) {
	t.Parallel()
	tmdbFake := fakeStubTMDB{resp: &tmdb.MovieResponse{ID: 5, Title: ""}}
	writer := &spyStubWriter{}
	r := newStubResolver(tmdbFake, writer)

	err := r.EnsureStub(context.Background(), domain.TMDBID(5), "ru-RU")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
	assert.Equal(t, 0, writer.calls)
}

// TestMovieStubResolver_TMDBTransportError_500_NoRow — a non-404 TMDB error
// surfaces as-is (→ 500) and writes nothing.
func TestMovieStubResolver_TMDBTransportError_500_NoRow(t *testing.T) {
	t.Parallel()
	tmdbFake := fakeStubTMDB{err: &tmdb.APIError{Status: 503, Body: "upstream"}}
	writer := &spyStubWriter{}
	r := newStubResolver(tmdbFake, writer)

	err := r.EnsureStub(context.Background(), domain.TMDBID(1315772), "ru-RU")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ports.ErrNotFound), "5xx is 500, not 404")
	assert.Equal(t, 0, writer.calls)
}

// TestMovieStubResolver_WriterError_Wrapped — a seed-insert failure surfaces
// wrapped (→ 500), not ErrNotFound.
func TestMovieStubResolver_WriterError_Wrapped(t *testing.T) {
	t.Parallel()
	tmdbFake := fakeStubTMDB{resp: &tmdb.MovieResponse{ID: 7, Title: "X"}}
	writer := &spyStubWriter{err: errors.New("db down")}
	r := newStubResolver(tmdbFake, writer)

	err := r.EnsureStub(context.Background(), domain.TMDBID(7), "ru-RU")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ports.ErrNotFound))
	assert.Equal(t, 1, writer.calls)
}
