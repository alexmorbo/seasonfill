package repositories

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

func TestEpisodePeopleRepository_UpsertAndGet(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
	require.NoError(t, err)
	episodeIDRaw, err := NewEpisodesRepository(db).Upsert(ctx, series.CanonEpisode{
		SeriesID: seriesID, SeasonNumber: 1, EpisodeNumber: 1,
	})
	require.NoError(t, err)
	episodeID := domain.EpisodeID(episodeIDRaw)
	personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("John Turturro"))
	require.NoError(t, err)
	repo := NewEpisodePeopleRepository(db)

	id, err := repo.Upsert(ctx, people.EpisodeCredit{
		EpisodeID:     episodeID,
		PersonID:      personID,
		Kind:          people.EpisodeCreditGuestStar,
		TMDBCreditID:  "ep-credit-1",
		CharacterName: ptrString("Irving"),
	})
	require.NoError(t, err)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, episodeID, got.EpisodeID)
	assert.Equal(t, people.EpisodeCreditGuestStar, got.Kind)
}

func TestEpisodePeopleRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewEpisodePeopleRepository(db)
	_, err := repo.Get(context.Background(), 9999)
	assert.True(t, errors.Is(err, ports.ErrNotFound))

	var typedErr *sharedErrors.EpisodeNotFoundError
	require.True(t, errors.As(err, &typedErr))
}

func TestEpisodePeopleRepository_BatchUpsert_Idempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
	require.NoError(t, err)
	episodeIDRaw, err := NewEpisodesRepository(db).Upsert(ctx, series.CanonEpisode{
		SeriesID: seriesID, SeasonNumber: 1, EpisodeNumber: 1,
	})
	require.NoError(t, err)
	episodeID := domain.EpisodeID(episodeIDRaw)
	peopleRepo := NewPeopleRepository(db)
	repo := NewEpisodePeopleRepository(db)

	const n = 10
	credits := make([]people.EpisodeCredit, n)
	for i := 0; i < n; i++ {
		p := samplePerson(fmt.Sprintf("Guest %02d", i))
		p.TMDBID = ptrTMDBID(9000 + i)
		personID, err := peopleRepo.Upsert(ctx, p)
		require.NoError(t, err)
		credits[i] = people.EpisodeCredit{
			EpisodeID:    episodeID,
			PersonID:     personID,
			Kind:         people.EpisodeCreditGuestStar,
			TMDBCreditID: fmt.Sprintf("ep-credit-%02d", i),
			CreditOrder:  ptrInt(i),
		}
	}

	ids, err := repo.BatchUpsert(ctx, credits)
	require.NoError(t, err)
	require.Len(t, ids, n)

	ids2, err := repo.BatchUpsert(ctx, credits)
	require.NoError(t, err)
	require.Equal(t, ids, ids2)

	rows, err := repo.ListByEpisode(ctx, episodeID, "")
	require.NoError(t, err)
	assert.Len(t, rows, n)
}

func TestEpisodePeopleRepository_ListByEpisode_KindFilter(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Andor"))
	require.NoError(t, err)
	episodeIDRaw, err := NewEpisodesRepository(db).Upsert(ctx, series.CanonEpisode{
		SeriesID: seriesID, SeasonNumber: 1, EpisodeNumber: 1,
	})
	require.NoError(t, err)
	episodeID := domain.EpisodeID(episodeIDRaw)
	personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Director X"))
	require.NoError(t, err)
	repo := NewEpisodePeopleRepository(db)

	_, err = repo.Upsert(ctx, people.EpisodeCredit{
		EpisodeID: episodeID, PersonID: personID,
		Kind: people.EpisodeCreditGuestStar, TMDBCreditID: "g1",
	})
	require.NoError(t, err)
	_, err = repo.Upsert(ctx, people.EpisodeCredit{
		EpisodeID:    episodeID,
		PersonID:     personID,
		Kind:         people.EpisodeCreditCrew,
		TMDBCreditID: "c1",
		Department:   ptrString("Directing"),
		Job:          ptrString("Director"),
	})
	require.NoError(t, err)

	guests, err := repo.ListByEpisode(ctx, episodeID, people.EpisodeCreditGuestStar)
	require.NoError(t, err)
	assert.Len(t, guests, 1)

	crew, err := repo.ListByEpisode(ctx, episodeID, people.EpisodeCreditCrew)
	require.NoError(t, err)
	assert.Len(t, crew, 1)
}
