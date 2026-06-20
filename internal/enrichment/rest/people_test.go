package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	apppeople "github.com/alexmorbo/seasonfill/internal/enrichment/app/people"
	domenrich "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	dompeople "github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// newPeopleRouter mounts the F-2c-1 typed-error middleware so the
// handler's c.Error(err) dispatch reaches the JSON envelope.
func newPeopleRouter(h *PeopleHandler) *gin.Engine {
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(handlerTestLogger()))
	r.GET("/api/v1/people/:tmdbId", h.Get)
	return r
}

// --- handler-local fakes (minimal — heavy lifting is in usecase_test.go) ---

type peopleHandlerFakePeople struct {
	person  dompeople.Person
	errTMDB error
	errID   error
}

func (f peopleHandlerFakePeople) GetByTMDBID(_ context.Context, _ domain.TMDBID) (dompeople.Person, error) {
	if f.errTMDB != nil {
		return dompeople.Person{}, f.errTMDB
	}
	return f.person, nil
}

func (f peopleHandlerFakePeople) GetWithBio(_ context.Context, _ int64, _ string) (dompeople.Person, error) {
	if f.errID != nil {
		return dompeople.Person{}, f.errID
	}
	return f.person, nil
}

type peopleHandlerFakeCredits struct {
	rows []dompeople.PersonCredit
}

func (f peopleHandlerFakeCredits) ListByPerson(_ context.Context, _ int64) ([]dompeople.PersonCredit, error) {
	return f.rows, nil
}

type peopleHandlerFakeSeriesByTMDB struct {
	rows map[int]series.Canon
}

func (f peopleHandlerFakeSeriesByTMDB) GetByTMDBID(_ context.Context, tmdbID domain.TMDBID) (series.Canon, error) {
	if c, ok := f.rows[int(tmdbID)]; ok {
		return c, nil
	}
	return series.Canon{}, ports.ErrNotFound
}

type peopleHandlerFakeSeriesCache struct {
	rows map[domain.SeriesID][]series.CacheEntry
}

func (f peopleHandlerFakeSeriesCache) ListBySeriesID(_ context.Context, seriesID domain.SeriesID) ([]series.CacheEntry, error) {
	return f.rows[seriesID], nil
}

type peopleHandlerFakeSyncLog struct {
	row domenrich.SyncLog
	err error
}

func (f peopleHandlerFakeSyncLog) GetLastSync(_ context.Context, _ domenrich.EntityType, _ int64, _ domenrich.Source) (domenrich.SyncLog, error) {
	if f.err != nil {
		return domenrich.SyncLog{}, f.err
	}
	return f.row, nil
}

type peopleHandlerFakeEnqueuer struct {
	calls int
}

func (f *peopleHandlerFakeEnqueuer) Enqueue(_ appenrich.EntityKind, _ int64, _ appenrich.Priority) {
	f.calls++
}

func handlerTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// buildHandler wires a real apppeople.UseCase with the supplied fakes.
func buildHandler(uc *apppeople.UseCase) *PeopleHandler {
	return NewPeopleHandler(uc, handlerTestLogger())
}

func happyHandlerUseCase(t *testing.T) *apppeople.UseCase {
	t.Helper()
	tmdbPersonID := domain.TMDBID(4495)
	person := dompeople.Person{
		ID:        1,
		TMDBID:    &tmdbPersonID,
		Hydration: dompeople.HydrationFull,
		Name:      "Pedro Pascal",
	}
	canon := series.Canon{
		ID:     42,
		TMDBID: new(domain.TMDBID(100)),
		Title:  "The Last of Us",
		Year:   new(2023),
	}
	credits := []dompeople.PersonCredit{
		{
			ID:            1,
			MediaType:     "tv",
			TMDBMediaID:   100,
			Title:         "The Last of Us",
			Kind:          dompeople.SeriesCreditCast,
			CharacterName: new("Joel Miller"),
		},
		{
			ID:          2,
			MediaType:   "tv",
			TMDBMediaID: 999,
			Title:       "Other Show",
			Kind:        dompeople.SeriesCreditCast,
		},
	}
	return apppeople.NewUseCase(apppeople.Deps{
		People:        peopleHandlerFakePeople{person: person},
		PersonCredits: peopleHandlerFakeCredits{rows: credits},
		SeriesByTMDB: peopleHandlerFakeSeriesByTMDB{
			rows: map[int]series.Canon{100: canon},
		},
		SeriesCache: peopleHandlerFakeSeriesCache{
			rows: map[domain.SeriesID][]series.CacheEntry{
				42: {{InstanceName: "alpha", SonarrSeriesID: 7777}},
			},
		},
		SyncLog: peopleHandlerFakeSyncLog{err: ports.ErrNotFound},
		Logger:  handlerTestLogger(),
	})
}

