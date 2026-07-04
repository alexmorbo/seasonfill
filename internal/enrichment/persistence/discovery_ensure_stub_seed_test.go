package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestDiscoveryEnsureStubSeed_D0 exercises the exact DB-write sequence the
// W15-6 stubUpserterAdapter.EnsureStub runs — UpsertStub (canon +
// original_title/original_language) → SeriesTextsRepository
// .InsertBaseLangIfAbsent(callLang) → SeriesMediaTextsRepository
// .InsertIfAbsent(callLang poster/backdrop). The adapter itself lives in
// internal/wiring (which has no testcontainers DB harness — only
// hand-rolled port stubs), so per the story we assert the persistence
// behavior directly through the same three repos, which drives the
// identical ON CONFLICT paths on both SQLite and Postgres.
func TestDiscoveryEnsureStubSeed_D0(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			seed := func(t *testing.T) (*SeriesRepository, *SeriesTextsRepository, *SeriesMediaTextsRepository) {
				t.Helper()
				gdb := backend.NewDB(t)
				return NewSeriesRepository(gdb),
					NewSeriesTextsRepository(gdb),
					NewSeriesMediaTextsRepository(gdb)
			}

			// ensureStub replays EnsureStub's write sequence for the call
			// language, exactly as stubUpserterAdapter.EnsureStub does.
			ensureStub := func(
				t *testing.T,
				sr *SeriesRepository, tr *SeriesTextsRepository, mr *SeriesMediaTextsRepository,
				tmdbID int, lang, title, origTitle, origLang string,
				poster, backdrop *string,
			) domain.SeriesID {
				t.Helper()
				canon := series.Canon{
					TMDBID:           ptrTMDBID(tmdbID),
					Hydration:        series.HydrationStub,
					OriginalTitle:    nonEmpty(origTitle),
					OriginalLanguage: nonEmpty(origLang),
				}
				sid, err := sr.UpsertStub(ctx, canon)
				require.NoError(t, err)
				tt := title
				require.NoError(t, tr.InsertBaseLangIfAbsent(ctx, series.SeriesText{
					SeriesID: sid, Language: lang, Title: &tt,
				}))
				if poster != nil || backdrop != nil {
					require.NoError(t, mr.InsertIfAbsent(ctx, series.SeriesMediaText{
						SeriesID: sid, Language: lang, PosterAsset: poster, BackdropAsset: backdrop,
					}))
				}
				return sid
			}

			t.Run("ru-RU seeds ru row and never poisons en-US", func(t *testing.T) {
				t.Parallel()
				sr, tr, mr := seed(t)
				sid := ensureStub(t, sr, tr, mr, 5001, "ru-RU", "Русское название", "Original EN", "en", nil, nil)

				got, err := tr.Get(ctx, sid, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, got.Title)
				assert.Equal(t, "Русское название", *got.Title)

				// The crux: a ru-RU warm must NOT create an en-US row with a
				// Cyrillic name (the pre-W15-6 poison).
				_, err = tr.Get(ctx, sid, "en-US")
				assert.ErrorIs(t, err, ports.ErrNotFound, "no en-US row must exist")
			})

			t.Run("en-US seeds the en-US row", func(t *testing.T) {
				t.Parallel()
				sr, tr, mr := seed(t)
				sid := ensureStub(t, sr, tr, mr, 5002, "en-US", "English Title", "English Title", "en", nil, nil)

				got, err := tr.Get(ctx, sid, "en-US")
				require.NoError(t, err)
				require.NotNil(t, got.Title)
				assert.Equal(t, "English Title", *got.Title)
			})

			t.Run("media seed is only-if-absent; RefreshAllLangs poster survives re-stub", func(t *testing.T) {
				t.Parallel()
				sr, tr, mr := seed(t)
				seedPoster := "/seed_poster.jpg"
				sid := ensureStub(t, sr, tr, mr, 5003, "ru-RU", "Русское", "Orig", "en", &seedPoster, nil)

				got, err := mr.Get(ctx, sid, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, got.PosterAsset)
				assert.Equal(t, seedPoster, *got.PosterAsset)

				// RefreshAllLangs writes the authoritative per-lang poster +
				// resolved hash via Upsert.
				refreshedPoster := "/refreshed_poster.jpg"
				refreshedHash := "sha256deadbeef"
				require.NoError(t, mr.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "ru-RU",
					PosterAsset: &refreshedPoster, PosterHash: &refreshedHash,
				}))

				// A later re-EnsureStub (InsertIfAbsent) must NOT clobber the
				// RefreshAllLangs value.
				restubPoster := "/restub_poster.jpg"
				require.NoError(t, mr.InsertIfAbsent(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "ru-RU", PosterAsset: &restubPoster,
				}))

				after, err := mr.Get(ctx, sid, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, after.PosterAsset)
				assert.Equal(t, refreshedPoster, *after.PosterAsset, "RefreshAllLangs poster must survive re-stub")
				require.NotNil(t, after.PosterHash)
				assert.Equal(t, refreshedHash, *after.PosterHash, "resolved hash must survive")
			})

			t.Run("original_title/original_language COALESCE-preserved on re-stub", func(t *testing.T) {
				t.Parallel()
				sr, tr, mr := seed(t)
				sid := ensureStub(t, sr, tr, mr, 5004, "en-US", "Title", "First Original", "ja", nil, nil)

				// Second EnsureStub for the SAME tmdb_id with DIFFERENT
				// original_* — UpsertStub COALESCEs existing-first, so the
				// first values survive.
				sid2 := ensureStub(t, sr, tr, mr, 5004, "en-US", "Title", "Second Original", "ko", nil, nil)
				assert.Equal(t, sid, sid2, "same tmdb_id resolves to the same series row")

				got, err := sr.Get(ctx, sid)
				require.NoError(t, err)
				require.NotNil(t, got.OriginalTitle)
				assert.Equal(t, "First Original", *got.OriginalTitle)
				require.NotNil(t, got.OriginalLanguage)
				assert.Equal(t, "ja", *got.OriginalLanguage)
			})
		})
	}
}

