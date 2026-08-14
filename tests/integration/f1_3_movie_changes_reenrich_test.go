//go:build integration

// Ф1.3 — end-to-end proof of the movie Changes-API re-enrichment loop on real
// Postgres: a movie the /movie/changes poller stamped (tmdb_changed_at bumped
// past its section clocks) is picked by PickMovieRefreshCandidates in the
// CHANGED tier, the widened MovieWorker re-hydrates EVERY section (canon+i18n,
// cast, genres, keywords, companies, videos, recommendations) and re-stamps
// every section clock, and afterwards the movie falls OUT of the CHANGED tier
// (synced_at now newer than tmdb_changed_at) so it never re-picks — the
// confidence gate for enabling the poller in prod (see
// documentation/stories/Ф1.3-movie-changes-reenrich.md).
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	enrichdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/wiring"
)

// f13FullMovieTMDB is a fake appenrich.MovieTMDBClient returning ONE full movie
// payload exercising every section writer (cast/genres/keywords/companies/
// videos/recommendations/translations). The requested id/language are ignored —
// the worker always targets the seeded canon row by PK.
type f13FullMovieTMDB struct{ resp *tmdb.MovieResponse }

func (f *f13FullMovieTMDB) GetMovie(_ context.Context, _ int64, _ string) (*tmdb.MovieResponse, error) {
	return f.resp, nil
}

func f13FullPayload() *tmdb.MovieResponse {
	return &tmdb.MovieResponse{
		ID:            603,
		Title:         "The Matrix", // response root = base lang (en-US)
		OriginalTitle: "The Matrix",
		Overview:      "A hacker learns the truth.",
		Tagline:       "Free your mind.",
		Status:        "Released",
		ReleaseDate:   "1999-03-31",
		VoteAverage:   8.2,
		PosterPath:    "/matrix_poster.jpg",
		BackdropPath:  "/matrix_backdrop.jpg",
		Translations: &tmdb.TVTranslations{Translations: []tmdb.TVTranslation{
			{ISO6391: "en", Data: tmdb.TVTranslationData{Name: "The Matrix", Overview: "en ov", Tagline: "en tag"}},
			{ISO6391: "ru", Data: tmdb.TVTranslationData{Name: "Матрица", Overview: "ру описание", Tagline: "ру слоган"}},
		}},
		Credits: &tmdb.MovieCredits{Cast: []tmdb.MovieCastMember{
			{ID: 6384, Name: "Keanu Reeves", CreditID: "c-neo", Order: 0},
			{ID: 2975, Name: "Laurence Fishburne", CreditID: "c-morph", Order: 1},
		}},
		Genres: []tmdb.TVGenre{
			{ID: 878, Name: "Science Fiction"},
			{ID: 28, Name: "Action"},
		},
		Keywords: &tmdb.MovieKeywords{Keywords: []tmdb.TVKeyword{
			{ID: 4565, Name: "dystopia"},
			{ID: 310, Name: "artificial intelligence"},
		}},
		ProductionCompanies: []tmdb.TVCompany{
			{ID: 79, Name: "Village Roadshow Pictures", LogoPath: "/vr.png", OriginCountry: "US"},
		},
		Videos: &tmdb.TVVideos{Results: []tmdb.TVVideo{
			{ID: "teaser", Site: "YouTube", Key: "t1", Type: "Teaser", Official: true, PublishedAt: "1999-01-01T00:00:00.000Z"},
			{ID: "trailer", Site: "YouTube", Key: "t2", Type: "Trailer", Official: true, PublishedAt: "1999-02-01T00:00:00.000Z"},
		}},
		Recommendations: &tmdb.MovieRecommendations{Results: []tmdb.MovieRecommendation{
			{ID: 604, Title: "The Matrix Reloaded"},
			{ID: 605, Title: "The Matrix Revolutions"},
		}},
	}
}

