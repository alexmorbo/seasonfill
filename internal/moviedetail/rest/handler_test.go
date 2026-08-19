package rest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// mdMediaLookupStub is a deterministic media.HashLookupPort for the M-FIX-1
// poster-resolution regression: maps source URL → stored hash; a miss raises
// ports.ErrNotFound so the resolver's miss path engages.
type mdMediaLookupStub struct {
	byURL map[string]string
}

func (s mdMediaLookupStub) HashForSourceURL(_ context.Context, url string) (string, error) {
	if h, ok := s.byURL[url]; ok {
		return h, nil
	}
	return "", ports.ErrNotFound
}

func (s mdMediaLookupStub) EnsurePending(_ context.Context, _, _, _ string) error {
	return nil
}

type stubCanon struct {
	canon movie.Canon
	err   error
}

func (s stubCanon) GetByTMDBID(_ context.Context, _ domain.TMDBID) (movie.Canon, error) {
	return s.canon, s.err
}

type stubI18n struct{}

func (stubI18n) Get(_ context.Context, _ domain.MovieID, _ string) (enrichpersistence.MovieI18nRow, error) {
	return enrichpersistence.MovieI18nRow{}, ports.ErrNotFound
}

type stubCollection struct{}

func (stubCollection) GetByTMDBCollectionID(_ context.Context, _ int) (movie.CollectionCanon, error) {
	return movie.CollectionCanon{}, ports.ErrNotFound
}

type stubMembership struct{ states []movie.StateEntry }

func (s stubMembership) ListActiveByMovieID(_ context.Context, _ domain.MovieID) ([]movie.StateEntry, error) {
	return s.states, nil
}

func newTestHandler(canon movie.Canon, canonErr error, states []movie.StateEntry) *Handler {
	uc := mdapp.New(
		stubCanon{canon: canon, err: canonErr},
		stubI18n{},
		stubCollection{},
		stubMembership{states: states},
	)
	return NewHandler(uc, nil, nil)
}

func doGet(h *Handler, param string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/movies/"+param+"?lang=ru-RU", nil)
	c.Params = gin.Params{{Key: "tmdb_id", Value: param}}
	h.Get(c)
	return w
}

func TestHandler_Get_OK(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(693134)
	avail := "released"
	canon := movie.Canon{
		ID: domain.MovieID(42), TMDBID: &tid, Title: "Dune: Part Two",
		PosterAsset: new("/p.jpg"),
	}
	h := newTestHandler(canon, nil, []movie.StateEntry{
		{InstanceName: "radarr-alpha", RadarrMovieID: 7, MovieID: 42, Monitored: true, HasFile: true, Availability: &avail, SizeOnDiskBytes: 5},
	})

	w := doGet(h, "693134")
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.MovieDetailResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 693134, body.TMDBID)
	assert.Equal(t, "Dune: Part Two", body.Title)
	require.Len(t, body.Library, 1)
	assert.Equal(t, "radarr-alpha", body.Library[0].InstanceName)
	assert.Equal(t, int64(5), body.Library[0].SizeOnDisk)
	assert.Contains(t, body.Degraded, "movie_i18n")
}

func TestHandler_Get_BadRequestNonNumeric(t *testing.T) {
	t.Parallel()
	h := newTestHandler(movie.Canon{}, nil, nil)

	w := doGet(h, "not-a-number")
	require.Equal(t, http.StatusBadRequest, w.Code)

	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "BAD_REQUEST", body.Code)
}

// TestHandler_Get_ResolvesImages is the M-FIX-1 regression: the movie-detail
// poster must be the resolved sha256 media hash, NOT the raw canon poster_asset
// path the FE cannot hand to /api/v1/media/:hash.
func TestHandler_Get_ResolvesImages(t *testing.T) {
	t.Parallel()
	const posterHash = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	rawPoster := "/2cxhvw.jpg"
	tid := domain.TMDBID(693134)

	newHandler := func(resolver *media.Resolver) *Handler {
		canon := movie.Canon{
			ID: domain.MovieID(42), TMDBID: &tid, Title: "Dune: Part Two",
			PosterAsset: &rawPoster,
		}
		uc := mdapp.New(
			stubCanon{canon: canon},
			stubI18n{},
			stubCollection{},
			stubMembership{},
		)
		return NewHandler(uc, resolver, nil)
	}

	t.Run("resolver hit replaces raw path with media hash", func(t *testing.T) {
		lookup := mdMediaLookupStub{byURL: map[string]string{
			appmedia.BuildTMDBImageURL("w342", rawPoster): posterHash,
		}}
		resolver := media.NewResolver(lookup, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

		w := doGet(newHandler(resolver), "693134")
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var body dto.MovieDetailResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.NotNil(t, body.Poster)
		assert.Equal(t, posterHash, *body.Poster, "stored hash must replace raw path")
		assert.NotEqual(t, rawPoster, *body.Poster, "must NOT emit the raw /xxxx.jpg path")
	})

	t.Run("nil resolver preserves raw path (legacy)", func(t *testing.T) {
		w := doGet(newHandler(nil), "693134")
		require.Equal(t, http.StatusOK, w.Code)

		var body dto.MovieDetailResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.NotNil(t, body.Poster)
		assert.Equal(t, rawPoster, *body.Poster)
	})
}

func TestHandler_Get_NotFound(t *testing.T) {
	t.Parallel()
	h := newTestHandler(movie.Canon{}, ports.ErrNotFound, nil)

	w := doGet(h, "999999")
	require.Equal(t, http.StatusNotFound, w.Code)

	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "MOVIE_NOT_FOUND", body.Code)
}

func TestHandler_Get_MapsMoneyAndIdentityFields(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(1315772)
	canon := movie.Canon{
		ID: domain.MovieID(99), TMDBID: &tid, Title: "Flow",
		OriginalTitle: new("Straume"),
		Homepage:      new("https://www.dream-well.com/flow"),
		Budget:        new(int64(85000000)),
		Revenue:       new(int64(451746275)),
	}
	h := newTestHandler(canon, nil, nil)

	w := doGet(h, "1315772")
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.MovieDetailResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	require.NotNil(t, body.OriginalTitle)
	assert.Equal(t, "Straume", *body.OriginalTitle)
	require.NotNil(t, body.Homepage)
	assert.Equal(t, "https://www.dream-well.com/flow", *body.Homepage)
	require.NotNil(t, body.Budget)
	assert.Equal(t, int64(85000000), *body.Budget)
	require.NotNil(t, body.Revenue)
	assert.Equal(t, int64(451746275), *body.Revenue)
}

func TestHandler_Get_NilMoneyAndIdentityFields_Omitted(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(1315772)
	canon := movie.Canon{ID: domain.MovieID(99), TMDBID: &tid, Title: "Flow"}
	h := newTestHandler(canon, nil, nil)

	w := doGet(h, "1315772")
	require.Equal(t, http.StatusOK, w.Code)

	// Decode into a raw map to assert the keys are absent (omitempty), not just nil.
	var wire map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &wire))
	assert.NotContains(t, wire, "original_title")
	assert.NotContains(t, wire, "homepage")
	assert.NotContains(t, wire, "budget")
	assert.NotContains(t, wire, "revenue")

	var body dto.MovieDetailResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Nil(t, body.OriginalTitle)
	assert.Nil(t, body.Homepage)
	assert.Nil(t, body.Budget)
	assert.Nil(t, body.Revenue)
}
