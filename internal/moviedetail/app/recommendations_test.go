package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeMovieRecs is a stub MovieRecsReader.
type fakeMovieRecs struct {
	ids []domain.MovieID
	err error
}

func (f *fakeMovieRecs) ListByMovie(_ context.Context, _ domain.MovieID) ([]domain.MovieID, error) {
	return f.ids, f.err
}

// fakeMovieBatch is a stub MovieCanonBatchReader. It returns its canons verbatim
// (deliberately NOT in the requested id order, to prove the usecase reorders).
type fakeMovieBatch struct {
	canons []movie.Canon
	err    error
	gotIDs []domain.MovieID
}

func (f *fakeMovieBatch) ListByIDs(_ context.Context, ids []domain.MovieID) ([]movie.Canon, error) {
	f.gotIDs = ids
	return f.canons, f.err
}

// fakeRecTitleLocalizer is a stub MovieRecTitleLocalizer. titles maps tmdb_id →
// localized title; err (when set) forces the batch-read failure path.
type fakeRecTitleLocalizer struct {
	titles  map[int]string
	err     error
	gotIDs  []int
	gotLang string
}

func (f *fakeRecTitleLocalizer) ListTitlesByTMDBIDsWithFallback(_ context.Context, tmdbIDs []int, lang string) (map[int]string, error) {
	f.gotIDs = tmdbIDs
	f.gotLang = lang
	return f.titles, f.err
}

func tmdbPtr(v int) *domain.TMDBID { p := domain.TMDBID(v); return &p }

func TestRecommendationsUseCase_OrderedStubSkipAndTMDBID(t *testing.T) {
	base := movie.Canon{ID: 1, TMDBID: tmdbPtr(603), Title: "The Matrix"}
	// recs rank order: 40, 30, 99. 99 never materialised (unresolved → skip).
	recs := &fakeMovieRecs{ids: []domain.MovieID{40, 30, 99}}
	poster40, poster30 := "/p40.jpg", "/p30.jpg"
	// Batch returns id-ASC (30 then 40) — usecase must reorder to 40,30.
	movies := &fakeMovieBatch{canons: []movie.Canon{
		{ID: 30, TMDBID: tmdbPtr(605), Title: "The Matrix Revolutions", PosterAsset: &poster30},
		{ID: 40, TMDBID: tmdbPtr(604), Title: "The Matrix Reloaded", PosterAsset: &poster40},
	}}
	uc := mdapp.NewRecommendationsUseCase(&fakeMovieCanon{canon: base}, recs, movies, nil)

	page, err := uc.Get(context.Background(), domain.TMDBID(603), "", 20, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 2, "unresolved stub id=99 must be skipped")
	assert.Equal(t, 2, page.TotalCount)
	assert.False(t, page.HasMore)
	assert.Equal(t, domain.MovieID(1), page.MovieID)

	// Order preserved from recs rank (40, 30), NOT batch order (30, 40).
	require.NotNil(t, page.Items[0].Canon.TMDBID)
	assert.Equal(t, domain.TMDBID(604), *page.Items[0].Canon.TMDBID)
	assert.Equal(t, "The Matrix Reloaded", page.Items[0].Title)
	require.NotNil(t, page.Items[0].Canon.PosterAsset)
	assert.Equal(t, "/p40.jpg", *page.Items[0].Canon.PosterAsset)
	require.NotNil(t, page.Items[1].Canon.TMDBID)
	assert.Equal(t, domain.TMDBID(605), *page.Items[1].Canon.TMDBID)

	// Batch was asked for exactly the rank-ordered ids.
	assert.Equal(t, []domain.MovieID{40, 30, 99}, movies.gotIDs)
}

func TestRecommendationsUseCase_SkipsNilTMDBID(t *testing.T) {
	base := movie.Canon{ID: 1, TMDBID: tmdbPtr(603), Title: "Base"}
	recs := &fakeMovieRecs{ids: []domain.MovieID{50, 60}}
	movies := &fakeMovieBatch{canons: []movie.Canon{
		{ID: 50, TMDBID: nil, Title: "Orphan (no tmdb)"}, // unlinkable → skip
		{ID: 60, TMDBID: tmdbPtr(700), Title: "Linkable"},
	}}
	uc := mdapp.NewRecommendationsUseCase(&fakeMovieCanon{canon: base}, recs, movies, nil)

	page, err := uc.Get(context.Background(), domain.TMDBID(603), "", 20, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, domain.TMDBID(700), *page.Items[0].Canon.TMDBID)
}

func TestRecommendationsUseCase_Pagination(t *testing.T) {
	base := movie.Canon{ID: 1, TMDBID: tmdbPtr(603)}
	ids := []domain.MovieID{10, 20, 30}
	canons := []movie.Canon{
		{ID: 10, TMDBID: tmdbPtr(100)},
		{ID: 20, TMDBID: tmdbPtr(200)},
		{ID: 30, TMDBID: tmdbPtr(300)},
	}
	uc := mdapp.NewRecommendationsUseCase(
		&fakeMovieCanon{canon: base},
		&fakeMovieRecs{ids: ids},
		&fakeMovieBatch{canons: canons},
		nil,
	)

	page, err := uc.Get(context.Background(), domain.TMDBID(603), "", 2, 0)
	require.NoError(t, err)
	assert.Len(t, page.Items, 2)
	assert.Equal(t, 3, page.TotalCount)
	assert.True(t, page.HasMore)

	page2, err := uc.Get(context.Background(), domain.TMDBID(603), "", 2, 2)
	require.NoError(t, err)
	assert.Len(t, page2.Items, 1)
	assert.False(t, page2.HasMore)

	// offset past the end → empty, not error.
	page3, err := uc.Get(context.Background(), domain.TMDBID(603), "", 2, 99)
	require.NoError(t, err)
	assert.Empty(t, page3.Items)
	assert.False(t, page3.HasMore)
}

