package rest_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	searchapp "github.com/alexmorbo/seasonfill/internal/search/app"
	searchdomain "github.com/alexmorbo/seasonfill/internal/search/domain"
	searchrest "github.com/alexmorbo/seasonfill/internal/search/rest"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

type fakeSearcher struct {
	result   searchdomain.LibrarySearchResult
	err      error
	gotQ     string
	gotLang  string
	gotLimit int
	gotScope searchapp.Scope
	gotTypes searchapp.TypeFilter
	calls    int
}

func (f *fakeSearcher) Search(_ context.Context, q, language string, limitPerGroup int, scope searchapp.Scope, types searchapp.TypeFilter) (searchdomain.LibrarySearchResult, error) {
	f.calls++
	f.gotQ, f.gotLang, f.gotLimit, f.gotScope, f.gotTypes = q, language, limitPerGroup, scope, types
	if f.err != nil {
		return searchdomain.LibrarySearchResult{}, f.err
	}
	return f.result, nil
}

func newRouter(t *testing.T, search searchrest.LibrarySearcher) *gin.Engine {
	t.Helper()
	return newRouterWithResolver(t, search, nil)
}

func newRouterWithResolver(t *testing.T, search searchrest.LibrarySearcher, resolver *media.Resolver) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := searchrest.NewSearchHandler(search, resolver, slog.Default())
	r := gin.New()
	r.GET("/search", h.Search)
	return r
}

// missingHashLookup is a HashLookupPort whose lookups always miss (hash="",
// err=nil → the resolver's non-error miss path). Under SetUnifiedResolve(true)
// this drives Resolve to mint the deterministic eager content hash of the CDN
// URL — byte-identical to what /movies mints for the same raw path + size.
type missingHashLookup struct{}

func (missingHashLookup) HashForSourceURL(context.Context, string) (string, error) {
	return "", nil
}
func (missingHashLookup) EnsurePending(context.Context, string, string, string) error { return nil }

// unifiedResolver builds a real media.Resolver on the always-miss lookup with
// the unified always-emit-hash contract on, so Resolve returns eager content
// hashes (the same ones /movies produces).
func unifiedResolver() *media.Resolver {
	r := media.NewResolver(missingHashLookup{}, nil, nil, slog.Default())
	r.SetUnifiedResolve(true)
	return r
}

// expectMediaHash mints the hash the resolver returns for (raw, size) — the
// same value /movies would carry for the identical path.
func expectMediaHash(raw, size string) string {
	return appmedia.HashFromURL(appmedia.BuildTMDBImageURL(size, raw))
}

var hex64Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

