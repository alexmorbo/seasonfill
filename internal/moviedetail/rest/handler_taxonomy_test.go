package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

type stubGenres struct {
	ids      []int64
	resolved []taxonomy.Genre
}

func (s stubGenres) ListByMovie(_ context.Context, _ domain.MovieID) ([]int64, error) {
	return s.ids, nil
}

func (s stubGenres) ListByIDsWithFallback(_ context.Context, _ []int64, _ string) ([]taxonomy.Genre, error) {
	return s.resolved, nil
}

type stubKeywords struct {
	ids      []int64
	resolved []taxonomy.Keyword
}

func (s stubKeywords) ListByMovie(_ context.Context, _ domain.MovieID) ([]int64, error) {
	return s.ids, nil
}

func (s stubKeywords) ListByIDsWithFallback(_ context.Context, _ []int64, _ string) ([]taxonomy.Keyword, error) {
	return s.resolved, nil
}

func TestHandler_Get_TaxonomyMapped(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(693134)
	canon := movie.Canon{ID: domain.MovieID(42), TMDBID: &tid, Title: "Dune: Part Two"}
	uc := mdapp.New(
		stubCanon{canon: canon},
		stubI18n{},
		stubCollection{},
		stubMembership{},
	).WithTaxonomy(
		stubGenres{
			ids:      []int64{7},
			resolved: []taxonomy.Genre{{ID: 7, Name: "Фантастика", Language: "ru-RU"}},
		},
		stubKeywords{
			ids:      []int64{5},
			resolved: []taxonomy.Keyword{{ID: 5, Name: "dystopia", Language: "en-US"}},
		},
	)
	h := NewHandler(uc, nil, nil)

	w := doGet(h, "693134")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var body dto.MovieDetailResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Genres, 1)
	assert.Equal(t, int64(7), body.Genres[0].ID)
	assert.Equal(t, "Фантастика", body.Genres[0].Name)
	assert.Equal(t, "ru-RU", body.Genres[0].Language)
	require.Len(t, body.Keywords, 1)
	assert.Equal(t, "dystopia", body.Keywords[0].Name)
	assert.Equal(t, "en-US", body.Keywords[0].Language)
}
