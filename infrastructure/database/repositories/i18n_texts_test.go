package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// TestSeriesTextsRepository_FallbackThreeScenarios covers the §5.6
// pattern: requested-language hit, en-US fallback, first-available
// when neither requested nor en-US exists.
func TestSeriesTextsRepository_FallbackThreeScenarios(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
	require.NoError(t, err)
	repo := NewSeriesTextsRepository(db)

	cases := []struct {
		name      string
		seed      []series.SeriesText
		requested string
		wantLang  string
		wantTitle string
	}{
		{
			name: "requested language present",
			seed: []series.SeriesText{
				{SeriesID: seriesID, Language: "ru-RU", Title: ptrString("Фонд")},
				{SeriesID: seriesID, Language: "en-US", Title: ptrString("Foundation")},
			},
			requested: "ru-RU",
			wantLang:  "ru-RU",
			wantTitle: "Фонд",
		},
		{
			name: "requested missing, en-US fallback",
			seed: []series.SeriesText{
				{SeriesID: seriesID, Language: "en-US", Title: ptrString("Foundation")},
			},
			requested: "ru-RU",
			wantLang:  "en-US",
			wantTitle: "Foundation",
		},
		{
			name: "requested and en-US missing, first available wins",
			seed: []series.SeriesText{
				{SeriesID: seriesID, Language: "fr-FR", Title: ptrString("Fondation")},
				{SeriesID: seriesID, Language: "de-DE", Title: ptrString("Stiftung")},
			},
			requested: "ru-RU",
			wantLang:  "de-DE", // language ASC tiebreaker — 'd' < 'f'
			wantTitle: "Stiftung",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Fresh DB per scenario keeps the test deterministic without
			// having to scrub rows between cases.
			db := setupTestDB(t)
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
			require.NoError(t, err)
			repo := NewSeriesTextsRepository(db)
			for _, row := range tc.seed {
				row.SeriesID = seriesID
				require.NoError(t, repo.Upsert(ctx, row))
			}
			got, err := repo.GetWithFallback(ctx, seriesID, tc.requested)
			require.NoError(t, err)
			assert.Equal(t, tc.wantLang, got.Language)
			require.NotNil(t, got.Title)
			assert.Equal(t, tc.wantTitle, *got.Title)
		})
	}

	_ = repo // silence the outer-scope binding when fresh DB takes over
}

// TestSeriesTextsRepository_Fallback_NoRows confirms the helper returns
// ErrNotFound when the entity has no text rows at all.
func TestSeriesTextsRepository_Fallback_NoRows(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
	require.NoError(t, err)
	repo := NewSeriesTextsRepository(db)
	_, err = repo.GetWithFallback(ctx, seriesID, "ru-RU")
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

// TestEpisodeTextsRepository_FallbackSmoke is a smaller smoke that
// confirms the same helper wired to episode_texts gives en-US fallback.
func TestEpisodeTextsRepository_FallbackSmoke(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
	require.NoError(t, err)
	epIDRaw, err := NewEpisodesRepository(db).Upsert(ctx, series.CanonEpisode{
		SeriesID: seriesID, SeasonNumber: 1, EpisodeNumber: 1,
	})
	require.NoError(t, err)
	epID := domain.EpisodeID(epIDRaw)
	repo := NewEpisodeTextsRepository(db)
	require.NoError(t, repo.Upsert(ctx, series.EpisodeText{
		EpisodeID: epID, Language: "en-US", Title: ptrString("Good News About Hell"),
	}))
	got, err := repo.GetWithFallback(ctx, epID, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "en-US", got.Language)
}