func TestPeopleHandler_Get_200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := happyHandlerUseCase(t)
	h := buildHandler(uc)
	r := gin.New()
	r.GET("/api/v1/people/:tmdbId", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/people/4495", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body dto.PersonDetailResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, int64(1), body.Person.ID)
	require.Len(t, body.LibraryCredits, 1)
	require.Len(t, body.LibraryCredits[0].Instances, 1)
	assert.Equal(t, domain.InstanceName("alpha"), body.LibraryCredits[0].Instances[0].Instance)
	assert.Equal(t, domain.SonarrSeriesID(7777), body.LibraryCredits[0].Instances[0].SonarrSeriesID)
	require.Len(t, body.OtherCredits, 1)
	assert.Equal(t, "tv", body.OtherCredits[0].MediaType)
}

func TestPeopleHandler_Get_400_NonNumeric(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := happyHandlerUseCase(t)
	h := buildHandler(uc)
	r := gin.New()
	r.GET("/api/v1/people/:tmdbId", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/people/abc", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "invalid tmdb id", body.Error)
}

func TestPeopleHandler_Get_400_ZeroOrNegative(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := happyHandlerUseCase(t)
	h := buildHandler(uc)
	r := gin.New()
	r.GET("/api/v1/people/:tmdbId", h.Get)

	for _, path := range []string{"/api/v1/people/0", "/api/v1/people/-5"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, "path=%s", path)
	}
}

func TestPeopleHandler_Get_404_UnknownPerson(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := apppeople.NewUseCase(apppeople.Deps{
		People:        peopleHandlerFakePeople{errTMDB: ports.ErrNotFound},
		PersonCredits: peopleHandlerFakeCredits{},
		SeriesByTMDB:  peopleHandlerFakeSeriesByTMDB{},
		SeriesCache:   peopleHandlerFakeSeriesCache{},
		SyncLog:       peopleHandlerFakeSyncLog{err: ports.ErrNotFound},
		Logger:        handlerTestLogger(),
	})
	h := buildHandler(uc)
	r := newPeopleRouter(h)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/people/4495", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	// F-2c-1: middleware emits the typed slug on `error`. The use case
	// passes through bare ports.ErrNotFound (no typed wrap yet — that's
	// F-2c-2's job), so the middleware falls back to the generic
	// `not_found` slug.
	assert.Equal(t, "not_found", body.Error)
}

func TestPeopleHandler_Get_500_OnNonNotFoundError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := apppeople.NewUseCase(apppeople.Deps{
		People:        peopleHandlerFakePeople{errTMDB: errors.New("db boom")},
		PersonCredits: peopleHandlerFakeCredits{},
		SeriesByTMDB:  peopleHandlerFakeSeriesByTMDB{},
		SeriesCache:   peopleHandlerFakeSeriesCache{},
		SyncLog:       peopleHandlerFakeSyncLog{err: ports.ErrNotFound},
		Logger:        handlerTestLogger(),
	})
	h := buildHandler(uc)
	r := newPeopleRouter(h)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/people/4495", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestPeopleHandler_Get_StubPersonReturns200WithDegraded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmdbPersonID := domain.TMDBID(4495)
	person := dompeople.Person{
		ID:        1,
		TMDBID:    &tmdbPersonID,
		Hydration: dompeople.HydrationStub,
		Name:      "Pedro Pascal",
	}
	enq := &peopleHandlerFakeEnqueuer{}
	uc := apppeople.NewUseCase(apppeople.Deps{
		People:        peopleHandlerFakePeople{person: person},
		PersonCredits: peopleHandlerFakeCredits{},
		SeriesByTMDB:  peopleHandlerFakeSeriesByTMDB{},
		SeriesCache:   peopleHandlerFakeSeriesCache{},
		SyncLog:       peopleHandlerFakeSyncLog{err: ports.ErrNotFound},
		Enqueuer:      enq,
		Logger:        handlerTestLogger(),
	})
	h := buildHandler(uc)
	r := gin.New()
	r.GET("/api/v1/people/:tmdbId", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/people/4495", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "stub person returns 200, NEVER 202")

	var body dto.PersonDetailResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, []string{"tmdb_person"}, body.Degraded)
	assert.Equal(t, 1, enq.calls)
}

