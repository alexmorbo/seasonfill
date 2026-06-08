package sonarr

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/domain"
)

func newPosterClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New("test", srv.URL, "secret", 2*time.Second,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
}

func TestGetMediaCover_FullSizeDefaultVariant(t *testing.T) {
	body := []byte("hello-poster")
	c := newPosterClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v3/MediaCover/42/poster.jpg", r.URL.Path)
		assert.Equal(t, "secret", r.Header.Get("X-Api-Key"))
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("ETag", `W/"7"`)
		_, _ = w.Write(body)
	})
	resp, err := c.GetMediaCover(context.Background(), 42, PosterFull, "")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.False(t, resp.NotModified)
	assert.Equal(t, "image/jpeg", resp.ContentType)
	assert.Equal(t, `W/"7"`, resp.ETag)
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestGetMediaCover_SmallSizeUsesResizedVariant(t *testing.T) {
	c := newPosterClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v3/MediaCover/7/poster-500.jpg", r.URL.Path)
		_, _ = w.Write([]byte("x"))
	})
	resp, err := c.GetMediaCover(context.Background(), 7, PosterSmall, "")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, "image/jpeg", resp.ContentType, "missing upstream content-type defaults to image/jpeg")
}

func TestGetMediaCover_IfNoneMatchForwardedAnd304Returned(t *testing.T) {
	c := newPosterClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, `"v1"`, r.Header.Get("If-None-Match"))
		w.Header().Set("ETag", `"v1"`)
		w.WriteHeader(http.StatusNotModified)
	})
	resp, err := c.GetMediaCover(context.Background(), 1, PosterFull, `"v1"`)
	require.NoError(t, err)
	assert.True(t, resp.NotModified)
	assert.Equal(t, `"v1"`, resp.ETag)
	assert.Nil(t, resp.Body)
}

func TestGetMediaCover_404SurfacesStatusError(t *testing.T) {
	c := newPosterClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := c.GetMediaCover(context.Background(), 1, PosterFull, "")
	require.Error(t, err)
	var se *StatusError
	require.True(t, errors.As(err, &se))
	assert.Equal(t, http.StatusNotFound, se.Status)
}

func TestGetMediaCover_401MapsToUnauthorizedSentinel(t *testing.T) {
	c := newPosterClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := c.GetMediaCover(context.Background(), 1, PosterFull, "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInstanceUnauthorized))
}

func TestGetMediaCover_NetworkErrorMapsToNetworkSentinel(t *testing.T) {
	c := New("test", "http://127.0.0.1:1", "k", 150*time.Millisecond,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	_, err := c.GetMediaCover(context.Background(), 1, PosterFull, "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInstanceNetwork))
}

func TestGetMediaCover_TextContentTypeOverriddenToJpeg(t *testing.T) {
	c := newPosterClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("oops"))
	})
	resp, err := c.GetMediaCover(context.Background(), 1, PosterFull, "")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, "image/jpeg", resp.ContentType)
}