func doGET(t *testing.T, r *gin.Engine, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func tmdb(v int) *shareddomain.TMDBID { return new(shareddomain.TMDBID(v)) }

func fullResult() searchdomain.LibrarySearchResult {
	return searchdomain.LibrarySearchResult{
		Series: []searchdomain.SeriesHit{{
			SeriesID: shareddomain.SeriesID(11), TMDBID: tmdb(1399),
			Title: "Game of Thrones", Year: new(2011),
			PosterPath: new("poster-s"), BackdropPath: new("backdrop-s"),
		}},
		Movies: []searchdomain.MovieHit{{
			MovieID: shareddomain.MovieID(22), TMDBID: tmdb(603),
			Title: "The Matrix", Year: new(1999),
			PosterPath: new("poster-m"), BackdropPath: nil,
		}},
		Collections: []searchdomain.CollectionHit{{
			CollectionID: searchdomain.CollectionID(33), TMDBID: tmdb(2344),
			Name: "The Matrix Collection", PosterPath: new("poster-c"),
		}},
		People: []searchdomain.PersonHit{{
			PersonID: searchdomain.PersonID(44), TMDBID: tmdb(6384),
			Name: "Keanu Reeves", ProfilePath: new("profile-p"),
			KnownFor: new("Acting"),
		}},
	}
}

func TestSearch_AllGroupsMapped(t *testing.T) {
	t.Parallel()
	f := &fakeSearcher{result: fullResult()}
	r := newRouter(t, f)

	w := doGET(t, r, "/search?q=matrix")
	require.Equal(t, http.StatusOK, w.Code)

	var resp searchrest.SearchResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Equal(t, "matrix", resp.Query)
	assert.Equal(t, "all", resp.Scope)
	assert.Equal(t, []string{"collection", "movie", "person", "series"}, resp.Types)

	require.Len(t, resp.Series, 1)
	s := resp.Series[0]
	assert.Equal(t, int64(11), s.ID)
	require.NotNil(t, s.TMDBID)
	assert.Equal(t, int64(1399), *s.TMDBID)
	assert.Equal(t, "Game of Thrones", s.Title)
	require.NotNil(t, s.Year)
	assert.Equal(t, 2011, *s.Year)
	assert.Equal(t, "poster-s", *s.PosterPath)
	assert.Equal(t, "backdrop-s", *s.BackdropPath)
	assert.Equal(t, "library", s.Source)

	require.Len(t, resp.Movies, 1)
	assert.Equal(t, int64(22), resp.Movies[0].ID)
	assert.Nil(t, resp.Movies[0].BackdropPath)
	assert.Equal(t, "library", resp.Movies[0].Source)

	require.Len(t, resp.Collections, 1)
	assert.Equal(t, int64(33), resp.Collections[0].ID)
	assert.Equal(t, "The Matrix Collection", resp.Collections[0].Name)
	assert.Equal(t, "library", resp.Collections[0].Source)

	require.Len(t, resp.People, 1)
	assert.Equal(t, int64(44), resp.People[0].ID)
	assert.Equal(t, "Keanu Reeves", resp.People[0].Name)
	require.NotNil(t, resp.People[0].KnownFor)
	assert.Equal(t, "Acting", *resp.People[0].KnownFor)
	assert.Equal(t, "library", resp.People[0].Source)

	assert.Equal(t, "en-US", f.gotLang)
	assert.Equal(t, 0, f.gotLimit)
}

func TestSearch_EmptyGroupsSerializeAsArrays(t *testing.T) {
	t.Parallel()
	f := &fakeSearcher{result: searchdomain.LibrarySearchResult{}}
	r := newRouter(t, f)

	w := doGET(t, r, "/search?q=zzz")
	require.Equal(t, http.StatusOK, w.Code)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	for _, group := range []string{"series", "movies", "collections", "people"} {
		require.Contains(t, raw, group)
		assert.JSONEq(t, "[]", string(raw[group]), "group %q must serialize as []", group)
	}
}

func TestSearch_MinQuery(t *testing.T) {
	t.Parallel()
	f := &fakeSearcher{result: fullResult()}
	r := newRouter(t, f)

	for _, target := range []string{"/search", "/search?q=", "/search?q=%20%20"} {
		w := doGET(t, r, target)
		assert.Equal(t, http.StatusBadRequest, w.Code, target)
	}
	over := "/search?q=" + strings.Repeat("a", 101)
	w := doGET(t, r, over)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	assert.Equal(t, 0, f.calls, "use case must not run on rejected queries")
}

func TestSearch_ScopeCatalogAndAll(t *testing.T) {
	t.Parallel()
	for _, scope := range []string{"catalog", "all", "library"} {
		f := &fakeSearcher{result: fullResult()}
		r := newRouter(t, f)
		w := doGET(t, r, "/search?q=matrix&scope="+scope)
		require.Equal(t, http.StatusOK, w.Code, scope)

		var resp searchrest.SearchResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, scope, resp.Scope)
		assert.Len(t, resp.Series, 1, "library hits present for scope=%s", scope)
		assert.Len(t, resp.Movies, 1)
		assert.Len(t, resp.People, 1)
		assert.Len(t, resp.Collections, 1)
	}

	f := &fakeSearcher{result: fullResult()}
	r := newRouter(t, f)
	w := doGET(t, r, "/search?q=x&scope=bogus")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSearch_TypesFilter(t *testing.T) {
	t.Parallel()
	f := &fakeSearcher{result: fullResult()}
	r := newRouter(t, f)

	w := doGET(t, r, "/search?q=matrix&types=series,movie")
	require.Equal(t, http.StatusOK, w.Code)

	var resp searchrest.SearchResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, []string{"movie", "series"}, resp.Types)
	assert.Len(t, resp.Series, 1)
	assert.Len(t, resp.Movies, 1)
	assert.Empty(t, resp.Collections)
	assert.Empty(t, resp.People)

	var rawmap map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rawmap))
	assert.JSONEq(t, "[]", string(rawmap["collections"]))
	assert.JSONEq(t, "[]", string(rawmap["people"]))

	w2 := doGET(t, r, "/search?q=matrix&types=series,bogus")
	assert.Equal(t, http.StatusBadRequest, w2.Code)
}

