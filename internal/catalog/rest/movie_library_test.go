package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// fakeMovieLibraryRepo is a focused ports.MovieLibraryRepository fake capturing
// the resolved filter/sort/paging and returning canned rows.
type fakeMovieLibraryRepo struct {
	rows  []ports.MovieLibraryRow
	total int
	err   error

	gotFilter ports.MovieLibraryFilter
	gotSort   ports.MovieLibrarySort
	gotLimit  int
	gotOffset int
}

func (f *fakeMovieLibraryRepo) List(_ context.Context, filter ports.MovieLibraryFilter, sort ports.MovieLibrarySort, limit, offset int) ([]ports.MovieLibraryRow, int, error) {
	f.gotFilter, f.gotSort, f.gotLimit, f.gotOffset = filter, sort, limit, offset
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.rows, f.total, nil
}

func doMovieList(t *testing.T, repo ports.MovieLibraryRepository, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.GET("/api/v1/movies", NewMovieLibraryHandler(repo, nil).List)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/movies"+query, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestMovieLibraryHandler_List_Defaults(t *testing.T) {
	poster := "/p.jpg"
	repo := &fakeMovieLibraryRepo{
		rows: []ports.MovieLibraryRow{{
			TMDBID: 438631, Title: "Dune", PosterAsset: &poster,
			Monitored: true, HasFile: true, Instances: []string{"r1", "r2"},
			SizeOnDisk: 5_000_000_000, UpdatedAt: time.Unix(1700000000, 0).UTC(),
		}},
		total: 1,
	}
	w := doMovieList(t, repo, "")
	require.Equal(t, http.StatusOK, w.Code)

	// Defaults applied.
	assert.Equal(t, ports.MovieLibraryStateAll, repo.gotFilter.State)
	assert.Equal(t, ports.MovieLibrarySortUpdatedDesc, repo.gotSort)
	assert.Equal(t, 24, repo.gotLimit)
	assert.Equal(t, 0, repo.gotOffset)

	var body dto.MovieLibraryList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
	it := body.Items[0]
	assert.Equal(t, 438631, it.TMDBID)
	assert.Equal(t, "Dune", it.Title)
	require.NotNil(t, it.Poster)
	assert.Equal(t, "/p.jpg", *it.Poster)
	assert.Equal(t, []string{"r1", "r2"}, it.Instances)
	assert.True(t, it.HasFile)
	assert.Equal(t, int64(5_000_000_000), it.SizeOnDisk)
	assert.Equal(t, 1, body.Total)
	assert.False(t, body.HasMore)
	assert.Empty(t, body.NextCursor)
}

func TestMovieLibraryHandler_List_FiltersSortPaging(t *testing.T) {
	repo := &fakeMovieLibraryRepo{rows: nil, total: 0}
	w := doMovieList(t, repo, "?state=downloaded&sort=title_asc&q=dune&limit=10&cursor=20")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, ports.MovieLibraryStateDownloaded, repo.gotFilter.State)
	assert.Equal(t, "dune", repo.gotFilter.Search)
	assert.Equal(t, ports.MovieLibrarySortTitleAsc, repo.gotSort)
	assert.Equal(t, 10, repo.gotLimit)
	assert.Equal(t, 20, repo.gotOffset)
}

func TestMovieLibraryHandler_List_HasMoreCursor(t *testing.T) {
	rows := make([]ports.MovieLibraryRow, 10)
	for i := range rows {
		rows[i] = ports.MovieLibraryRow{TMDBID: i + 1, Title: "m", Instances: []string{"r1"}}
	}
	repo := &fakeMovieLibraryRepo{rows: rows, total: 42}
	w := doMovieList(t, repo, "?limit=10&cursor=0")
	require.Equal(t, http.StatusOK, w.Code)
	var body dto.MovieLibraryList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.True(t, body.HasMore, "offset 0 + 10 rows < 42 total → has_more")
	assert.Equal(t, "10", body.NextCursor)
}

func TestMovieLibraryHandler_List_EmptyInstancesNeverNull(t *testing.T) {
	repo := &fakeMovieLibraryRepo{
		rows:  []ports.MovieLibraryRow{{TMDBID: 1, Title: "x", Instances: nil}},
		total: 1,
	}
	w := doMovieList(t, repo, "")
	require.Equal(t, http.StatusOK, w.Code)
	// instances must serialize as [] not null so the FE can .map without guard.
	assert.Contains(t, w.Body.String(), `"instances":[]`)
}

func TestMovieLibraryHandler_List_BadParams(t *testing.T) {
	cases := []struct{ name, query string }{
		{"bad_state", "?state=bogus"},
		{"bad_sort", "?sort=bogus"},
		{"bad_limit_zero", "?limit=0"},
		{"bad_limit_over", "?limit=101"},
		{"bad_limit_nan", "?limit=abc"},
		{"bad_cursor", "?cursor=-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doMovieList(t, &fakeMovieLibraryRepo{}, tc.query)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestMovieLibraryHandler_List_RepoError500(t *testing.T) {
	w := doMovieList(t, &fakeMovieLibraryRepo{err: errors.New("boom")}, "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