func TestF1_3_MovieChangesReenrich_Loop_Postgres(t *testing.T) {
	for _, b := range allD1Backends(t) {
		if b.name != "postgres" {
			continue
		}
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
			require.NoError(t, err)

			// Fixed clock: worker stamps become baseNow; seed clocks are baseNow-48h,
			// tmdb_changed_at is baseNow-1h (newer than the section clocks).
			baseNow := time.Now().UTC().Truncate(time.Microsecond)
			past := baseNow.Add(-48 * time.Hour)
			changedAt := baseNow.Add(-1 * time.Hour)

			movieRepo := enrichpersistence.NewMovieRepository(gdb)

			// 1. Seed a fully-enriched movie via the real Upsert, then drive its
			// section clocks to the past + stamp tmdb_changed_at recent (simulating
			// the /movie/changes poller having flagged it).
			tid := shareddomain.TMDBID(603)
			seedID, err := movieRepo.Upsert(ctx, movie.Canon{
				TMDBID:    &tid,
				Title:     "The Matrix (seed)",
				Hydration: movie.HydrationFull,
			})
			require.NoError(t, err)
			require.NotZero(t, seedID)

			require.NoError(t, gdb.WithContext(ctx).Table("movies").
				Where("id = ?", seedID).
				Updates(map[string]any{
					"enrichment_tmdb_synced_at":     past,
					"enrichment_cast_synced_at":     past,
					"enrichment_keywords_synced_at": past,
					"enrichment_media_synced_at":    past,
					"enrichment_recs_synced_at":     past,
					"tmdb_changed_at":               changedAt,
				}).Error)

			ttl := enrichdomain.DefaultRefreshTTL()

			// 2. First pick: the stamped movie MUST appear in the CHANGED tier.
			cands, err := movieRepo.PickMovieRefreshCandidates(ctx, baseNow, ttl, 50)
			require.NoError(t, err)
			require.Len(t, cands, 1, "only the seeded movie exists; it must be picked")
			require.Equal(t, seedID, cands[0].MovieID)
			require.Equal(t, enrichdomain.RefreshTierChanged, cands[0].Tier,
				"a changed movie (tmdb_changed_at > synced_at) must land in tier 0 CHANGED")

			// 3. Construct the widened worker with ALL real persistence repos + a
			// fake TMDB full payload. Resolver/OMDb/Collections stay nil (nil-OK).
			worker, err := appenrich.NewMovieWorker(appenrich.MovieWorkerDeps{
				TMDB:   &f13FullMovieTMDB{resp: f13FullPayload()},
				Movies: movieRepo,
				I18n:   enrichpersistence.NewMovieI18nSeeder(gdb),
				People: enrichpersistence.NewPeopleRepository(gdb),
				PersonCredits: wiring.PersonCreditsRepoAdapter{
					Inner: enrichpersistence.NewPersonCreditsRepository(gdb),
				},
				Tx: catalogpersistence.NewGormTransactor(gdb),
				Genres: wiring.GenresRepoAdapter{
					Main: enrichpersistence.NewGenresRepository(gdb),
					I18n: enrichpersistence.NewGenresI18nRepository(gdb),
				},
				Keywords: wiring.KeywordsRepoAdapter{
					Main: enrichpersistence.NewKeywordsRepository(gdb),
					I18n: enrichpersistence.NewKeywordsI18nRepository(gdb),
				},
				Companies: enrichpersistence.NewCompaniesRepository(gdb),
				Videos:    enrichpersistence.NewMovieVideosRepository(gdb),
				Recs:      enrichpersistence.NewMovieRecommendationsRepository(gdb),
				Clock:     func() time.Time { return baseNow },
			})
			require.NoError(t, err)

			// Re-enrich the changed movie.
			require.NoError(t, worker.HandleForced(ctx, int64(seedID)))

			// 4a. Every section clock advanced past the seeded -48h.
			var clocks struct {
				TMDB     *time.Time `gorm:"column:enrichment_tmdb_synced_at"`
				Cast     *time.Time `gorm:"column:enrichment_cast_synced_at"`
				Keywords *time.Time `gorm:"column:enrichment_keywords_synced_at"`
				Media    *time.Time `gorm:"column:enrichment_media_synced_at"`
				Recs     *time.Time `gorm:"column:enrichment_recs_synced_at"`
			}
			require.NoError(t, gdb.WithContext(ctx).Table("movies").
				Select("enrichment_tmdb_synced_at, enrichment_cast_synced_at, enrichment_keywords_synced_at, enrichment_media_synced_at, enrichment_recs_synced_at").
				Where("id = ?", seedID).Scan(&clocks).Error)
			for name, got := range map[string]*time.Time{
				"tmdb": clocks.TMDB, "cast": clocks.Cast, "keywords": clocks.Keywords,
				"media": clocks.Media, "recs": clocks.Recs,
			} {
				require.NotNilf(t, got, "%s section clock must be stamped", name)
				require.Truef(t, got.After(past), "%s section clock must advance past the seeded -48h", name)
			}

			// 4b. Every section side/join table has rows.
			f13Count := func(table, where string, args ...any) int64 {
				var n int64
				require.NoError(t, gdb.WithContext(ctx).Table(table).Where(where, args...).Count(&n).Error)
				return n
			}
			require.Equal(t, int64(2), f13Count("person_credits", "media_type = ? AND tmdb_media_id = ?", tmdb.MediaTypeMovie, int64(603)), "cast → person_credits")
			require.Equal(t, int64(2), f13Count("movie_genres", "movie_id = ?", seedID), "genres join")
			require.Equal(t, int64(2), f13Count("movie_keywords", "movie_id = ?", seedID), "keywords join")
			require.Equal(t, int64(1), f13Count("movie_companies", "movie_id = ?", seedID), "companies join")
			require.Equal(t, int64(1), f13Count("movie_videos", "movie_id = ?", seedID), "best-trailer")
			require.Equal(t, int64(2), f13Count("movie_recommendations", "movie_id = ?", seedID), "recs join")
			require.GreaterOrEqual(t, f13Count("movie_i18n", "movie_id = ?", seedID), int64(1), "i18n rows")

			var enTitle string
			require.NoError(t, gdb.WithContext(ctx).Table("movie_i18n").
				Select("title").Where("movie_id = ? AND language = ?", seedID, "en-US").
				Row().Scan(&enTitle))
			require.Equal(t, "The Matrix", enTitle, "en-US i18n row carries the re-enriched title")

			// 5. Confidence gate — no re-pick churn: synced_at is now newer than
			// tmdb_changed_at, so the movie falls out of the CHANGED tier and (being
			// fresh) out of NORMAL too. The worker's own recommendation-stub movies
			// (604/605) may appear as never-synced NORMAL candidates — expected — so
			// we assert the TARGET is gone, not that the list is empty.
			cands2, err := movieRepo.PickMovieRefreshCandidates(ctx, baseNow, ttl, 50)
			require.NoError(t, err)
			for _, c := range cands2 {
				require.NotEqualf(t, seedID, c.MovieID,
					"re-enriched movie must NOT re-pick (tier %d)", c.Tier)
			}
		})
	}
}
