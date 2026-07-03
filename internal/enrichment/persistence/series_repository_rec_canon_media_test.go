package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedRecChildCanon inserts a rec-child canon row via UpsertStub (matches the
// production A3b write path pre-Story 571) and seeds its legacy
// poster_asset/backdrop_asset DB columns directly. S-E3a removed those fields
// from series.Canon (the mappers stopped copying them), so Upsert no longer
// writes the columns — the raw seed simulates the post-Sonarr-scan state that
// UpdateRecCanonMedia's narrow UPDATE later overwrites. Reuses seedRecCanonMedia
// / readRecCanonMedia from a3b_rec_canon_media_integration_test.go.
func seedRecChildCanon(t *testing.T, gdb *gorm.DB, repo *SeriesRepository, title string, tmdbID domain.TMDBID, poster, backdrop *string) domain.SeriesID {
	t.Helper()
	canon := series.Canon{
		OriginalTitle: new(title),
		TMDBID:        &tmdbID,
		Hydration:     series.HydrationStub,
	}
	id, err := repo.UpsertStub(context.Background(), canon)
	require.NoError(t, err)
	require.NotZero(t, id)
	p, b := "", ""
	if poster != nil {
		p = *poster
	}
	if backdrop != nil {
		b = *backdrop
	}
	seedRecCanonMedia(t, gdb, id, p, b)
	return id
}

// TestUpdateRecCanonMedia_OverwritesExistingPoster — Story 571 B-54 root-
// cause fix: existing poster_asset (typically en-US locked in by UpsertStub
// COALESCE) MUST be overwritten by the new lang-preferred path. Backdrop
// unchanged because caller passed empty string.
func TestUpdateRecCanonMedia_OverwritesExistingPoster(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			repo := NewSeriesRepository(gdb)
			ctx := context.Background()

			enPoster := "/en.jpg"
			enBackdrop := "/en_bd.jpg"
			id := seedRecChildCanon(t, gdb, repo, "Rec Child",
				domain.TMDBID(1001),
				&enPoster,
				&enBackdrop,
			)

			// UPDATE poster only (backdrop empty). Expect: poster changes,
			// backdrop untouched.
			require.NoError(t, repo.UpdateRecCanonMedia(ctx, id, "/ru.jpg", ""))

			gotPoster, gotBackdrop := readRecCanonMedia(t, gdb, id)
			require.NotNil(t, gotPoster)
			assert.Equal(t, "/ru.jpg", *gotPoster,
				"poster_asset MUST be overwritten to ru-RU path (this is the B-54 root fix)")
			require.NotNil(t, gotBackdrop)
			assert.Equal(t, "/en_bd.jpg", *gotBackdrop,
				"backdrop_asset MUST be untouched when backdropPath empty")
		})
	}
}

// TestUpdateRecCanonMedia_BackdropOnly — symmetric to poster-only case.
func TestUpdateRecCanonMedia_BackdropOnly(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			repo := NewSeriesRepository(gdb)
			ctx := context.Background()

			enPoster := "/en.jpg"
			enBackdrop := "/en_bd.jpg"
			id := seedRecChildCanon(t, gdb, repo, "Rec Child",
				domain.TMDBID(1002),
				&enPoster,
				&enBackdrop,
			)

			require.NoError(t, repo.UpdateRecCanonMedia(ctx, id, "", "/ru_bd.jpg"))

			gotPoster, gotBackdrop := readRecCanonMedia(t, gdb, id)
			require.NotNil(t, gotPoster)
			assert.Equal(t, "/en.jpg", *gotPoster,
				"poster_asset MUST be untouched when posterPath empty")
			require.NotNil(t, gotBackdrop)
			assert.Equal(t, "/ru_bd.jpg", *gotBackdrop,
				"backdrop_asset MUST be overwritten")
		})
	}
}

