//go:build integration

package persistence

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichmentapp "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	enrichmentdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// fkFakeTMDB serves a fixed tv + per-season payload for the E-FIX-1 FK-ordering
// integration test. GetTV / GetTVAllLangs return the tv; GetSeason returns the
// seeded season detail (the mismatched-bucket episode lives here).
type fkFakeTMDB struct {
	tv      *tmdb.TVResponse
	seasons map[int]*tmdb.SeasonResponse
}

func (f *fkFakeTMDB) GetTV(_ context.Context, _ int64, _ string) (*tmdb.TVResponse, error) {
	return f.tv, nil
}
func (f *fkFakeTMDB) GetTVAllLangs(_ context.Context, _ int64) (*tmdb.TVResponse, error) {
	return f.tv, nil
}
func (f *fkFakeTMDB) GetSeason(_ context.Context, _ int64, n int, _ string) (*tmdb.SeasonResponse, error) {
	return f.seasons[n], nil
}
func (f *fkFakeTMDB) GetPerson(_ context.Context, _ int64, _ string) (*tmdb.PersonResponse, error) {
	return nil, nil
}
func (f *fkFakeTMDB) FindByTVDB(_ context.Context, _ domain.TVDBID) (*tmdb.FindResponse, error) {
	return nil, nil
}

// --- Silent nop ports (return zero values). The E-FIX-1 payload carries no
// cast / taxonomy / videos / recommendations, so these are either never called
// or called with empty slices — a silent no-op keeps the full Handle path from
// touching side-tables the FK-ordering assertion does not care about. REAL
// Series/Seasons/Episodes + a real gorm Tx are wired below so the FK is enforced.

type fkSilentSeriesTexts struct{}

func (fkSilentSeriesTexts) Upsert(_ context.Context, _ series.SeriesText) error { return nil }

type fkSilentEpisodeTexts struct{}

func (fkSilentEpisodeTexts) Upsert(_ context.Context, _ series.EpisodeText) error { return nil }

type fkSilentPeople struct{}

func (fkSilentPeople) GetByTMDBID(_ context.Context, _ domain.TMDBID) (people.Person, error) {
	return people.Person{}, ports.ErrNotFound
}
func (fkSilentPeople) Upsert(_ context.Context, _ people.Person) (int64, error) { return 0, nil }

type fkSilentPersonCredits struct{}

func (fkSilentPersonCredits) BatchUpsert(_ context.Context, _ []people.PersonCredit) ([]int64, error) {
	return nil, nil
}
func (fkSilentPersonCredits) BatchUpsertAuthoritative(_ context.Context, _ []people.PersonCredit) ([]int64, error) {
	return nil, nil
}

type fkSilentGenres struct{}

func (fkSilentGenres) Upsert(_ context.Context, _ taxonomy.Genre) (int64, error) { return 0, nil }
func (fkSilentGenres) UpsertI18n(_ context.Context, _ int64, _, _ string) error  { return nil }
func (fkSilentGenres) Set(_ context.Context, _ domain.SeriesID, _ []int64) error { return nil }

type fkSilentKeywords struct{}

func (fkSilentKeywords) Upsert(_ context.Context, _ taxonomy.Keyword) (int64, error) { return 0, nil }
func (fkSilentKeywords) UpsertI18n(_ context.Context, _ int64, _, _ string) error    { return nil }
func (fkSilentKeywords) Set(_ context.Context, _ domain.SeriesID, _ []int64) error   { return nil }

type fkSilentNetworks struct{}

func (fkSilentNetworks) Upsert(_ context.Context, _ taxonomy.Network) (int64, error) { return 0, nil }
func (fkSilentNetworks) Set(_ context.Context, _ domain.SeriesID, _ []int64) error   { return nil }

type fkSilentCompanies struct{}

func (fkSilentCompanies) Upsert(_ context.Context, _ taxonomy.ProductionCompany) (int64, error) {
	return 0, nil
}
func (fkSilentCompanies) Set(_ context.Context, _ domain.SeriesID, _ []int64) error { return nil }

type fkSilentVideos struct{}

func (fkSilentVideos) Upsert(_ context.Context, _ enrichmentapp.VideoRow) error { return nil }

type fkSilentContentRatings struct{}

func (fkSilentContentRatings) Upsert(_ context.Context, _ domain.SeriesID, _, _ string) error {
	return nil
}

type fkSilentExternalIDs struct{}

func (fkSilentExternalIDs) Upsert(_ context.Context, _ enrichmentdomain.EntityType, _ int64, _, _ string) error {
	return nil
}

type fkSilentRecommendations struct{}

func (fkSilentRecommendations) Set(_ context.Context, _ domain.SeriesID, _ []domain.SeriesID) error {
	return nil
}