func TestPeopleHandler_Get_SortQueryPropagates(t *testing.T) {
	// Different sort param produces deterministic ordering across two
	// in-library credits. The handler does not pre-validate sort —
	// unknown values fall through to the use case's default.
	gin.SetMode(gin.TestMode)
	tmdbPersonID := domain.TMDBID(4495)
	person := dompeople.Person{ID: 1, TMDBID: &tmdbPersonID, Hydration: dompeople.HydrationFull, Name: "p"}
	canonA := series.Canon{ID: 42, Title: "Alpha Show", Year: new(2020)}
	canonZ := series.Canon{ID: 43, Title: "Zulu Show", Year: new(2025)}
	credits := []dompeople.PersonCredit{
		{ID: 1, MediaType: "tv", TMDBMediaID: 100, Title: "Alpha Show", Kind: dompeople.SeriesCreditCast},
		{ID: 2, MediaType: "tv", TMDBMediaID: 200, Title: "Zulu Show", Kind: dompeople.SeriesCreditCast},
	}
	uc := apppeople.NewUseCase(apppeople.Deps{
		People:        peopleHandlerFakePeople{person: person},
		PersonCredits: peopleHandlerFakeCredits{rows: credits},
		SeriesByTMDB: peopleHandlerFakeSeriesByTMDB{
			rows: map[int]series.Canon{100: canonA, 200: canonZ},
		},
		SeriesCache: peopleHandlerFakeSeriesCache{
			rows: map[domain.SeriesID][]series.CacheEntry{
				42: {{InstanceName: "alpha"}},
				43: {{InstanceName: "alpha"}},
			},
		},
		SyncLog: peopleHandlerFakeSyncLog{err: ports.ErrNotFound},
		Logger:  handlerTestLogger(),
	})
	h := buildHandler(uc)
	r := gin.New()
	r.GET("/api/v1/people/:tmdbId", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/people/4495?sort=title", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var body dto.PersonDetailResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.LibraryCredits, 2)
	assert.Equal(t, "Alpha Show", body.LibraryCredits[0].Title)
	assert.Equal(t, "Zulu Show", body.LibraryCredits[1].Title)
}

// TestSeriesPersonHandler_OtherCredits_NewFields_Story307 asserts the
// 3 new optional OtherCreditEntry fields (department, original_title,
// vote_count) surface in the JSON when the underlying PersonCredit
// has them populated. Mirrors the happy-path handler test shape but
// scoped to one OtherCredit row.
func TestSeriesPersonHandler_OtherCredits_NewFields_Story307(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	original := "Narcos (Original)"
	dept := "Production"
	character := "Javier Peña"
	votes := 9876
	rating := 8.5
	releaseDate := time.Date(2017, 9, 1, 0, 0, 0, 0, time.UTC)
	tmdbPersonID := domain.TMDBID(4495)

	credits := []dompeople.PersonCredit{
		{
			ID:            10,
			PersonID:      1,
			MediaType:     "tv",
			TMDBMediaID:   300,
			TMDBCreditID:  "cr-narcos",
			Kind:          dompeople.SeriesCreditCast,
			Title:         "Narcos",
			OriginalTitle: &original,
			CharacterName: &character,
			Department:    &dept,
			ReleaseDate:   &releaseDate,
			TMDBRating:    &rating,
			TMDBVotes:     &votes,
		},
	}

	detail := &apppeople.PersonDetail{
		Person: dompeople.Person{
			ID:        1,
			TMDBID:    &tmdbPersonID,
			Hydration: dompeople.HydrationFull,
			Name:      "Pedro Pascal",
		},
		OtherCredits: []apppeople.OtherCredit{{Credit: credits[0]}},
	}

	resp := toPersonDetailResponse(detail)
	require.Len(t, resp.OtherCredits, 1)
	got := resp.OtherCredits[0]

	require.NotNil(t, got.Department, "department dropped")
	assert.Equal(t, "Production", *got.Department)
	require.NotNil(t, got.OriginalTitle, "original_title dropped")
	assert.Equal(t, "Narcos (Original)", *got.OriginalTitle)
	require.NotNil(t, got.VoteCount, "vote_count dropped")
	assert.Equal(t, 9876, *got.VoteCount)

	// JSON round-trip — confirms the field tags are correct and the
	// optional fields show up in the wire payload.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.JSON(http.StatusOK, resp)

	var decoded dto.PersonDetailResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &decoded))
	require.Len(t, decoded.OtherCredits, 1)
	wire := decoded.OtherCredits[0]
	require.NotNil(t, wire.Department)
	assert.Equal(t, "Production", *wire.Department)
	require.NotNil(t, wire.OriginalTitle)
	assert.Equal(t, "Narcos (Original)", *wire.OriginalTitle)
	require.NotNil(t, wire.VoteCount)
	assert.Equal(t, 9876, *wire.VoteCount)
}