func TestSearch_LimitClamping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		query string
		want  int
		code  int
	}{
		{"unset defaults to 0", "/search?q=x", 0, http.StatusOK},
		{"in range", "/search?q=x&limit=5", 5, http.StatusOK},
		{"above cap clamps to 50", "/search?q=x&limit=999", 50, http.StatusOK},
		{"zero rejected", "/search?q=x&limit=0", 0, http.StatusBadRequest},
		{"negative rejected", "/search?q=x&limit=-3", 0, http.StatusBadRequest},
		{"non-integer rejected", "/search?q=x&limit=abc", 0, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeSearcher{result: searchdomain.LibrarySearchResult{}}
			r := newRouter(t, f)
			w := doGET(t, r, tc.query)
			require.Equal(t, tc.code, w.Code)
			if tc.code == http.StatusOK {
				assert.Equal(t, tc.want, f.gotLimit)
			}
		})
	}
}

func TestSearch_UseCaseError500(t *testing.T) {
	t.Parallel()
	f := &fakeSearcher{err: context.DeadlineExceeded}
	r := newRouter(t, f)
	w := doGET(t, r, "/search?q=matrix")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestSearch_CatalogSourceProjected(t *testing.T) {
	t.Parallel()
	f := &fakeSearcher{result: searchdomain.LibrarySearchResult{
		Movies: []searchdomain.MovieHit{{
			MovieID: 0, TMDBID: tmdb(603), Title: "The Matrix",
			Source: searchdomain.SourceCatalog,
		}},
	}}
	r := newRouter(t, f)
	w := doGET(t, r, "/search?q=matrix&scope=catalog")
	require.Equal(t, http.StatusOK, w.Code)

	var resp searchrest.SearchResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Movies, 1)
	assert.Equal(t, "catalog", resp.Movies[0].Source)
	assert.Equal(t, searchapp.ScopeCatalog, f.gotScope)
}

func TestSearch_AllScope_LibraryAndCatalogSources(t *testing.T) {
	t.Parallel()
	f := &fakeSearcher{result: searchdomain.LibrarySearchResult{
		Series: []searchdomain.SeriesHit{
			{SeriesID: shareddomain.SeriesID(11), TMDBID: tmdb(1399), Title: "in-library", Source: searchdomain.SourceLibrary},
			{TMDBID: tmdb(999), Title: "from-catalog", Source: searchdomain.SourceCatalog},
		},
	}}
	r := newRouter(t, f)
	w := doGET(t, r, "/search?q=q&scope=all")
	require.Equal(t, http.StatusOK, w.Code)

	var resp searchrest.SearchResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Series, 2)
	assert.Equal(t, "library", resp.Series[0].Source) // library-first (D8)
	assert.Equal(t, "from-catalog", resp.Series[1].Title)
	assert.Equal(t, "catalog", resp.Series[1].Source)
	assert.Equal(t, searchapp.ScopeAll, f.gotScope)
}

