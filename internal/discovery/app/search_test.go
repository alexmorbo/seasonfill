package app_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// fakeSearchRepo satisfies app.SearchRepo. TMDBFallback never calls it,
// but NewSearchUseCase requires a non-nil repo.
type fakeSearchRepo struct{}

func (fakeSearchRepo) LocalSearch(_ context.Context, _, _ string, _ int) ([]disco.Item, error) {
	return nil, nil
}

// fakeSearchTMDB satisfies app.SearchTMDB, returning a canned response.
type fakeSearchTMDB struct {
	resp *tmdb.TVListResponse
}

func (f *fakeSearchTMDB) SearchTV(_ context.Context, _, _ string, _ int) (*tmdb.TVListResponse, error) {
	return f.resp, nil
}

// fakeDispatch satisfies app.EnrichmentDispatcher; no-op sink.
type fakeDispatch struct{}

func (fakeDispatch) Enqueue(_ string, _ int64, _ string) {}

func newFallbackUC(tm app.SearchTMDB) *app.SearchUseCase {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return app.NewSearchUseCase(fakeSearchRepo{}, tm, newFakeStubs(), fakeDispatch{}, log)
}

// W18-2 — TMDBFallback must map each result's vote_average onto
// disco.Item.TMDBRating via nonZeroRating so search cards render the ★
// badge just like trending/popular. A positive vote_average surfaces as
// a non-nil pointer with the equal value; a 0 sentinel stays nil.
func TestTMDBFallback_MapsVoteAverageToTMDBRating_W18_2(t *testing.T) {
	tm := &fakeSearchTMDB{resp: &tmdb.TVListResponse{
		Page:       1,
		TotalPages: 1,
		Results: []tmdb.TVListEntry{
			{ID: 100, Name: "Rated Result", FirstAirDate: "2021-01-01", VoteAverage: 7.9},
			{ID: 200, Name: "Unrated Result", FirstAirDate: "2020-01-01", VoteAverage: 0},
		},
	}}
	uc := newFallbackUC(tm)

	items, err := uc.TMDBFallback(context.Background(), "q", "en-US")
	require.NoError(t, err)
	require.Len(t, items, 2)

	require.NotNil(t, items[0].TMDBRating,
		"positive vote_average must surface as a non-nil Item.TMDBRating")
	assert.InDelta(t, 7.9, *items[0].TMDBRating, 1e-9)

	assert.Nil(t, items[1].TMDBRating,
		"vote_average 0 sentinel must stay nil, not a 0.0 pointer")
}
