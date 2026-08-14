package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeGenresReader struct {
	ids      []int64
	resolved []taxonomy.Genre
	listErr  error
	resErr   error
}

func (f fakeGenresReader) ListByMovie(_ context.Context, _ domain.MovieID) ([]int64, error) {
	return f.ids, f.listErr
}

func (f fakeGenresReader) ListByIDsWithFallback(_ context.Context, _ []int64, _ string) ([]taxonomy.Genre, error) {
	return f.resolved, f.resErr
}

type fakeKeywordsReader struct {
	ids      []int64
	resolved []taxonomy.Keyword
	listErr  error
	resErr   error
}

func (f fakeKeywordsReader) ListByMovie(_ context.Context, _ domain.MovieID) ([]int64, error) {
	return f.ids, f.listErr
}

func (f fakeKeywordsReader) ListByIDsWithFallback(_ context.Context, _ []int64, _ string) ([]taxonomy.Keyword, error) {
	return f.resolved, f.resErr
}

func TestUseCase_Get_TaxonomySurfaced(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "Dune: Part Two"}
	// resolved returned id-ASC (2,7) — the usecase must re-project into join order (7,2).
	uc := New(
		fakeCanon{canon: canon},
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	).WithTaxonomy(
		fakeGenresReader{
			ids: []int64{7, 2},
			resolved: []taxonomy.Genre{
				{ID: 2, Name: "Adventure", Language: "en-US"},
				{ID: 7, Name: "Фантастика", Language: "ru-RU"},
			},
		},
		fakeKeywordsReader{
			ids: []int64{5},
			resolved: []taxonomy.Keyword{
				{ID: 5, Name: "dystopia", Language: "en-US"},
			},
		},
	)

	d, err := uc.Get(context.Background(), domain.TMDBID(693134), "ru-RU")
	require.NoError(t, err)

	require.Len(t, d.Genres, 2)
	assert.Equal(t, int64(7), d.Genres[0].ID, "join order preserved (7 before 2)")
	assert.Equal(t, "Фантастика", d.Genres[0].Name)
	assert.Equal(t, "ru-RU", d.Genres[0].Language)
	assert.Equal(t, int64(2), d.Genres[1].ID)
	assert.Equal(t, "en-US", d.Genres[1].Language, "fallback language surfaced")

	require.Len(t, d.Keywords, 1)
	assert.Equal(t, "dystopia", d.Keywords[0].Name)
}

func TestUseCase_Get_TaxonomyUnwiredEmpty(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "X"}
	uc := New(fakeCanon{canon: canon}, fakeI18n{err: ports.ErrNotFound}, fakeCollection{}, fakeMembership{})

	d, err := uc.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.NoError(t, err)
	assert.Empty(t, d.Genres, "no taxonomy readers wired → empty")
	assert.Empty(t, d.Keywords)
}

func TestUseCase_Get_TaxonomyReadErrorFailOpen(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "X"}
	// Both a join-read error and a resolve error must fail open (no 500, empty slices).
	uc := New(fakeCanon{canon: canon}, fakeI18n{err: ports.ErrNotFound}, fakeCollection{}, fakeMembership{}).
		WithTaxonomy(
			fakeGenresReader{listErr: errors.New("db down")},
			fakeKeywordsReader{ids: []int64{5}, resErr: errors.New("i18n down")},
		)

	d, err := uc.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.NoError(t, err, "taxonomy read errors never fail the detail")
	assert.Empty(t, d.Genres)
	assert.Empty(t, d.Keywords)
}

func TestUseCase_Get_TaxonomyEmptyLangDefaultsResolve(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "X"}
	// lang="" must still load taxonomy (ListByIDsWithFallback normalizes to en-US).
	uc := New(fakeCanon{canon: canon}, fakeI18n{}, fakeCollection{}, fakeMembership{}).
		WithTaxonomy(
			fakeGenresReader{ids: []int64{2}, resolved: []taxonomy.Genre{{ID: 2, Name: "Adventure", Language: "en-US"}}},
			fakeKeywordsReader{},
		)

	d, err := uc.Get(context.Background(), domain.TMDBID(1), "")
	require.NoError(t, err)
	require.Len(t, d.Genres, 1)
	assert.Equal(t, "Adventure", d.Genres[0].Name)
}
