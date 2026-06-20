package seriesdetail

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// --- richer fakes for loadTopCast / loadTaxonomy / loadBestTrailer ---

type errSeriesPeople struct{ err error }

func (e *errSeriesPeople) ListBySeries(_ context.Context, _ domain.SeriesID, _ people.SeriesCreditKind) ([]people.SeriesCredit, error) {
	return nil, e.err
}

type errGenres struct{ err error }

func (e *errGenres) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return nil, e.err
}
func (e *errGenres) Get(_ context.Context, _ int64, _ string) (taxonomy.Genre, error) {
	return taxonomy.Genre{}, e.err
}

type errKeywords struct{ err error }

func (e *errKeywords) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return nil, e.err
}
func (e *errKeywords) Get(_ context.Context, _ int64, _ string) (taxonomy.Keyword, error) {
	return taxonomy.Keyword{}, e.err
}

type errNetworks struct {
	listErr error
	idsErr  error
	ids     []int64
}

func (e *errNetworks) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return e.ids, e.listErr
}
func (e *errNetworks) ListByIDs(_ context.Context, _ []int64) ([]taxonomy.Network, error) {
	return nil, e.idsErr
}

type errVideos struct {
	rows []database.VideoModel
	err  error
}

func (e *errVideos) ListBySeriesAndType(_ context.Context, _ domain.SeriesID, _ string) ([]database.VideoModel, error) {
	return e.rows, e.err
}

// --- loadTopCast ---

func TestComposer_loadTopCast_RepoError(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	deps.SeriesPeople = &errSeriesPeople{err: errors.New("db down")}
	c := NewComposer(deps)
	d := &Detail{SeriesID: 42}
	err := c.loadTopCast(context.Background(), d, 10)
	require.Error(t, err)
	assert.Nil(t, d.Cast)
}

func TestComposer_loadTopCast_NoCredits(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	// fakeSeriesPeople with nil rows.
	c := NewComposer(deps)
	d := &Detail{SeriesID: 42}
	err := c.loadTopCast(context.Background(), d, 10)
	require.NoError(t, err)
	assert.Nil(t, d.Cast)
}

func TestComposer_loadTopCast_TrimsToLimit(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	co0, co1, co2 := 0, 1, 2
	creds := []people.SeriesCredit{
		{PersonID: 1, CreditOrder: &co0}, {PersonID: 2, CreditOrder: &co1}, {PersonID: 3, CreditOrder: &co2},
	}
	deps.SeriesPeople = &fakeSeriesPeople{rows: creds}
	deps.People = &fakePeople{rows: []people.Person{
		{ID: 1, Name: "A"}, {ID: 2, Name: "B"}, {ID: 3, Name: "C"},
	}}
	c := NewComposer(deps)
	d := &Detail{SeriesID: 42}
	err := c.loadTopCast(context.Background(), d, 2)
	require.NoError(t, err)
	assert.Len(t, d.Cast, 2)
	assert.Equal(t, int64(1), d.Cast[0].Person.ID)
	assert.Equal(t, int64(2), d.Cast[1].Person.ID)
}

func TestComposer_loadTopCast_SkipsMissingPersonRows(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	creds := []people.SeriesCredit{
		{PersonID: 1}, {PersonID: 2}, {PersonID: 99}, // 99 missing
	}
	deps.SeriesPeople = &fakeSeriesPeople{rows: creds}
	deps.People = &fakePeople{rows: []people.Person{
		{ID: 1, Name: "A"}, {ID: 2, Name: "B"},
	}}
	c := NewComposer(deps)
	d := &Detail{SeriesID: 42}
	err := c.loadTopCast(context.Background(), d, 10)
	require.NoError(t, err)
	assert.Len(t, d.Cast, 2, "missing person row → cast credit skipped")
}

// --- loadTaxonomy ---

func TestComposer_loadTaxonomy_GenresErrorPropagates(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	deps.Genres = &errGenres{err: errors.New("genres list down")}
	c := NewComposer(deps)
	d := &Detail{SeriesID: 42}
	err := c.loadTaxonomy(context.Background(), d, "en-US")
	require.Error(t, err)
}

func TestComposer_loadTaxonomy_KeywordsErrorPropagates(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	deps.Keywords = &errKeywords{err: errors.New("keywords list down")}
	c := NewComposer(deps)
	d := &Detail{SeriesID: 42}
	err := c.loadTaxonomy(context.Background(), d, "en-US")
	require.Error(t, err)
}

func TestComposer_loadTaxonomy_NetworksErrorPropagates(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	deps.Networks = &errNetworks{listErr: errors.New("net list down")}
	c := NewComposer(deps)
	d := &Detail{SeriesID: 42}
	err := c.loadTaxonomy(context.Background(), d, "en-US")
	require.Error(t, err)
}

func TestComposer_loadTaxonomy_NetworksByIDsErrorPropagates(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	deps.Networks = &errNetworks{ids: []int64{1, 2}, idsErr: errors.New("net by-ids down")}
	c := NewComposer(deps)
	d := &Detail{SeriesID: 42}
	err := c.loadTaxonomy(context.Background(), d, "en-US")
	require.Error(t, err)
}

func TestComposer_loadTaxonomy_HappyPath_PopulatesAll(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	deps.Genres = &fakeGenres{ids: []int64{10, 11}}
	deps.Keywords = &fakeKeywords{ids: []int64{20}}
	deps.Networks = &fakeNetworks{ids: []int64{30}}
	c := NewComposer(deps)
	d := &Detail{SeriesID: 42}
	err := c.loadTaxonomy(context.Background(), d, "en-US")
	require.NoError(t, err)
	assert.Len(t, d.Genres, 2)
	assert.Len(t, d.Keywords, 1)
	assert.Len(t, d.Networks, 1)
}

// --- loadBestTrailer ---

func TestComposer_loadBestTrailer_RepoError(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	deps.Videos = &errVideos{err: errors.New("videos down")}
	c := NewComposer(deps)
	d := &Detail{SeriesID: 42}
	err := c.loadBestTrailer(context.Background(), d)
	require.Error(t, err)
}

func TestComposer_loadBestTrailer_PicksLatestOfficialYouTube(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	siteVimeo := "Vimeo"
	siteYT := "YouTube"
	deps.Videos = &fakeVideos{rows: []database.VideoModel{
		{Site: &siteVimeo, Official: true, Name: "A", PublishedAt: &t2},  // wrong site
		{Site: &siteYT, Official: false, Name: "B", PublishedAt: &t2},    // unofficial
		{Site: &siteYT, Official: true, Name: "C-old", PublishedAt: &t1}, // candidate
		{Site: &siteYT, Official: true, Name: "C-new", PublishedAt: &t2}, // best (latest)
		{Site: nil, Official: true, Name: "D", PublishedAt: &t2},         // no site
	}}
	c := NewComposer(deps)
	d := &Detail{SeriesID: 42}
	err := c.loadBestTrailer(context.Background(), d)
	require.NoError(t, err)
	require.NotNil(t, d.Trailer)
	assert.Equal(t, "C-new", d.Trailer.Name)
}

func TestComposer_loadBestTrailer_NoEligibleTrailers(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	siteVimeo := "Vimeo"
	deps.Videos = &fakeVideos{rows: []database.VideoModel{
		{Site: &siteVimeo, Official: true, Name: "A"},
	}}
	c := NewComposer(deps)
	d := &Detail{SeriesID: 42}
	err := c.loadBestTrailer(context.Background(), d)
	require.NoError(t, err)
	assert.Nil(t, d.Trailer)
}
