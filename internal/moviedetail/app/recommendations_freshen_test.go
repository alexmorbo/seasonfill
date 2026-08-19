package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeRecsFreshener records the (canon, lang) EnsureFresh was called with.
type fakeRecsFreshener struct {
	calls    int
	lastLang string
	lastID   domain.MovieID
}

func (f *fakeRecsFreshener) EnsureFresh(_ context.Context, canon movie.Canon, lang string) FreshenResult {
	f.calls++
	f.lastLang = lang
	f.lastID = canon.ID
	return FreshenResult{Refreshed: true}
}

// minimal read fakes for the recs usecase.
type fakeRecsCanon struct{ c movie.Canon }

func (f fakeRecsCanon) GetByTMDBID(_ context.Context, _ domain.TMDBID) (movie.Canon, error) {
	return f.c, nil
}

type fakeRecsList struct{ ids []domain.MovieID }

func (f fakeRecsList) ListByMovie(_ context.Context, _ domain.MovieID) ([]domain.MovieID, error) {
	return f.ids, nil
}

type fakeRecsBatch struct{ rows []movie.Canon }

func (f fakeRecsBatch) ListByIDs(_ context.Context, _ []domain.MovieID) ([]movie.Canon, error) {
	return f.rows, nil
}

func recsUCUnderTest(fr freshenerPort) *RecommendationsUseCase {
	tid := domain.TMDBID(787)
	base := movie.Canon{ID: 7, TMDBID: &tid}
	rec := movie.Canon{ID: 9, TMDBID: ptrTMDB(1233575), Title: "Black Bag"}
	return NewRecommendationsUseCase(
		fakeRecsCanon{c: base},
		fakeRecsList{ids: []domain.MovieID{9}},
		fakeRecsBatch{rows: []movie.Canon{rec}},
		nil, // titles localizer unwired — freshen behavior is what we assert
	).WithFreshener(fr)
}

func ptrTMDB(v int) *domain.TMDBID { t := domain.TMDBID(v); return &t }

func TestRecommendations_SyncFreshen_DrivesOnLangGET(t *testing.T) {
	fr := &fakeRecsFreshener{}
	uc := recsUCUnderTest(fr)

	_, err := uc.Get(context.Background(), domain.TMDBID(787), "ru-RU", 20, 0)
	require.NoError(t, err)

	assert.Equal(t, 1, fr.calls, "recs GET drives the engine freshen synchronously (F-02a)")
	assert.Equal(t, "ru-RU", fr.lastLang)
	assert.Equal(t, domain.MovieID(7), fr.lastID, "freshen keyed on the BASE movie canon")
}

func TestRecommendations_SyncFreshen_SkippedForEmptyLang(t *testing.T) {
	fr := &fakeRecsFreshener{}
	uc := recsUCUnderTest(fr)

	_, err := uc.Get(context.Background(), domain.TMDBID(787), "", 20, 0)
	require.NoError(t, err)

	assert.Zero(t, fr.calls, "empty lang → no freshen (internal callers get canon EN, no TMDB work)")
}
