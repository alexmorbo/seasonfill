package rest_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
)

// fakeRowConfigLister scripts List outcomes for the handler test.
type fakeRowConfigLister struct {
	rows []disco.Row
	err  error
}

func (f *fakeRowConfigLister) List(context.Context) ([]disco.Row, error) {
	return f.rows, f.err
}

func doRowConfigRequest(t *testing.T, repo discoveryrest.RowConfigLister) discoveryrest.RowConfigResponse {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(httptest.NewRecorder(), &slog.HandlerOptions{Level: slog.LevelError}))
	h := discoveryrest.NewRowConfigHandler(repo, log)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/discovery/rows", nil)
	h.Handle(c)

	require.Equal(t, http.StatusOK, w.Code)
	var got discoveryrest.RowConfigResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got
}

func TestRowConfigHandler_Empty_ServesDefaults(t *testing.T) {
	t.Parallel()
	got := doRowConfigRequest(t, &fakeRowConfigLister{rows: nil})

	require.Len(t, got.Rows, 7)
	require.Equal(t, "trending", got.Rows[0].RowType)
	require.Zero(t, got.Rows[0].ID)
	require.NotNil(t, got.Rows[0].Params)
	for i, r := range got.Rows {
		require.Equal(t, i, r.Position, "positions dense 0..6")
	}

	// params must serialize as {} not null.
	rawBody := mustMarshal(t, got)
	require.Contains(t, rawBody, `"params":{}`)
	require.NotContains(t, rawBody, `"params":null`)
}

func TestRowConfigHandler_Seeded_PassesThrough(t *testing.T) {
	t.Parallel()
	repo := &fakeRowConfigLister{rows: []disco.Row{
		{ID: 10, RowType: disco.RowTypePopular, Source: disco.SourceTMDBDiscover, MediaType: disco.MediaTypeTV, Position: 0, Enabled: true, Title: "Популярное", Params: map[string]string{}},
		{ID: 20, RowType: disco.RowTypeGenre, Source: disco.SourceTMDBDiscover, MediaType: disco.MediaTypeTV, Position: 1, Enabled: true, Title: "Драмы", Params: map[string]string{"with_genres": "18"}},
	}}
	got := doRowConfigRequest(t, repo)

	require.Len(t, got.Rows, 2)
	require.Equal(t, int64(10), got.Rows[0].ID)
	require.Equal(t, int64(20), got.Rows[1].ID)
	require.Equal(t, "popular", got.Rows[0].RowType)
	require.Equal(t, map[string]string{"with_genres": "18"}, got.Rows[1].Params)
}

func TestRowConfigHandler_RepoError_GracefulDegrade(t *testing.T) {
	t.Parallel()
	got := doRowConfigRequest(t, &fakeRowConfigLister{err: errors.New("db down")})

	require.Len(t, got.Rows, 7, "repo error falls back to code-default set")
	require.Equal(t, "trending", got.Rows[0].RowType)
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
