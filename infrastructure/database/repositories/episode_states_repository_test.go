package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/series"
)

func TestEpisodeStatesRepository_UpsertAndGet(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Andor"))
	require.NoError(t, err)
	epID, err := NewEpisodesRepository(db).Upsert(ctx, series.CanonEpisode{
		SeriesID: seriesID, SeasonNumber: 1, EpisodeNumber: 1,
	})
	require.NoError(t, err)
	repo := NewEpisodeStatesRepository(db)

	q := "WEBDL-1080p"
	sz := int64(123456789)
	require.NoError(t, repo.Upsert(ctx, series.EpisodeState{
		InstanceName: "main",
		EpisodeID:    epID,
		Monitored:    true,
		HasFile:      true,
		Quality:      &q,
		SizeBytes:    &sz,
	}))

	got, err := repo.Get(ctx, "main", epID)
	require.NoError(t, err)
	assert.True(t, got.HasFile)
	assert.True(t, got.Monitored)
	require.NotNil(t, got.Quality)
	assert.Equal(t, "WEBDL-1080p", *got.Quality)
	assert.Equal(t, int64(123456789), *got.SizeBytes)
}

func TestEpisodeStatesRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewEpisodeStatesRepository(db)
	_, err := repo.Get(context.Background(), "main", 9999)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestEpisodeStatesRepository_Upsert_Idempotent_PerInstance(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
	require.NoError(t, err)
	epID, err := NewEpisodesRepository(db).Upsert(ctx, series.CanonEpisode{
		SeriesID: seriesID, SeasonNumber: 1, EpisodeNumber: 1,
	})
	require.NoError(t, err)
	repo := NewEpisodeStatesRepository(db)

	st := series.EpisodeState{InstanceName: "main", EpisodeID: epID, Monitored: true}
	require.NoError(t, repo.Upsert(ctx, st))
	require.NoError(t, repo.Upsert(ctx, st)) // idempotent

	// Same episode, different instance — independent row by PK shape.
	require.NoError(t, repo.Upsert(ctx, series.EpisodeState{
		InstanceName: "main-4k", EpisodeID: epID, Monitored: false, HasFile: true,
	}))

	got1, err := repo.Get(ctx, "main", epID)
	require.NoError(t, err)
	assert.True(t, got1.Monitored)
	got2, err := repo.Get(ctx, "main-4k", epID)
	require.NoError(t, err)
	assert.True(t, got2.HasFile)
}

func TestEpisodeStatesRepository_ListBySeries(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
	require.NoError(t, err)
	repoEp := NewEpisodesRepository(db)
	repoSt := NewEpisodeStatesRepository(db)
	for i := 1; i <= 3; i++ {
		epID, err := repoEp.Upsert(ctx, series.CanonEpisode{
			SeriesID: seriesID, SeasonNumber: 1, EpisodeNumber: i,
		})
		require.NoError(t, err)
		require.NoError(t, repoSt.Upsert(ctx, series.EpisodeState{
			InstanceName: "main", EpisodeID: epID, HasFile: i == 1,
		}))
	}
	rows, err := repoSt.ListBySeries(ctx, "main", seriesID)
	require.NoError(t, err)
	require.Len(t, rows, 3)
}

func ptrInt64(v int64) *int64 { return &v }

func TestEpisodeStatesRepository_MediaMeta_RoundTrip(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("MediaMeta"))
	require.NoError(t, err)
	epID, err := NewEpisodesRepository(db).Upsert(ctx, series.CanonEpisode{
		SeriesID: seriesID, SeasonNumber: 5, EpisodeNumber: 1,
	})
	require.NoError(t, err)
	repo := NewEpisodeStatesRepository(db)

	vc := "HEVC"
	ac := "DDP"
	ach := "5.1"
	rg := "RARBG"
	st := series.EpisodeState{
		InstanceName:  "main",
		EpisodeID:     epID,
		Monitored:     true,
		HasFile:       true,
		VideoCodec:    &vc,
		AudioCodec:    &ac,
		AudioChannels: &ach,
		ReleaseGroup:  &rg,
	}
	require.NoError(t, repo.Upsert(ctx, st))

	got, err := repo.Get(ctx, "main", epID)
	require.NoError(t, err)
	require.Equal(t, &vc, got.VideoCodec)
	require.Equal(t, &ac, got.AudioCodec)
	require.Equal(t, &ach, got.AudioChannels)
	require.Equal(t, &rg, got.ReleaseGroup)
}
