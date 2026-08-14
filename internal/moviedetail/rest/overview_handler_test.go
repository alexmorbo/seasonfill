package rest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// stubOverviewI18n is a configurable mdapp.I18nReader (the package-level stubI18n
// is a fixed always-NotFound stub, unsuitable for the happy path here).
type stubOverviewI18n struct {
	row enrichpersistence.MovieI18nRow
	err error
}

func (s stubOverviewI18n) Get(_ context.Context, _ domain.MovieID, _ string) (enrichpersistence.MovieI18nRow, error) {
	return s.row, s.err
}

func buildOverviewRouter(t *testing.T, titleLang string, row enrichpersistence.MovieI18nRow) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	tid := domain.TMDBID(603)
	canon := stubCanon{canon: movie.Canon{ID: 7, TMDBID: &tid, Title: "Canon Title"}}
	uc := mdapp.NewOverviewUseCase(canon, stubOverviewI18n{row: row}, stubTitleLang{lang: titleLang})
	h := NewMovieOverviewHandler(uc, nil)
	r := gin.New()
	r.GET("/movies/:tmdb_id/overview", h.Get)
	return r
}

func doOverviewGet(t *testing.T, r *gin.Engine, url string) (*httptest.ResponseRecorder, dto.MovieOverviewResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	r.ServeHTTP(w, req)
	var body dto.MovieOverviewResponse
	if w.Code == http.StatusOK {
		b, _ := io.ReadAll(w.Body)
		require.NoError(t, json.Unmarshal(b, &body))
	}
	return w, body
}

func TestMovieOverviewHandler_HappyPathShape(t *testing.T) {
	row := enrichpersistence.MovieI18nRow{Title: new("Дюна"), Overview: new("обзор"), Tagline: new("слоган")}
	r := buildOverviewRouter(t, "ru-RU", row)
	w, body := doOverviewGet(t, r, "/movies/603/overview?lang=ru-RU")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, domain.TMDBID(603), body.TMDBID)
	assert.Equal(t, "ru-RU", body.Lang)
	assert.Equal(t, "Дюна", body.Title)
	require.NotNil(t, body.Overview)
	assert.Equal(t, "обзор", *body.Overview)
	require.NotNil(t, body.Tagline)
	assert.Equal(t, "слоган", *body.Tagline)
	assert.Equal(t, "ru-RU", body.ServedLanguage)
	assert.Equal(t, []string{}, body.Degraded)
}

func TestMovieOverviewHandler_MissingLangDegraded(t *testing.T) {
	row := enrichpersistence.MovieI18nRow{Title: new("The Matrix"), Overview: new("synopsis")}
	r := buildOverviewRouter(t, "en-US", row) // title only in en-US
	_, body := doOverviewGet(t, r, "/movies/603/overview?lang=ru-RU")
	assert.Equal(t, "en-US", body.ServedLanguage)
	assert.Equal(t, []string{"missing_lang"}, body.Degraded)
}

func TestMovieOverviewHandler_BadID(t *testing.T) {
	r := buildOverviewRouter(t, "en-US", enrichpersistence.MovieI18nRow{})
	w, _ := doOverviewGet(t, r, "/movies/0/overview")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMovieOverviewHandler_TitleFallsBackToCanon(t *testing.T) {
	// Empty i18n row (all nil) → canon title, nil overview/tagline, empty degraded.
	r := buildOverviewRouter(t, "", enrichpersistence.MovieI18nRow{})
	w, body := doOverviewGet(t, r, "/movies/603/overview")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Canon Title", body.Title)
	assert.Nil(t, body.Overview)
	assert.Nil(t, body.Tagline)
	assert.Equal(t, []string{}, body.Degraded)
}
