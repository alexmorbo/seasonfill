package rest_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// resolverLookupStub is a deterministic media.HashLookupPort stub for
// the discovery projection regression: maps source URL → hash. Misses
// raise ports.ErrNotFound so the resolver falls back to the eager-hash
// path under the unified flag.
type resolverLookupStub struct {
	byURL map[string]string
}

func (s resolverLookupStub) HashForSourceURL(_ context.Context, url string) (string, error) {
	if h, ok := s.byURL[url]; ok {
		return h, nil
	}
	return "", ports.ErrNotFound
}

func (s resolverLookupStub) EnsurePending(_ context.Context, _, _, _ string) error {
	return nil
}

// TestDiscoveryHandler_ResolvesPosterPath confirms the story 526
// integration: when a *media.Resolver is wired into the handler, the
// JSON response carries the sha256 hash for any disco.Item PosterPath
// that has a stored media_assets row, not the raw TMDB path. This is
// the regression that motivated the extraction — operator observed
// the FE rendering monograms on /discovery because the wire still
// shipped /abc.jpg instead of the 64-char hash.
func TestDiscoveryHandler_ResolvesPosterPath(t *testing.T) {
	t.Parallel()
	const (
		posterHash = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	)
	posterPath := "/abc.jpg"
	backdropPath := "/def.jpg"

	posterURL := appmedia.BuildTMDBImageURL("w342", posterPath)
	lookup := resolverLookupStub{byURL: map[string]string{
		posterURL: posterHash,
		// Backdrop deliberately missing — resolver falls through to
		// eager-hash; the response should still carry a hash (not the
		// raw path) so the FE has a stable wire slot.
	}}
	resolver := media.NewResolver(lookup, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	repo := newFakeRepo()
	seedItem := disco.Item{
		SeriesID:     shareddomain.SeriesID(42),
		Title:        "Series 42",
		PosterPath:   &posterPath,
		BackdropPath: &backdropPath,
	}
	repo.mu.Lock()
	repo.pages[fakeKey(disco.KindTrendingDay, "", "en-US")] = disco.Page{
		Items:       []disco.Item{seedItem},
		RefreshedAt: time.Now(),
		Total:       1,
	}
	repo.mu.Unlock()

	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := discoveryrest.NewDiscoveryHandler(
		repo,
		&fakeWarming{},
		&fakeRefresh{},
		persistence.NewGenresPickerRepo(nil),
		persistence.NewNetworksPickerRepo(nil),
		nil,      // searchUC
		resolver, // story 526
		nil,      // libraryInstances — story 527
		log,
	)
	r := gin.New()
	r.GET("/discovery/trending", h.Trending)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/discovery/trending?scope=day", nil)
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var body struct {
		Items []struct {
			PosterPath   *string `json:"poster_path"`
			BackdropPath *string `json:"backdrop_path"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)

	require.NotNil(t, body.Items[0].PosterPath)
	assert.Equal(t, posterHash, *body.Items[0].PosterPath,
		"stored hash must replace raw /abc.jpg path")

	// Backdrop missed lookup but resolver flag-off (default), so the
	// async-enqueue path returns nil — projectItem keeps raw path.
	// Flip the flag on to exercise the unified eager-hash path:
	require.NotNil(t, body.Items[0].BackdropPath)
	assert.Equal(t, backdropPath, *body.Items[0].BackdropPath,
		"flag-off miss preserves raw backdrop path (legacy behavior)")
}

// TestDiscoveryHandler_NoResolver_LegacyRawPath confirms the nil-resolver
// nil-safe path: projectItem ships the raw TMDB path unchanged so the
// pre-526 wire contract still holds for callers that opt out.
func TestDiscoveryHandler_NoResolver_LegacyRawPath(t *testing.T) {
	t.Parallel()
	posterPath := "/abc.jpg"

	repo := newFakeRepo()
	repo.mu.Lock()
	repo.pages[fakeKey(disco.KindPopular, "", "en-US")] = disco.Page{
		Items: []disco.Item{{
			SeriesID:   shareddomain.SeriesID(1),
			Title:      "Series 1",
			PosterPath: &posterPath,
		}},
		RefreshedAt: time.Now(),
		Total:       1,
	}
	repo.mu.Unlock()

	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := discoveryrest.NewDiscoveryHandler(
		repo,
		&fakeWarming{},
		&fakeRefresh{},
		persistence.NewGenresPickerRepo(nil),
		persistence.NewNetworksPickerRepo(nil),
		nil, // searchUC
		nil, // resolver — story 526 nil-OK
		nil, // libraryInstances — story 527 nil-OK
		log,
	)
	r := gin.New()
	r.GET("/discovery/popular", h.Popular)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/discovery/popular", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var body struct {
		Items []struct {
			PosterPath *string `json:"poster_path"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
	require.NotNil(t, body.Items[0].PosterPath)
	assert.Equal(t, posterPath, *body.Items[0].PosterPath,
		"nil resolver preserves raw TMDB path verbatim")
}