// TestPeopleUseCase_ResolvesAssets exercises the Story 315 wiring:
// the use case calls MediaResolver.Resolve for person.profile_asset
// and library_credits[].poster_asset. The stub resolver records its
// calls; the assertions verify (a) Resolve was called with the right
// (size, kind) tags, and (b) the returned hex string replaces the raw
// path in the response.
func TestPeopleUseCase_ResolvesAssets(t *testing.T) {
	t.Parallel()

	profilePath := "/abc.jpg"
	posterPath := "/def.jpg"
	tmdbPersonID := domain.TMDBID(5887)

	resolver := &recordingResolver{
		responses: map[string]string{
			"w185|/abc.jpg": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"w342|/def.jpg": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
	}

	person := dompeople.Person{
		ID:           1,
		TMDBID:       &tmdbPersonID,
		Hydration:    dompeople.HydrationFull,
		Name:         "Pedro Pascal",
		ProfileAsset: &profilePath,
	}
	credits := []dompeople.PersonCredit{{
		ID: 10, PersonID: 1, MediaType: "tv", TMDBMediaID: 300, Kind: dompeople.SeriesCreditCast,
	}}
	canon := series.Canon{ID: 42, Title: "FROM", PosterAsset: &posterPath}

	uc := apppeople.NewUseCase(apppeople.Deps{
		People:        peopleHandlerFakePeople{person: person},
		PersonCredits: peopleHandlerFakeCredits{rows: credits},
		SeriesByTMDB:  peopleHandlerFakeSeriesByTMDB{rows: map[int]series.Canon{300: canon}},
		SeriesCache: peopleHandlerFakeSeriesCache{
			rows: map[domain.SeriesID][]series.CacheEntry{42: {{InstanceName: "homelab", SonarrSeriesID: 369}}},
		},
		SyncLog:       peopleHandlerFakeSyncLog{err: ports.ErrNotFound},
		MediaResolver: resolver,
		Logger:        handlerTestLogger(),
	})

	detail, err := uc.Get(t.Context(), 5887, "en-US", "recent")
	require.NoError(t, err)
	require.NotNil(t, detail.Person.ProfileAsset, "profile_asset should be a hash, not nil")
	assert.Len(t, *detail.Person.ProfileAsset, 64, "profile_asset should be 64-char sha256 hex")
	require.Len(t, detail.LibraryCredits, 1)
	require.NotNil(t, detail.LibraryCredits[0].Canon.PosterAsset)
	assert.Len(t, *detail.LibraryCredits[0].Canon.PosterAsset, 64)

	// Resolver was called with the right (size, kind) tags.
	assert.Contains(t, resolver.calls, "w185|/abc.jpg")
	assert.Contains(t, resolver.calls, "w342|/def.jpg")
}

// recordingResolver is the test stub for apppeople.MediaResolver.
// Story 316 widened the interface with ResolveSync; we delegate both
// methods to the same shared call recorder so existing assertions
// continue to pass even though the use case now routes the hero
// portrait through ResolveSync.
type recordingResolver struct {
	responses map[string]string
	calls     []string
}

func (r *recordingResolver) resolveOne(rawPath *string, size string) *string {
	if rawPath == nil || *rawPath == "" {
		return nil
	}
	k := size + "|" + *rawPath
	r.calls = append(r.calls, k)
	if v, ok := r.responses[k]; ok {
		return &v
	}
	return nil
}

func (r *recordingResolver) Resolve(_ context.Context, rawPath *string, size, _ string) *string {
	return r.resolveOne(rawPath, size)
}

func (r *recordingResolver) ResolveSync(_ context.Context, rawPath *string, size, _ string) *string {
	return r.resolveOne(rawPath, size)
}
