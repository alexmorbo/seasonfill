package handlers

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

	appenrich "github.com/alexmorbo/seasonfill/application/enrichment"
	apppeople "github.com/alexmorbo/seasonfill/application/people"
	"github.com/alexmorbo/seasonfill/application/ports"
	domenrich "github.com/alexmorbo/seasonfill/domain/enrichment"
	dompeople "github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// --- handler-local fakes (minimal — heavy lifting is in usecase_test.go) ---

type peopleHandlerFakePeople struct {
	person  dompeople.Person
	errTMDB error
	errID   error
}

func (f peopleHandlerFakePeople) GetByTMDBID(_ context.Context, _ int) (dompeople.Person, error) {
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

func (f peopleHandlerFakeSeriesByTMDB) GetByTMDBID(_ context.Context, tmdbID int) (series.Canon, error) {
	if c, ok := f.rows[tmdbID]; ok {
		return c, nil
	}
	return series.Canon{}, ports.ErrNotFound
}

type peopleHandlerFakeSeriesCache struct {
	rows map[int64][]series.CacheEntry
}

func (f peopleHandlerFakeSeriesCache) ListBySeriesID(_ context.Context, seriesID int64) ([]series.CacheEntry, error) {
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

func handlerPtr[T any](v T) *T { return &v }

// buildHandler wires a real apppeople.UseCase with the supplied fakes.
func buildHandler(uc *apppeople.UseCase) *PeopleHandler {
	return NewPeopleHandler(uc, handlerTestLogger())
}

func happyHandlerUseCase(t *testing.T) *apppeople.UseCase {
	t.Helper()
	tmdbPersonID := 4495
	person := dompeople.Person{
		ID:        1,
		TMDBID:    &tmdbPersonID,
		Hydration: dompeople.HydrationFull,
		Name:      "Pedro Pascal",
	}
	canon := series.Canon{
		ID:     42,
		TMDBID: handlerPtr(100),
		Title:  "The Last of Us",
		Year:   handlerPtr(2023),
	}
	credits := []dompeople.PersonCredit{
		{
			ID:            1,
			MediaType:     "tv",
			TMDBMediaID:   100,
			Title:         "The Last of Us",
			Kind:          dompeople.SeriesCreditCast,
			CharacterName: handlerPtr("Joel Miller"),
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
			rows: map[int64][]series.CacheEntry{
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
	assert.Equal(t, "alpha", body.LibraryCredits[0].Instances[0].Instance)
	assert.Equal(t, 7777, body.LibraryCredits[0].Instances[0].SonarrSeriesID)
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
	r := gin.New()
	r.GET("/api/v1/people/:tmdbId", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/people/4495", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "person not found", body.Error)
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
	r := gin.New()
	r.GET("/api/v1/people/:tmdbId", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/people/4495", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestPeopleHandler_Get_StubPersonReturns200WithDegraded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmdbPersonID := 4495
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
	tmdbPersonID := 4495
	person := dompeople.Person{ID: 1, TMDBID: &tmdbPersonID, Hydration: dompeople.HydrationFull, Name: "p"}
	canonA := series.Canon{ID: 42, Title: "Alpha Show", Year: handlerPtr(2020)}
	canonZ := series.Canon{ID: 43, Title: "Zulu Show", Year: handlerPtr(2025)}
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
			rows: map[int64][]series.CacheEntry{
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
	tmdbPersonID := 4495

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