// TestSearch_ResolverMintsMediaHashes proves BUG-1: with a real resolver
// wired, the media-path fields (poster_path / backdrop_path / profile_path)
// carry the /media wire hash — byte-identical to what /movies mints for the
// same raw path — instead of the raw TMDB path.
func TestSearch_ResolverMintsMediaHashes(t *testing.T) {
	t.Parallel()
	const (
		posterRaw   = "/aAnTxjpg.jpg"
		backdropRaw = "/bBackdrop.jpg"
		profileRaw  = "/pProfile.jpg"
	)
	f := &fakeSearcher{result: searchdomain.LibrarySearchResult{
		Movies: []searchdomain.MovieHit{{
			MovieID: shareddomain.MovieID(22), TMDBID: tmdb(603),
			Title: "The Matrix", PosterPath: new(posterRaw), BackdropPath: new(backdropRaw),
			Source: searchdomain.SourceLibrary,
		}},
		People: []searchdomain.PersonHit{{
			PersonID: searchdomain.PersonID(44), TMDBID: tmdb(6384),
			Name: "Keanu Reeves", ProfilePath: new(profileRaw),
			Source: searchdomain.SourceLibrary,
		}},
	}}
	r := newRouterWithResolver(t, f, unifiedResolver())

	w := doGET(t, r, "/search?q=matrix")
	require.Equal(t, http.StatusOK, w.Code)

	var resp searchrest.SearchResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp.Movies, 1)
	require.NotNil(t, resp.Movies[0].PosterPath)
	gotPoster := *resp.Movies[0].PosterPath
	assert.Equal(t, expectMediaHash(posterRaw, "w342"), gotPoster, "poster_path == /movies-minted hash")
	assert.Regexp(t, hex64Re, gotPoster, "poster_path is 64 lowercase hex")
	assert.NotEqual(t, posterRaw, gotPoster, "poster_path is NOT the raw path")

	require.NotNil(t, resp.Movies[0].BackdropPath)
	assert.Equal(t, expectMediaHash(backdropRaw, "w1280"), *resp.Movies[0].BackdropPath)
	assert.Regexp(t, hex64Re, *resp.Movies[0].BackdropPath)

	require.Len(t, resp.People, 1)
	require.NotNil(t, resp.People[0].ProfilePath)
	gotProfile := *resp.People[0].ProfilePath
	assert.Equal(t, expectMediaHash(profileRaw, "w185"), gotProfile, "profile_path == /movies-minted hash")
	assert.Regexp(t, hex64Re, gotProfile, "profile_path is 64 lowercase hex")
	assert.NotEqual(t, profileRaw, gotProfile, "profile_path is NOT the raw path")
}

// TestSearch_NilResolverKeepsRawPaths documents the nil-OK pass-through: with
// no resolver the media-path fields serialize the raw TMDB paths unchanged.
func TestSearch_NilResolverKeepsRawPaths(t *testing.T) {
	t.Parallel()
	f := &fakeSearcher{result: searchdomain.LibrarySearchResult{
		Movies: []searchdomain.MovieHit{{
			MovieID: shareddomain.MovieID(22), Title: "The Matrix",
			PosterPath: new("/raw-poster.jpg"), Source: searchdomain.SourceLibrary,
		}},
		People: []searchdomain.PersonHit{{
			PersonID: searchdomain.PersonID(44), Name: "Keanu Reeves",
			ProfilePath: new("/raw-profile.jpg"), Source: searchdomain.SourceLibrary,
		}},
	}}
	r := newRouter(t, f) // nil resolver

	w := doGET(t, r, "/search?q=matrix")
	require.Equal(t, http.StatusOK, w.Code)

	var resp searchrest.SearchResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Movies, 1)
	require.NotNil(t, resp.Movies[0].PosterPath)
	assert.Equal(t, "/raw-poster.jpg", *resp.Movies[0].PosterPath)
	require.Len(t, resp.People, 1)
	require.NotNil(t, resp.People[0].ProfilePath)
	assert.Equal(t, "/raw-profile.jpg", *resp.People[0].ProfilePath)
}

func TestSearch_LibraryScopeSourceUnchanged(t *testing.T) {
	t.Parallel()
	f := &fakeSearcher{result: searchdomain.LibrarySearchResult{
		Movies: []searchdomain.MovieHit{{MovieID: shareddomain.MovieID(1), Title: "x", Source: searchdomain.SourceLibrary}},
	}}
	r := newRouter(t, f)
	w := doGET(t, r, "/search?q=x&scope=library&types=movie")
	require.Equal(t, http.StatusOK, w.Code)

	var resp searchrest.SearchResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Movies, 1)
	assert.Equal(t, "library", resp.Movies[0].Source)
	assert.Equal(t, searchapp.ScopeLibrary, f.gotScope)
	assert.True(t, f.gotTypes.Movie)
	assert.False(t, f.gotTypes.Series)
}