// TestUpdateRecCanonMedia_BothEmpty_NoOp — early-return branch: no SQL
// emitted, row unchanged. Guards against writing NULL and triggering
// Story 319 image-null cold-start recovery loop.
func TestUpdateRecCanonMedia_BothEmpty_NoOp(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			repo := NewSeriesRepository(gdb)
			ctx := context.Background()

			enPoster := "/en.jpg"
			enBackdrop := "/en_bd.jpg"
			id := seedRecChildCanon(t, gdb, repo, "Rec Child",
				domain.TMDBID(1003),
				&enPoster,
				&enBackdrop,
			)
			// Capture updated_at BEFORE the no-op.
			before, err := repo.Get(ctx, id)
			require.NoError(t, err)
			priorUpdatedAt := before.UpdatedAt

			// Sleep a tick so a UPDATE (if it happened) would leave an
			// observable updated_at delta.
			time.Sleep(15 * time.Millisecond)

			require.NoError(t, repo.UpdateRecCanonMedia(ctx, id, "", ""))

			gotPoster, gotBackdrop := readRecCanonMedia(t, gdb, id)
			require.NotNil(t, gotPoster)
			assert.Equal(t, "/en.jpg", *gotPoster,
				"poster_asset MUST be unchanged on both-empty no-op")
			require.NotNil(t, gotBackdrop)
			assert.Equal(t, "/en_bd.jpg", *gotBackdrop,
				"backdrop_asset MUST be unchanged on both-empty no-op")
			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, priorUpdatedAt.Unix(), got.UpdatedAt.Unix(),
				"updated_at MUST NOT change on both-empty no-op (no SQL emitted)")
		})
	}
}

// TestUpdateRecCanonMedia_RowMissing_NoError — non-existent id: RowsAffected=0
// treated as no-op (docstring contract). No error bubbled.
func TestUpdateRecCanonMedia_RowMissing_NoError(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			repo := NewSeriesRepository(gdb)
			ctx := context.Background()

			// Empty DB, id=99999 doesn't exist.
			require.NoError(t, repo.UpdateRecCanonMedia(ctx, domain.SeriesID(99999), "/ru.jpg", "/ru_bd.jpg"),
				"UpdateRecCanonMedia MUST NOT error on missing row (defensive per docstring)")
		})
	}
}

// TestUpdateRecCanonMedia_UpdatesTimestamp — updated_at MUST advance on a
// real write. Complementary to BothEmpty_NoOp; asserts the write path
// stamps the row.
func TestUpdateRecCanonMedia_UpdatesTimestamp(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			repo := NewSeriesRepository(gdb)
			ctx := context.Background()

			enPoster := "/en.jpg"
			id := seedRecChildCanon(t, gdb, repo, "Rec Child",
				domain.TMDBID(1004),
				&enPoster,
				nil,
			)
			before, err := repo.Get(ctx, id)
			require.NoError(t, err)
			priorUpdatedAt := before.UpdatedAt

			// Sleep enough that Postgres millisecond precision resolves a
			// difference. SQLite stores DATETIME strings with second
			// precision — sleep >1s to be safe on both backends.
			time.Sleep(1100 * time.Millisecond)

			require.NoError(t, repo.UpdateRecCanonMedia(ctx, id, "/ru.jpg", ""))

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.True(t, got.UpdatedAt.After(priorUpdatedAt),
				"updated_at MUST advance on write: prior=%s got=%s",
				priorUpdatedAt.Format(time.RFC3339Nano), got.UpdatedAt.Format(time.RFC3339Nano))
		})
	}
}

