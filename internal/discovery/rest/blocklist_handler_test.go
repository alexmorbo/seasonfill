package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/discovery/persistence"
)

type fakeStore struct {
	inserted  []disco.BlocklistKind
	rows      []persistence.ResolvedBlocklistRow
	deleted   []int64
	nextID    int64
	insertErr error
	listErr   error
	deleteErr error
}

func (f *fakeStore) Insert(_ context.Context, k disco.BlocklistKind, refID int64, label *string) (disco.BlocklistEntry, error) {
	f.inserted = append(f.inserted, k)
	if f.insertErr != nil {
		return disco.BlocklistEntry{}, f.insertErr
	}
	f.nextID++
	return disco.BlocklistEntry{ID: f.nextID, Kind: k, RefID: refID, Label: label}, nil
}

func (f *fakeStore) DeleteByID(_ context.Context, id int64) error {
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

func (f *fakeStore) ListResolved(_ context.Context, _ string) ([]persistence.ResolvedBlocklistRow, error) {
	return f.rows, f.listErr
}

// fakeLoader drives a real app.BlocklistCache (Refresh must not panic in
// Create/Delete). Empty sets are fine for handler tests.
type fakeLoader struct{}

func (fakeLoader) LoadBlockSets(context.Context) ([]int64, []int64, error) { return nil, nil, nil }

// fakeKeywords is a scripted rest.KeywordSearcher for the keyword-search
// handler tests.
type fakeKeywords struct {
	hits []KeywordHit
	err  error
}

func (f fakeKeywords) SearchKeyword(context.Context, string) ([]KeywordHit, error) {
	return f.hits, f.err
}

func newTestHandler(t *testing.T, store BlocklistStore) *BlocklistHandler {
	t.Helper()
	return newTestHandlerKW(t, store, nil)
}

func newTestHandlerKW(t *testing.T, store BlocklistStore, kw KeywordSearcher) *BlocklistHandler {
	t.Helper()
	cache := app.NewBlocklistCache(fakeLoader{})
	return NewBlocklistHandler(store, cache, kw, nil /*resolver*/, slog.Default())
}

func doReq(t *testing.T, h *BlocklistHandler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/discovery/blocklist", h.Create)
	r.GET("/api/v1/discovery/blocklist", h.List)
	r.DELETE("/api/v1/discovery/blocklist/:id", h.Delete)
	r.GET("/api/v1/discovery/keyword-search", h.KeywordSearch)
	var rd *bytes.Reader
	if body != "" {
		rd = bytes.NewReader([]byte(body))
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestBlocklistHandler_Create(t *testing.T) {
	cases := []struct {
		name, body string
		want       int
	}{
		{"tmdb ok", `{"kind":"tmdb","ref_id":1399}`, http.StatusCreated},
		{"keyword ok", `{"kind":"keyword","ref_id":210024,"label":"anime"}`, http.StatusCreated},
		{"bad kind", `{"kind":"nope","ref_id":1}`, http.StatusBadRequest},
		{"ref_id zero", `{"kind":"tmdb","ref_id":0}`, http.StatusBadRequest},
		{"malformed", `{`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(t, &fakeStore{})
			w := doReq(t, h, http.MethodPost, "/api/v1/discovery/blocklist", tc.body)
			assert.Equal(t, tc.want, w.Code)
			if tc.want == http.StatusCreated {
				var got blocklistCreateResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
				assert.Positive(t, got.ID)
				assert.NotEmpty(t, got.Kind)
			}
		})
	}
}

func TestBlocklistHandler_List(t *testing.T) {
	title := "Game of Thrones"
	poster := "/abc.jpg"
	label := "anime"
	store := &fakeStore{rows: []persistence.ResolvedBlocklistRow{
		{ID: 2, Kind: "tmdb", RefID: 1399, Title: &title, PosterAsset: &poster},
		{ID: 1, Kind: "keyword", RefID: 210024, Label: &label},
	}}
	h := newTestHandler(t, store)
	w := doReq(t, h, http.MethodGet, "/api/v1/discovery/blocklist", "")
	require.Equal(t, http.StatusOK, w.Code)

	// Bare JSON array (FE contract) — NOT wrapped in {items}.
	var items []BlocklistItem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &items))
	require.Len(t, items, 2)
	assert.Equal(t, "tmdb", items[0].Kind)
	require.NotNil(t, items[0].Title)
	assert.Equal(t, title, *items[0].Title)
	// resolver nil → poster_hash mirrors the raw asset path.
	require.NotNil(t, items[0].PosterHash)
	assert.Equal(t, poster, *items[0].PosterHash)
	assert.Equal(t, "keyword", items[1].Kind)
	require.NotNil(t, items[1].Label)
}

func TestBlocklistHandler_List_EmptyIsArray(t *testing.T) {
	h := newTestHandler(t, &fakeStore{})
	w := doReq(t, h, http.MethodGet, "/api/v1/discovery/blocklist", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "[]", w.Body.String())
}

func TestBlocklistHandler_Delete(t *testing.T) {
	store := &fakeStore{}
	h := newTestHandler(t, store)
	assert.Equal(t, http.StatusNoContent,
		doReq(t, h, http.MethodDelete, "/api/v1/discovery/blocklist/5", "").Code)
	assert.Equal(t, []int64{5}, store.deleted)
	assert.Equal(t, http.StatusBadRequest,
		doReq(t, h, http.MethodDelete, "/api/v1/discovery/blocklist/abc", "").Code)
}

func TestBlocklistHandler_KeywordSearch(t *testing.T) {
	// wired → 200 bare JSON array [{id,name}].
	h := newTestHandlerKW(t, &fakeStore{}, fakeKeywords{hits: []KeywordHit{{ID: 210024, Name: "anime"}}})
	w := doReq(t, h, http.MethodGet, "/api/v1/discovery/keyword-search?q=anim", "")
	require.Equal(t, http.StatusOK, w.Code)
	var hits []KeywordHit
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &hits))
	require.Len(t, hits, 1)
	assert.Equal(t, 210024, hits[0].ID)
	assert.Equal(t, "anime", hits[0].Name)

	// no hits → bare empty array, never null.
	h2 := newTestHandlerKW(t, &fakeStore{}, fakeKeywords{hits: nil})
	w2 := doReq(t, h2, http.MethodGet, "/api/v1/discovery/keyword-search?q=zzz", "")
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "[]", w2.Body.String())

	// unwired searcher → 503.
	h3 := newTestHandler(t, &fakeStore{})
	assert.Equal(t, http.StatusServiceUnavailable,
		doReq(t, h3, http.MethodGet, "/api/v1/discovery/keyword-search?q=x", "").Code)

	// blank q → 400.
	assert.Equal(t, http.StatusBadRequest,
		doReq(t, h, http.MethodGet, "/api/v1/discovery/keyword-search?q=", "").Code)

	// upstream error → 502.
	h4 := newTestHandlerKW(t, &fakeStore{}, fakeKeywords{err: context.DeadlineExceeded})
	assert.Equal(t, http.StatusBadGateway,
		doReq(t, h4, http.MethodGet, "/api/v1/discovery/keyword-search?q=anime", "").Code)
}
