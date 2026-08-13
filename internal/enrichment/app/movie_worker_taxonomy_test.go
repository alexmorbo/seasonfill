package enrichment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type i18nSeed struct {
	id   int64
	lang string
	name string
}

type fakeGenresWriter struct {
	next     int64
	upserted []taxonomy.Genre
	i18n     []i18nSeed
	setMovie domain.MovieID
	setIDs   []int64
	setCalls int
}

func (f *fakeGenresWriter) Upsert(_ context.Context, g taxonomy.Genre) (int64, error) {
	f.next++
	f.upserted = append(f.upserted, g)
	return f.next, nil
}
func (f *fakeGenresWriter) UpsertI18n(_ context.Context, id int64, lang, name string) error {
	f.i18n = append(f.i18n, i18nSeed{id, lang, name})
	return nil
}
func (f *fakeGenresWriter) SetMovie(_ context.Context, movieID domain.MovieID, ids []int64) error {
	f.setCalls++
	f.setMovie = movieID
	f.setIDs = ids
	return nil
}

type fakeKeywordsWriter struct {
	next     int64
	i18n     []i18nSeed
	setIDs   []int64
	setCalls int
}

func (f *fakeKeywordsWriter) Upsert(_ context.Context, _ taxonomy.Keyword) (int64, error) {
	f.next++
	return f.next, nil
}
func (f *fakeKeywordsWriter) UpsertI18n(_ context.Context, id int64, lang, name string) error {
	f.i18n = append(f.i18n, i18nSeed{id, lang, name})
	return nil
}
func (f *fakeKeywordsWriter) SetMovie(_ context.Context, _ domain.MovieID, ids []int64) error {
	f.setCalls++
	f.setIDs = ids
	return nil
}

type fakeCompaniesWriter struct {
	next     int64
	upserted []taxonomy.ProductionCompany
	setIDs   []int64
	setCalls int
}

func (f *fakeCompaniesWriter) Upsert(_ context.Context, c taxonomy.ProductionCompany) (int64, error) {
	f.next++
	f.upserted = append(f.upserted, c)
	return f.next, nil
}
func (f *fakeCompaniesWriter) SetMovie(_ context.Context, _ domain.MovieID, ids []int64) error {
	f.setCalls++
	f.setIDs = ids
	return nil
}

func taxonomyResp() *tmdb.MovieResponse {
	return &tmdb.MovieResponse{
		ID:    693134,
		Title: "Dune: Part Two",
		Genres: []tmdb.TVGenre{
			{ID: 878, Name: "Science Fiction"},
			{ID: 12, Name: "Adventure"},
		},
		Keywords: &tmdb.MovieKeywords{Keywords: []tmdb.TVKeyword{
			{ID: 4565, Name: "dystopia"},
			{ID: 9951, Name: "alien"},
		}},
		ProductionCompanies: []tmdb.TVCompany{
			{ID: 923, Name: "Legendary Pictures", LogoPath: "/l.png", OriginCountry: "US"},
		},
	}
}

// TestMovieWorker_HandleForced_WritesTaxonomyTrio proves all three writers persist their
// join sets, seed base-lang i18n for genres+keywords, run inside the Transactor, and that
// ONLY keywords stamps its section clock (genres + companies unstamped).
func TestMovieWorker_HandleForced_WritesTaxonomyTrio(t *testing.T) {
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 693134)}
	genres := &fakeGenresWriter{}
	keywords := &fakeKeywordsWriter{}
	companies := &fakeCompaniesWriter{}
	tx := &passthroughTx{}

	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:      &fakeMovieTMDB{resp: taxonomyResp()},
		Movies:    canon,
		Genres:    genres,
		Keywords:  keywords,
		Companies: companies,
		Tx:        tx,
		Clock:     func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)
	require.NoError(t, w.HandleForced(context.Background(), 7))

	// three writer txns ran (plus zero others in this fixture).
	assert.Equal(t, 3, tx.calls)

	// genres: two dict seeds, two base-lang i18n rows, join set = [1,2].
	assert.Len(t, genres.upserted, 2)
	require.Len(t, genres.i18n, 2)
	assert.Equal(t, tmdb.DefaultLanguage, genres.i18n[0].lang)
	assert.Equal(t, domain.MovieID(7), genres.setMovie)
	assert.Equal(t, []int64{1, 2}, genres.setIDs)

	// keywords: base-lang i18n present, join set written, section stamped ONCE.
	require.Len(t, keywords.i18n, 2)
	assert.Equal(t, tmdb.DefaultLanguage, keywords.i18n[0].lang)
	assert.Equal(t, []int64{1, 2}, keywords.setIDs)
	assert.Equal(t, 1, canon.keywordsMarkCalls, "enrichment_keywords_synced_at stamped once")
	assert.Equal(t, domain.MovieID(7), canon.keywordsMarkedID)

	// companies: dict seed + join set, NO i18n, NO stamp column exists.
	assert.Len(t, companies.upserted, 1)
	assert.Equal(t, []int64{1}, companies.setIDs)
}

// TestMovieWorker_HandleForced_NoTaxonomyDepsSkips proves the pre-Ф1.1b path: with the trio
// deps nil the worker hydrates canon and touches no taxonomy writer / no keyword stamp.
func TestMovieWorker_HandleForced_NoTaxonomyDepsSkips(t *testing.T) {
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 693134)}
	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:   &fakeMovieTMDB{resp: taxonomyResp()},
		Movies: canon,
		Clock:  func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)
	require.NoError(t, w.HandleForced(context.Background(), 7))
	assert.Equal(t, 0, canon.keywordsMarkCalls, "no keyword stamp without taxonomy deps")
}

// TestMovieWorker_HandleForced_NilKeywordsSubResourceSkipsKeywords proves the keyword writer
// is additionally gated on the decoded sub-resource: genres/companies still run, keywords
// does not (no stamp).
func TestMovieWorker_HandleForced_NilKeywordsSubResourceSkipsKeywords(t *testing.T) {
	resp := taxonomyResp()
	resp.Keywords = nil // append token absent / movie has no keywords sub-resource
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 693134)}
	genres := &fakeGenresWriter{}
	keywords := &fakeKeywordsWriter{}
	companies := &fakeCompaniesWriter{}

	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:      &fakeMovieTMDB{resp: resp},
		Movies:    canon,
		Genres:    genres,
		Keywords:  keywords,
		Companies: companies,
		Tx:        &passthroughTx{},
		Clock:     func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	require.NoError(t, err)
	require.NoError(t, w.HandleForced(context.Background(), 7))

	assert.Equal(t, 1, genres.setCalls)
	assert.Equal(t, 1, companies.setCalls)
	assert.Equal(t, 0, keywords.setCalls, "keyword writer skipped when sub-resource nil")
	assert.Equal(t, 0, canon.keywordsMarkCalls)
}
