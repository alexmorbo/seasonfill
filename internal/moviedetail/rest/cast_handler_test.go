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
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

type stubCastCanon struct{ canon movie.Canon }

func (s stubCastCanon) GetByTMDBID(context.Context, domain.TMDBID) (movie.Canon, error) {
	return s.canon, nil
}

type stubCastRows struct {
	rows []enrichpersistence.PersonCredit
}

func (s stubCastRows) ListByMediaWithTextFallback(context.Context, string, int, string) ([]enrichpersistence.PersonCredit, error) {
	return s.rows, nil
}

type stubCastPeople struct{ rows map[int64]people.Person }

func (s stubCastPeople) ListByIDsWithNameFallback(_ context.Context, ids []int64, _ string) ([]people.Person, error) {
	out := make([]people.Person, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.rows[id])
	}
	return out, nil
}

type stubTitleLang struct{ lang string }

func (s stubTitleLang) TitleLanguage(context.Context, domain.MovieID, string) (string, error) {
	return s.lang, nil
}

func castRow(personID int64, character *string, order *int) enrichpersistence.PersonCredit {
	return database.PersonCreditModel{
		PersonID: personID, TMDBCreditID: "c" + string(rune('0'+personID)),
		MediaType: "movie", TMDBMediaID: 603, Title: "The Matrix",
		Kind: "cast", CharacterName: character, CreditOrder: order,
	}
}

// buildCastRouter wires the handler with NO resolver (raw profile paths) + the given
// title language, returning the gin engine.
func buildCastRouter(t *testing.T, titleLang string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	canon := stubCastCanon{canon: movie.Canon{ID: 7}}
	rows := stubCastRows{rows: []enrichpersistence.PersonCredit{
		castRow(1, new("Neo"), new(2)),
		castRow(2, new("Trinity"), new(0)),
		castRow(3, new("Morpheus"), new(1)),
	}}
	ppl := stubCastPeople{rows: map[int64]people.Person{
		1: {ID: 1, Name: "Zed Actor"},
		2: {ID: 2, Name: "Amy Actor"},
		3: {ID: 3, Name: "Mike Actor"},
	}}
	uc := mdapp.NewCastUseCase(canon, rows, ppl, stubTitleLang{lang: titleLang})
	h := NewMovieCastHandler(uc, nil, nil)
	r := gin.New()
	r.GET("/movies/:tmdb_id/cast", h.Get)
	return r
}

func doCastGet(t *testing.T, r *gin.Engine, url string) (*httptest.ResponseRecorder, dto.MovieCastResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	r.ServeHTTP(w, req)
	var body dto.MovieCastResponse
	if w.Code == http.StatusOK {
		b, _ := io.ReadAll(w.Body)
		require.NoError(t, json.Unmarshal(b, &body))
	}
	return w, body
}

func ids(ms []dto.MovieCastMember) []int64 {
	out := make([]int64, len(ms))
	for i, m := range ms {
		out[i] = m.PersonID
	}
	return out
}

func TestMovieCastHandler_DefaultSortIsCredit(t *testing.T) {
	r := buildCastRouter(t, "en-US")
	w, body := doCastGet(t, r, "/movies/603/cast?lang=en-US")
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, body.Cast, 3)
	// credit_order ASC: id2(0), id3(1), id1(2)
	assert.Equal(t, []int64{2, 3, 1}, ids(body.Cast))
	assert.Equal(t, "Trinity", *body.Cast[0].CharacterName)
}

func TestMovieCastHandler_ServedLanguagePresent(t *testing.T) {
	r := buildCastRouter(t, "en-US")
	_, body := doCastGet(t, r, "/movies/603/cast?lang=en-US")
	assert.Equal(t, "en-US", body.ServedLanguage)
	assert.Equal(t, []string{}, body.Degraded)
}

func TestMovieCastHandler_MissingLangDegraded(t *testing.T) {
	r := buildCastRouter(t, "en-US") // title only in en-US
	_, body := doCastGet(t, r, "/movies/603/cast?lang=ru-RU")
	assert.Equal(t, "en-US", body.ServedLanguage)
	assert.Equal(t, []string{"missing_lang"}, body.Degraded)
}

func TestMovieCastHandler_ExplicitSortName(t *testing.T) {
	r := buildCastRouter(t, "en-US")
	_, body := doCastGet(t, r, "/movies/603/cast?sort=name")
	// name ASC: Amy(2), Mike(3), Zed(1)
	assert.Equal(t, []int64{2, 3, 1}, ids(body.Cast))
}

func TestMovieCastHandler_BadID(t *testing.T) {
	r := buildCastRouter(t, "en-US")
	w, _ := doCastGet(t, r, "/movies/0/cast")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
