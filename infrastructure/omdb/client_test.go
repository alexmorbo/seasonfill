package omdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(Config{
		APIKey:     "secret",
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		BaseURL:    srv.URL,
	})
	require.NoError(t, err)
	return c
}

func TestClient_GetByIMDB_Success(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"Title": "Breaking Bad",
			"Rated": "TV-MA",
			"Awards": "Won 16 Primetime Emmys",
			"imdbRating": "9.5",
			"imdbVotes": "2,034,123",
			"Response": "True"
		}`))
	}))
	resp, err := c.GetByIMDB(context.Background(), "tt0903747")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "9.5", resp.IMDBRating)
	assert.Equal(t, "2,034,123", resp.IMDBVotes)
	assert.Equal(t, "TV-MA", resp.Rated)
}

func TestClient_GetByIMDB_NotFound_SentinelError(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Response":"False","Error":"Movie not found!"}`))
	}))
	_, err := c.GetByIMDB(context.Background(), "tt0000000")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestClient_GetByIMDB_IncorrectIMDBID_SentinelError(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Response":"False","Error":"Incorrect IMDb ID."}`))
	}))
	_, err := c.GetByIMDB(context.Background(), "tt9999999")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestClient_GetByIMDB_InvalidKey_SentinelError(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Response":"False","Error":"Invalid API key!"}`))
	}))
	_, err := c.GetByIMDB(context.Background(), "tt0000001")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidKey))
}

func TestClient_GetByIMDB_DailyLimit_SentinelError(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Response":"False","Error":"Daily limit reached!"}`))
	}))
	_, err := c.GetByIMDB(context.Background(), "tt0000001")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrDailyLimit))
}

func TestClient_GetByIMDB_5xx_APIError(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	_, err := c.GetByIMDB(context.Background(), "tt0000001")
	require.Error(t, err)
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, 500, apiErr.Status)
}

func TestClient_GetByIMDB_NetworkError_Wrapped(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // immediately closed so Do() errors
	c, err := New(Config{
		APIKey:     "secret",
		HTTPClient: &http.Client{Timeout: 500 * time.Millisecond},
		BaseURL:    srv.URL,
	})
	require.NoError(t, err)

	_, err = c.GetByIMDB(context.Background(), "tt0000001")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrNotFound))
	assert.False(t, errors.Is(err, ErrInvalidKey))
	assert.False(t, errors.Is(err, ErrDailyLimit))
}

func TestClient_GetByIMDB_PassesIMDBIDAndAPIKey(t *testing.T) {
	t.Parallel()
	var gotIMDB, gotKey string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIMDB = r.URL.Query().Get("i")
		gotKey = r.URL.Query().Get("apikey")
		_, _ = w.Write([]byte(`{"Response":"True","Title":"x"}`))
	}))
	_, err := c.GetByIMDB(context.Background(), "tt0903747")
	require.NoError(t, err)
	assert.Equal(t, "tt0903747", gotIMDB)
	assert.Equal(t, "secret", gotKey)
}

func TestClient_GetByIMDB_EmptyIMDBID_ProgrammerError(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not be called")
	}))
	_, err := c.GetByIMDB(context.Background(), "")
	require.Error(t, err)
}

func TestNew_RequiresAPIKey(t *testing.T) {
	t.Parallel()
	_, err := New(Config{HTTPClient: &http.Client{}})
	require.Error(t, err)
}

func TestNew_RequiresHTTPClient(t *testing.T) {
	t.Parallel()
	_, err := New(Config{APIKey: "k"})
	require.Error(t, err)
}
