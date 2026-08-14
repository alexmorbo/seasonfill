package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeCompaniesReader struct {
	ids     []int64
	rows    []taxonomy.ProductionCompany
	listErr error
	dictErr error
}

func (f fakeCompaniesReader) ListByMovie(_ context.Context, _ domain.MovieID) ([]int64, error) {
	return f.ids, f.listErr
}

func (f fakeCompaniesReader) ListByIDs(_ context.Context, _ []int64) ([]taxonomy.ProductionCompany, error) {
	return f.rows, f.dictErr
}

type fakeTrailerReader struct {
	v   enrichpersistence.MovieVideo
	err error
}

func (f fakeTrailerReader) GetBestTrailer(_ context.Context, _ domain.MovieID) (enrichpersistence.MovieVideo, error) {
	return f.v, f.err
}

func TestUseCase_Get_SidebarSurfaced(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "Dune: Part Two"}
	// dict rows returned id-ASC (5,9) — the usecase must re-project into join order (9,5).
	uc := New(fakeCanon{canon: canon}, fakeI18n{err: ports.ErrNotFound}, fakeCollection{}, fakeMembership{}).
		WithSidebar(
			fakeCompaniesReader{
				ids: []int64{9, 5},
				rows: []taxonomy.ProductionCompany{
					{ID: 5, Name: "Warner Bros.", OriginCountry: new("US")},
					{ID: 9, Name: "Legendary Pictures", OriginCountry: new("US")},
				},
			},
			fakeTrailerReader{v: enrichpersistence.MovieVideo{
				Name: "Official Trailer", Site: new("YouTube"), Key: new("abc123"), Official: true,
			}},
		)

	d, err := uc.Get(context.Background(), domain.TMDBID(693134), "ru-RU")
	require.NoError(t, err)

	require.Len(t, d.Companies, 2)
	assert.Equal(t, int64(9), d.Companies[0].ID, "join order preserved (9 before 5)")
	assert.Equal(t, "Legendary Pictures", d.Companies[0].Name)
	require.NotNil(t, d.Trailer)
	require.NotNil(t, d.Trailer.Key)
	assert.Equal(t, "abc123", *d.Trailer.Key)
}

func TestUseCase_Get_SidebarUnwiredEmpty(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "X"}
	uc := New(fakeCanon{canon: canon}, fakeI18n{err: ports.ErrNotFound}, fakeCollection{}, fakeMembership{})

	d, err := uc.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.NoError(t, err)
	assert.Empty(t, d.Companies, "no sidebar readers wired → empty")
	assert.Nil(t, d.Trailer)
}

func TestUseCase_Get_TrailerNotFoundOmitted(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "X"}
	uc := New(fakeCanon{canon: canon}, fakeI18n{err: ports.ErrNotFound}, fakeCollection{}, fakeMembership{}).
		WithSidebar(
			fakeCompaniesReader{},
			fakeTrailerReader{err: ports.ErrNotFound},
		)

	d, err := uc.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.NoError(t, err, "a movie with no trailer is not an error")
	assert.Nil(t, d.Trailer, "ErrNotFound → trailer omitted")
	assert.Empty(t, d.Companies)
}

func TestUseCase_Get_SidebarReadErrorFailOpen(t *testing.T) {
	t.Parallel()
	canon := movie.Canon{ID: domain.MovieID(42), Title: "X"}
	// A join-read error, a dict-resolve error, and a non-NotFound trailer error must all
	// fail open (no 500, empty companies, nil trailer).
	uc := New(fakeCanon{canon: canon}, fakeI18n{err: ports.ErrNotFound}, fakeCollection{}, fakeMembership{}).
		WithSidebar(
			fakeCompaniesReader{listErr: errors.New("db down")},
			fakeTrailerReader{err: errors.New("db down")},
		)

	d, err := uc.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.NoError(t, err, "sidebar read errors never fail the detail")
	assert.Empty(t, d.Companies)
	assert.Nil(t, d.Trailer)

	// dict-resolve error alone (join ids OK) also fails open.
	uc2 := New(fakeCanon{canon: canon}, fakeI18n{err: ports.ErrNotFound}, fakeCollection{}, fakeMembership{}).
		WithSidebar(
			fakeCompaniesReader{ids: []int64{5}, dictErr: errors.New("dict down")},
			fakeTrailerReader{err: ports.ErrNotFound},
		)
	d2, err := uc2.Get(context.Background(), domain.TMDBID(1), "ru-RU")
	require.NoError(t, err)
	assert.Empty(t, d2.Companies)
}
