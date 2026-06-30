package seriesdetail

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Story 552 (E-1 Z3) — composer-level order/missing-id regression for the
// new batched loadTaxonomy. The repo returns rows in id-ASC order; the
// composer's contract is to iterate the original ids slice for input-order
// projection AND silently skip ids absent from the batch result (mirrors
// the prior `gerr == nil` skip behaviour of per-id Get).

// orderingGenres returns canon rows in REVERSE input order so a composer
// that naively forwards the slice would surface the wrong sequence.
type orderingGenres struct {
	ids []int64
}

func (o *orderingGenres) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return o.ids, nil
}
func (o *orderingGenres) Get(_ context.Context, id int64, lang string) (taxonomy.Genre, error) {
	return taxonomy.Genre{ID: id, Name: "g", Language: lang}, nil
}
func (o *orderingGenres) ListByIDsWithFallback(_ context.Context, ids []int64, lang string) ([]taxonomy.Genre, error) {
	out := make([]taxonomy.Genre, 0, len(ids))
	for _, id := range slices.Backward(ids) {
		out = append(out, taxonomy.Genre{ID: id, Name: "g", Language: lang})
	}
	return out, nil
}

// sparseGenres drops the id 20 from the batch result, simulating a missing
// row in `genres`. Composer must silently skip.
type sparseGenres struct{ ids []int64 }

func (s *sparseGenres) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return s.ids, nil
}
func (s *sparseGenres) Get(_ context.Context, id int64, lang string) (taxonomy.Genre, error) {
	return taxonomy.Genre{ID: id, Name: "g", Language: lang}, nil
}
func (s *sparseGenres) ListByIDsWithFallback(_ context.Context, ids []int64, lang string) ([]taxonomy.Genre, error) {
	out := make([]taxonomy.Genre, 0, len(ids))
	for _, id := range ids {
		if id == 20 {
			continue
		}
		out = append(out, taxonomy.Genre{ID: id, Name: "g", Language: lang})
	}
	return out, nil
}

// orderingKeywords / sparseKeywords mirror the genre fakes.
type orderingKeywords struct {
	ids []int64
}

func (o *orderingKeywords) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return o.ids, nil
}
func (o *orderingKeywords) Get(_ context.Context, id int64, lang string) (taxonomy.Keyword, error) {
	return taxonomy.Keyword{ID: id, Name: "k", Language: lang}, nil
}
func (o *orderingKeywords) ListByIDsWithFallback(_ context.Context, ids []int64, lang string) ([]taxonomy.Keyword, error) {
	out := make([]taxonomy.Keyword, 0, len(ids))
	for _, id := range slices.Backward(ids) {
		out = append(out, taxonomy.Keyword{ID: id, Name: "k", Language: lang})
	}
	return out, nil
}

type sparseKeywords struct{ ids []int64 }

func (s *sparseKeywords) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return s.ids, nil
}
func (s *sparseKeywords) Get(_ context.Context, id int64, lang string) (taxonomy.Keyword, error) {
	return taxonomy.Keyword{ID: id, Name: "k", Language: lang}, nil
}
func (s *sparseKeywords) ListByIDsWithFallback(_ context.Context, ids []int64, lang string) ([]taxonomy.Keyword, error) {
	out := make([]taxonomy.Keyword, 0, len(ids))
	for _, id := range ids {
		if id == 20 {
			continue
		}
		out = append(out, taxonomy.Keyword{ID: id, Name: "k", Language: lang})
	}
	return out, nil
}

// newTaxonomyComposer returns a *Composer wired with the minimum ports
// loadTaxonomy touches (Genres, Keywords, Networks, Companies). Callers
// override Genres / Keywords with the targeted fake.
func newTaxonomyComposer(t *testing.T) *Composer {
	t.Helper()
	return NewComposer(Deps{
		Genres:    &fakeGenres{},
		Keywords:  &fakeKeywords{},
		Networks:  &fakeNetworks{},
		Companies: fakeCompanies{},
		Logger:    newSilentLogger(),
	})
}

func TestLoadTaxonomy_GenresPreservesInputOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	c := newTaxonomyComposer(t)
	c.d.Genres = &orderingGenres{ids: []int64{30, 10, 20}}

	d := &Detail{SeriesID: 1}
	require.NoError(t, c.loadTaxonomy(ctx, d, "en-US"))

	require.Len(t, d.Genres, 3)
	assert.Equal(t, int64(30), d.Genres[0].ID)
	assert.Equal(t, int64(10), d.Genres[1].ID)
	assert.Equal(t, int64(20), d.Genres[2].ID)
}

func TestLoadTaxonomy_GenresMissingIDDroppedSilently(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	c := newTaxonomyComposer(t)
	c.d.Genres = &sparseGenres{ids: []int64{10, 20, 30}}

	d := &Detail{SeriesID: 1}
	require.NoError(t, c.loadTaxonomy(ctx, d, "en-US"))

	require.Len(t, d.Genres, 2)
	assert.Equal(t, int64(10), d.Genres[0].ID)
	assert.Equal(t, int64(30), d.Genres[1].ID)
}

func TestLoadTaxonomy_KeywordsPreservesInputOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	c := newTaxonomyComposer(t)
	c.d.Keywords = &orderingKeywords{ids: []int64{30, 10, 20}}

	d := &Detail{SeriesID: 1}
	require.NoError(t, c.loadTaxonomy(ctx, d, "en-US"))

	require.Len(t, d.Keywords, 3)
	assert.Equal(t, int64(30), d.Keywords[0].ID)
	assert.Equal(t, int64(10), d.Keywords[1].ID)
	assert.Equal(t, int64(20), d.Keywords[2].ID)
}

func TestLoadTaxonomy_KeywordsMissingIDDroppedSilently(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	c := newTaxonomyComposer(t)
	c.d.Keywords = &sparseKeywords{ids: []int64{10, 20, 30}}

	d := &Detail{SeriesID: 1}
	require.NoError(t, c.loadTaxonomy(ctx, d, "en-US"))

	require.Len(t, d.Keywords, 2)
	assert.Equal(t, int64(10), d.Keywords[0].ID)
	assert.Equal(t, int64(30), d.Keywords[1].ID)
}
