package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// D-0 quality bar: testcontainers Postgres + SQLite via testhelpers.AllBackends,
// success + miss + empty-input pairs, t.Parallel() at every level.

func seedMovie(t *testing.T, db *gorm.DB, id domain.MovieID, tmdb int, title string) {
	t.Helper()
	tm := domain.TMDBID(tmdb)
	require.NoError(t, db.Create(&database.MovieModel{
		ID:     id,
		TMDBID: &tm,
		Title:  title,
	}).Error)
}

func seedSeries(t *testing.T, db *gorm.DB, id domain.SeriesID, tvdb int, originalTitle string) {
	t.Helper()
	tv := domain.TVDBID(tvdb)
	ot := originalTitle
	require.NoError(t, db.Create(&database.SeriesModel{
		ID:              id,
		TVDBID:          &tv,
		OriginalTitle:   &ot,
		OriginCountries: datatypes.JSON([]byte("[]")),
	}).Error)
}

func seedSeriesText(t *testing.T, db *gorm.DB, seriesID domain.SeriesID, lang, title string) {
	t.Helper()
	tt := title
	require.NoError(t, db.Create(&database.SeriesTextModel{
		SeriesID: seriesID,
		Language: lang,
		Title:    &tt,
	}).Error)
}

func TestTitleReader_MovieTitlesByTMDB(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			reader := NewTitleReader(db)

			seedMovie(t, db, 1, 603, "The Matrix")
			seedMovie(t, db, 2, 27205, "Inception")

			// Success: both ids resolve; a missing id is absent from the map.
			got, err := reader.MovieTitlesByTMDB(ctx, []int64{603, 27205, 999999})
			require.NoError(t, err)
			assert.Equal(t, "The Matrix", got[603])
			assert.Equal(t, "Inception", got[27205])
			_, ok := got[999999]
			assert.False(t, ok, "unknown tmdb id must be absent (null title on the wire)")

			// Empty input short-circuits to an empty map, no query.
			empty, err := reader.MovieTitlesByTMDB(ctx, nil)
			require.NoError(t, err)
			assert.Empty(t, empty)
		})
	}
}

func TestTitleReader_SeriesTitlesByTVDB(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			reader := NewTitleReader(db)

			// Series 10: en-US text row wins over original_title.
			seedSeries(t, db, 10, 81189, "Breaking Bad (orig)")
			seedSeriesText(t, db, 10, "ru-RU", "Во все тяжкие")
			seedSeriesText(t, db, 10, "en-US", "Breaking Bad")

			// Series 20: no text rows → falls back to canon original_title.
			seedSeries(t, db, 20, 121361, "Game of Thrones")

			got, err := reader.SeriesTitlesByTVDB(ctx, []int64{81189, 121361, 424242})
			require.NoError(t, err)
			assert.Equal(t, "Breaking Bad", got[81189],
				"en-US series_texts title must win over original_title")
			assert.Equal(t, "Game of Thrones", got[121361],
				"series with no text rows must fall back to original_title")
			_, ok := got[424242]
			assert.False(t, ok, "unknown tvdb id must be absent")

			empty, err := reader.SeriesTitlesByTVDB(ctx, nil)
			require.NoError(t, err)
			assert.Empty(t, empty)
		})
	}
}
