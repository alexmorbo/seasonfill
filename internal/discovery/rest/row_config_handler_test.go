package rest_test

import (
	"bytes"
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

// fakeRowConfigWriter records Replace/DeleteAll calls and scripts errors.
type fakeRowConfigWriter struct {
	replaced     []disco.Row
	replaceCalls int
	deleteCalls  int
	replaceErr   error
	deleteErr    error
}

func (f *fakeRowConfigWriter) Replace(_ context.Context, rows []disco.Row) error {
	f.replaceCalls++
	f.replaced = rows
	return f.replaceErr
}

func (f *fakeRowConfigWriter) DeleteAll(context.Context) error {
	f.deleteCalls++
	return f.deleteErr
}

func newHandler(t *testing.T, r discoveryrest.RowConfigLister, w discoveryrest.RowConfigWriter) *discoveryrest.RowConfigHandler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(httptest.NewRecorder(), &slog.HandlerOptions{Level: slog.LevelError}))
	return discoveryrest.NewRowConfigHandler(r, w, log)
}

func doGET(t *testing.T, repo discoveryrest.RowConfigLister) discoveryrest.RowConfigResponse {
	t.Helper()
	h := newHandler(t, repo, &fakeRowConfigWriter{})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/discovery/rows", nil)
	h.Handle(c)
	require.Equal(t, http.StatusOK, w.Code)
	var got discoveryrest.RowConfigResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got
}

func doPUT(t *testing.T, writer discoveryrest.RowConfigWriter, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := newHandler(t, &fakeRowConfigLister{}, writer)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/discovery/rows", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Save(c)
	return w
}

// doDELETE routes through a real gin engine (not a direct handler call) so a
// no-body 204 is flushed to the recorder — gin defers WriteHeaderNow, which
// only fires through ServeHTTP (matches the repo's sibling 204 test idiom).
func doDELETE(t *testing.T, writer discoveryrest.RowConfigWriter) *httptest.ResponseRecorder {
	t.Helper()
	h := newHandler(t, &fakeRowConfigLister{}, writer)
	r := gin.New()
	r.DELETE("/api/v1/discovery/rows", h.Reset)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/discovery/rows", nil)
	r.ServeHTTP(w, req)
	return w
}

func TestRowConfigHandler_Empty_ServesDefaults(t *testing.T) {
	t.Parallel()
	got := doGET(t, &fakeRowConfigLister{rows: nil})
	require.Len(t, got.Rows, 9)
	require.Equal(t, "trending", got.Rows[0].RowType)
	require.Zero(t, got.Rows[0].ID)
	require.NotNil(t, got.Rows[0].Params)
	for i, r := range got.Rows {
		require.Equal(t, i, r.Position, "positions dense 0..8")
	}
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
	got := doGET(t, repo)
	require.Len(t, got.Rows, 2)
	require.Equal(t, int64(10), got.Rows[0].ID)
	require.Equal(t, int64(20), got.Rows[1].ID)
	require.Equal(t, "popular", got.Rows[0].RowType)
	require.Equal(t, map[string]string{"with_genres": "18"}, got.Rows[1].Params)
}

func TestRowConfigHandler_RepoError_GracefulDegrade(t *testing.T) {
	t.Parallel()
	got := doGET(t, &fakeRowConfigLister{err: errors.New("db down")})
	require.Len(t, got.Rows, 9, "repo error falls back to code-default set")
	require.Equal(t, "trending", got.Rows[0].RowType)
}

func TestRowConfigHandler_Save_Valid_Persists(t *testing.T) {
	t.Parallel()
	writer := &fakeRowConfigWriter{}
	body := `{"rows":[
		{"row_type":"popular","source":"tmdb_discover","media_type":"tv","enabled":true,"title":"Популярное","params":{}},
		{"row_type":"genre","source":"tmdb_discover","media_type":"tv","enabled":false,"title":"Драмы","params":{"with_genres":"18","sort_by":"popularity.desc"}}
	]}`
	w := doPUT(t, writer, body)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, writer.replaceCalls)
	require.Len(t, writer.replaced, 2)
	// Positions densified by index.
	require.Equal(t, 0, writer.replaced[0].Position)
	require.Equal(t, 1, writer.replaced[1].Position)
	require.Equal(t, disco.RowTypeGenre, writer.replaced[1].RowType)
	require.False(t, writer.replaced[1].Enabled)
	require.Equal(t, map[string]string{"with_genres": "18", "sort_by": "popularity.desc"}, writer.replaced[1].Params)

	// 200 body echoes the persisted (dense) set.
	var got discoveryrest.RowConfigResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Rows, 2)
	require.Equal(t, 1, got.Rows[1].Position)
}

func TestRowConfigHandler_Save_Empty_Clears(t *testing.T) {
	t.Parallel()
	writer := &fakeRowConfigWriter{}
	w := doPUT(t, writer, `{"rows":[]}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, writer.replaceCalls)
	require.Len(t, writer.replaced, 0)
}

func TestRowConfigHandler_Save_UnknownEnum_400(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"row_type":   `{"rows":[{"row_type":"bogus","source":"tmdb_discover","media_type":"tv","title":"x","params":{}}]}`,
		"source":     `{"rows":[{"row_type":"genre","source":"bogus","media_type":"tv","title":"x","params":{}}]}`,
		"media_type": `{"rows":[{"row_type":"genre","source":"tmdb_discover","media_type":"bogus","title":"x","params":{}}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			writer := &fakeRowConfigWriter{}
			w := doPUT(t, writer, body)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Zero(t, writer.replaceCalls, "no write on validation failure")
		})
	}
}

func TestRowConfigHandler_Save_MalformedBody_400(t *testing.T) {
	t.Parallel()
	writer := &fakeRowConfigWriter{}
	w := doPUT(t, writer, `{"rows": not-json`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Zero(t, writer.replaceCalls)
}

func TestRowConfigHandler_Save_WriterError_500(t *testing.T) {
	t.Parallel()
	writer := &fakeRowConfigWriter{replaceErr: errors.New("db down")}
	w := doPUT(t, writer, `{"rows":[{"row_type":"popular","source":"tmdb_discover","media_type":"tv","title":"Популярное","params":{}}]}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, 1, writer.replaceCalls)
}

func TestRowConfigHandler_Reset_CallsDeleteAll(t *testing.T) {
	t.Parallel()
	writer := &fakeRowConfigWriter{}
	w := doDELETE(t, writer)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, 1, writer.deleteCalls)
}

func TestRowConfigHandler_Reset_Error_500(t *testing.T) {
	t.Parallel()
	writer := &fakeRowConfigWriter{deleteErr: errors.New("db down")}
	w := doDELETE(t, writer)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