// TestUpdateRecCanonMedia_DoesNotClobberFullEnrichment — surgical guarantee:
// writing ONLY poster+backdrop MUST NOT touch other Sonarr-authoritative
// columns (first_air_date, status, tmdb_rating, tmdb_votes, year, hydration,
// imdb_id, tvdb_id, original_title, etc). Regression guard against future
// contributor accidentally widening the map.
func TestUpdateRecCanonMedia_DoesNotClobberFullEnrichment(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			repo := NewSeriesRepository(gdb)
			ctx := context.Background()

			// Seed a fully-hydrated canon row with every recognisable
			// column populated so we can assert none of them shift.
			firstAir := time.Date(2020, 1, 15, 0, 0, 0, 0, time.UTC)
			runtime := 45
			rating := 8.5
			votes := 1234
			year := 2020
			pop := 42.7
			tmdbID := domain.TMDBID(2001)
			tvdbID := domain.TVDBID(3001)
			imdbID := domain.IMDBID("tt7654321")
			canon := series.Canon{
				OriginalTitle:    new("Full Rec Row (Original)"),
				TMDBID:           &tmdbID,
				TVDBID:           &tvdbID,
				IMDBID:           &imdbID,
				Hydration:        series.HydrationFull,
				Status:           new("Returning Series"),
				FirstAirDate:     &firstAir,
				Year:             &year,
				RuntimeMinutes:   &runtime,
				Homepage:         new("https://example.com"),
				OriginalLanguage: new("en"),
				Popularity:       &pop,
				InProduction:     true,
				TMDBRating:       &rating,
				TMDBVotes:        &votes,
			}
			id, err := repo.Upsert(ctx, canon)
			require.NoError(t, err)
			require.NotZero(t, id)
			// S-E3a — seed the legacy media columns directly (canon domain
			// fields removed); UpdateRecCanonMedia later overwrites them.
			seedRecCanonMedia(t, gdb, id, "/en_full.jpg", "/en_full_bd.jpg")

			// Snapshot pre-state.
			before, err := repo.Get(ctx, id)
			require.NoError(t, err)

			// Overwrite only media columns.
			require.NoError(t, repo.UpdateRecCanonMedia(ctx, id, "/ru_full.jpg", "/ru_full_bd.jpg"))

			after, err := repo.Get(ctx, id)
			require.NoError(t, err)

			// Media columns MUST be overwritten (read raw — toCanon no longer
			// surfaces poster_asset/backdrop_asset after S-E3a).
			gotPoster, gotBackdrop := readRecCanonMedia(t, gdb, id)
			require.NotNil(t, gotPoster)
			assert.Equal(t, "/ru_full.jpg", *gotPoster)
			require.NotNil(t, gotBackdrop)
			assert.Equal(t, "/ru_full_bd.jpg", *gotBackdrop)

			// Every other canon column MUST be untouched.
			assert.Equal(t, before.Hydration, after.Hydration,
				"hydration MUST NOT downgrade — narrow UPDATE does not touch this column")
			assert.Equal(t, before.Status, after.Status)
			assert.Equal(t, before.FirstAirDate, after.FirstAirDate)
			assert.Equal(t, before.Year, after.Year)
			assert.Equal(t, before.RuntimeMinutes, after.RuntimeMinutes)
			assert.Equal(t, before.Homepage, after.Homepage)
			assert.Equal(t, before.OriginalLanguage, after.OriginalLanguage)
			assert.Equal(t, before.Popularity, after.Popularity)
			assert.Equal(t, before.InProduction, after.InProduction)
			assert.Equal(t, before.TMDBRating, after.TMDBRating)
			assert.Equal(t, before.TMDBVotes, after.TMDBVotes)
			assert.Equal(t, before.OriginalTitle, after.OriginalTitle)
			require.NotNil(t, before.TMDBID)
			require.NotNil(t, after.TMDBID)
			assert.Equal(t, *before.TMDBID, *after.TMDBID)
			require.NotNil(t, before.TVDBID)
			require.NotNil(t, after.TVDBID)
			assert.Equal(t, *before.TVDBID, *after.TVDBID)
			require.NotNil(t, before.IMDBID)
			require.NotNil(t, after.IMDBID)
			assert.Equal(t, *before.IMDBID, *after.IMDBID)
		})
	}
}

// TestUpdateRecCanonMedia_ZeroSeriesID_Error — defensive: zero series_id
// caller must be rejected loudly. Story 571 §4.2 explicit ID guard.
func TestUpdateRecCanonMedia_ZeroSeriesID_Error(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			repo := NewSeriesRepository(gdb)
			ctx := context.Background()

			err := repo.UpdateRecCanonMedia(ctx, domain.SeriesID(0), "/ru.jpg", "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "series_id must be non-zero")
		})
	}
}