// TestSeriesMediaTextsRepository_InsertIfAbsent is the focused unit test for
// the W15-6 only-if-absent media seed: an initial insert lands, a second
// insert for the same (series_id, language) is a no-op preserving the first
// row, and validation rejects zero series_id / empty language.
func TestSeriesMediaTextsRepository_InsertIfAbsent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			seed := func(t *testing.T) (domain.SeriesID, *SeriesMediaTextsRepository) {
				t.Helper()
				gdb := backend.NewDB(t)
				sid, err := NewSeriesRepository(gdb).Upsert(ctx, sampleCanon("Insert If Absent Show"))
				require.NoError(t, err)
				return sid, NewSeriesMediaTextsRepository(gdb)
			}

			t.Run("insert then re-insert is a no-op", func(t *testing.T) {
				t.Parallel()
				sid, repo := seed(t)
				first := "/first.jpg"
				require.NoError(t, repo.InsertIfAbsent(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "en-US", PosterAsset: &first,
				}))
				second := "/second.jpg"
				require.NoError(t, repo.InsertIfAbsent(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "en-US", PosterAsset: &second,
				}))
				got, err := repo.Get(ctx, sid, "en-US")
				require.NoError(t, err)
				require.NotNil(t, got.PosterAsset)
				assert.Equal(t, first, *got.PosterAsset, "first row must be preserved")
			})

			t.Run("validation error pairs", func(t *testing.T) {
				t.Parallel()
				_, repo := seed(t)
				assert.Error(t, repo.InsertIfAbsent(ctx, series.SeriesMediaText{
					SeriesID: 0, Language: "en-US",
				}))
				assert.Error(t, repo.InsertIfAbsent(ctx, series.SeriesMediaText{
					SeriesID: 1, Language: "",
				}))
			})
		})
	}
}

// nonEmpty mirrors wiring.nonEmptyPtr for the test's canon builder — nil for
// an empty string so original_* seeds a SQL NULL, not "".
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