// TestSeriesWorker_MismatchedSeasonNumber_NoFKViolation — E-FIX-1 (D-0).
// Real Postgres/SQLite via the backend dispatch. A season detail whose episodes
// include a season_number absent from tv.Seasons must NOT trip
// episodes_season_id_fkey (SQLSTATE 23503): the tx commits, the orphan episode
// lands with season_id=NULL, the series is stamped synced, and NO
// enrichment_errors row is written.
func TestSeriesWorker_MismatchedSeasonNumber_NoFKViolation(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			ctx := context.Background()

			seriesRepo := NewSeriesRepository(gdb)
			seasonsRepo := NewSeasonsRepository(gdb)
			episodesRepo := NewEpisodesRepository(gdb)
			errorsRepo := NewEnrichmentErrorsRepository(gdb)
			tx := &inlineTransactor{db: gdb}
			fixedClock := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

			// Seed a library series with a tmdb_id + ZERO season rows (the
			// poisoned "Shark Week" prod state).
			tmdbID := domain.TMDBID(15871)
			canon := sampleCanon("Shark Week")
			canon.TMDBID = &tmdbID
			seriesID, err := seriesRepo.Upsert(ctx, canon)
			require.NoError(t, err)

			tv := &tmdb.TVResponse{
				ID:           15871,
				Name:         "Shark Week",
				Status:       "Returning Series",
				FirstAirDate: "2020-01-01",
				Seasons:      []tmdb.TVSeasonStub{{ID: 1, SeasonNumber: 1, EpisodeCount: 2}},
			}
			seasonResp := map[int]*tmdb.SeasonResponse{
				1: {
					SeasonNumber: 1,
					Episodes: []tmdb.SeasonEpisode{
						{ID: 900001, SeasonNumber: 1, EpisodeNumber: 1},
						{ID: 900007, SeasonNumber: 7, EpisodeNumber: 1}, // unresolvable
					},
				},
			}

			worker, err := enrichmentapp.NewSeriesWorker(enrichmentapp.SeriesWorkerDeps{
				TMDB:             &fkFakeTMDB{tv: tv, seasons: seasonResp},
				Tx:               tx,
				Languages:        []string{"en-US"},
				Series:           seriesRepo,
				SeriesTexts:      fkSilentSeriesTexts{},
				Seasons:          seasonsRepo,
				Episodes:         episodesRepo,
				EpisodeTexts:     fkSilentEpisodeTexts{},
				People:           fkSilentPeople{},
				PersonCredits:    fkSilentPersonCredits{},
				Genres:           fkSilentGenres{},
				Keywords:         fkSilentKeywords{},
				Networks:         fkSilentNetworks{},
				Companies:        fkSilentCompanies{},
				Videos:           fkSilentVideos{},
				ContentRatings:   fkSilentContentRatings{},
				ExternalIDs:      fkSilentExternalIDs{},
				Recommendations:  fkSilentRecommendations{},
				EnrichmentErrors: errorsRepo,
				Logger:           slog.Default(),
				Clock:            func() time.Time { return fixedClock },
			})
			require.NoError(t, err)

			require.NoError(t, worker.HandleForced(ctx, seriesID))

			// 1. Episodes committed (tx did NOT roll back).
			var epCount int64
			require.NoError(t, gdb.WithContext(ctx).Table("episodes").
				Where("series_id = ?", int64(seriesID)).Count(&epCount).Error)
			assert.EqualValues(t, 2, epCount, "both episodes must persist — no rollback")

			// 2. The orphan (season 7) episode carries NULL season_id.
			var orphanSeasonID *int64
			require.NoError(t, gdb.WithContext(ctx).Table("episodes").
				Select("season_id").
				Where("series_id = ? AND season_number = 7", int64(seriesID)).
				Scan(&orphanSeasonID).Error)
			assert.Nil(t, orphanSeasonID, "unresolvable season episode → NULL season_id (not 0)")

			// 3. In-bucket (season 1) episode links to the real season row.
			var s1SeasonID *int64
			require.NoError(t, gdb.WithContext(ctx).Table("episodes").
				Select("season_id").
				Where("series_id = ? AND season_number = 1", int64(seriesID)).
				Scan(&s1SeasonID).Error)
			require.NotNil(t, s1SeasonID)
			assert.Greater(t, *s1SeasonID, int64(0))

			// 4. Series stamped synced + NO error row (clean success).
			var syncedAt *time.Time
			require.NoError(t, gdb.WithContext(ctx).Table("series").
				Select("enrichment_tmdb_synced_at").
				Where("id = ?", int64(seriesID)).Scan(&syncedAt).Error)
			assert.NotNil(t, syncedAt, "successful enrichment stamps synced_at")

			var errCount int64
			require.NoError(t, gdb.WithContext(ctx).Table("enrichment_errors").
				Where("entity_id = ? AND source = ?", int64(seriesID), string(enrichmentdomain.SourceTMDBSeries)).
				Count(&errCount).Error)
			assert.EqualValues(t, 0, errCount, "clean commit leaves no enrichment_errors row")
		})
	}
}