func TestRecommendationsUseCase_NotFoundBubbles(t *testing.T) {
	uc := mdapp.NewRecommendationsUseCase(
		&fakeMovieCanon{err: ports.ErrNotFound},
		&fakeMovieRecs{},
		&fakeMovieBatch{},
		nil,
	)
	_, err := uc.Get(context.Background(), domain.TMDBID(1), "", 20, 0)
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestRecommendationsUseCase_RecsListErrorDegrades(t *testing.T) {
	base := movie.Canon{ID: 1, TMDBID: tmdbPtr(603)}
	uc := mdapp.NewRecommendationsUseCase(
		&fakeMovieCanon{canon: base},
		&fakeMovieRecs{err: errors.New("boom")},
		&fakeMovieBatch{},
		nil,
	)
	page, err := uc.Get(context.Background(), domain.TMDBID(603), "", 20, 0)
	require.NoError(t, err, "recs list failure must degrade, not fail")
	assert.Empty(t, page.Items)
	assert.Contains(t, page.Degraded, "tmdb_movie")
}

func TestRecommendationsUseCase_LocalizesTitleByLang(t *testing.T) {
	base := movie.Canon{ID: 1, TMDBID: tmdbPtr(603), Title: "The Matrix"}
	recs := &fakeMovieRecs{ids: []domain.MovieID{40, 30}}
	movies := &fakeMovieBatch{canons: []movie.Canon{
		{ID: 40, TMDBID: tmdbPtr(604), Title: "The Matrix Reloaded"},
		{ID: 30, TMDBID: tmdbPtr(605), Title: "The Matrix Revolutions"},
	}}
	// tmdb 604 has a ru title; 605 does NOT → falls back to canon EN.
	loc := &fakeRecTitleLocalizer{titles: map[int]string{604: "Матрица: Перезагрузка"}}
	uc := mdapp.NewRecommendationsUseCase(&fakeMovieCanon{canon: base}, recs, movies, loc)

	page, err := uc.Get(context.Background(), domain.TMDBID(603), "ru-RU", 20, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	// Rank order preserved (604 then 605).
	assert.Equal(t, "Матрица: Перезагрузка", page.Items[0].Title, "604 localized to ru")
	assert.Equal(t, "The Matrix Revolutions", page.Items[1].Title, "605 has no ru row → canon EN fallback")
	// Localizer was asked for the resolved tmdb ids + the requested lang.
	assert.ElementsMatch(t, []int{604, 605}, loc.gotIDs)
	assert.Equal(t, "ru-RU", loc.gotLang)
	assert.Empty(t, page.Degraded, "localization must not add a degraded tag")
}

func TestRecommendationsUseCase_EmptyLangKeepsCanonTitle(t *testing.T) {
	base := movie.Canon{ID: 1, TMDBID: tmdbPtr(603), Title: "The Matrix"}
	recs := &fakeMovieRecs{ids: []domain.MovieID{40}}
	movies := &fakeMovieBatch{canons: []movie.Canon{
		{ID: 40, TMDBID: tmdbPtr(604), Title: "The Matrix Reloaded"},
	}}
	// Localizer would return a ru title, but empty lang must SKIP it entirely.
	loc := &fakeRecTitleLocalizer{titles: map[int]string{604: "Матрица: Перезагрузка"}}
	uc := mdapp.NewRecommendationsUseCase(&fakeMovieCanon{canon: base}, recs, movies, loc)

	page, err := uc.Get(context.Background(), domain.TMDBID(603), "", 20, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "The Matrix Reloaded", page.Items[0].Title, "empty lang → canon EN")
	assert.Nil(t, loc.gotIDs, "localizer must not be called on empty lang")
}

func TestRecommendationsUseCase_LocalizerErrorDegradesToCanon(t *testing.T) {
	base := movie.Canon{ID: 1, TMDBID: tmdbPtr(603), Title: "The Matrix"}
	recs := &fakeMovieRecs{ids: []domain.MovieID{40}}
	movies := &fakeMovieBatch{canons: []movie.Canon{
		{ID: 40, TMDBID: tmdbPtr(604), Title: "The Matrix Reloaded"},
	}}
	loc := &fakeRecTitleLocalizer{err: errors.New("boom")}
	uc := mdapp.NewRecommendationsUseCase(&fakeMovieCanon{canon: base}, recs, movies, loc)

	page, err := uc.Get(context.Background(), domain.TMDBID(603), "ru-RU", 20, 0)
	require.NoError(t, err, "an i18n read failure must NOT 500")
	require.Len(t, page.Items, 1)
	assert.Equal(t, "The Matrix Reloaded", page.Items[0].Title, "read failure → canon EN fallback")
	assert.Empty(t, page.Degraded, "read failure adds NO degraded tag (silent, mirrors canon-batch-fail)")
}
