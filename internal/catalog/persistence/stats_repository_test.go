package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

var statsNow = time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

func ports0Totals() ports.StatsTotals { return ports.StatsTotals{} }

// seedStatsSeries inserts a bare series row (FK target for the join tables).
func seedStatsSeries(t *testing.T, db *gorm.DB, id domain.SeriesID) {
	t.Helper()
	require.NoError(t, db.Create(&database.SeriesModel{
		ID:              id,
		Hydration:       "stub",
		OriginCountries: datatypes.JSON([]byte("[]")), // NOT NULL column
		CreatedAt:       statsNow,
		UpdatedAt:       statsNow,
	}).Error)
}

// seedStatsCache inserts one series_cache row. deletedAt nil = live row.
func seedStatsCache(t *testing.T, db *gorm.DB, instance string, sonarrID domain.SonarrSeriesID, seriesID domain.SeriesID, episodeFiles int, size int64, deletedAt *time.Time) {
	t.Helper()
	sid := seriesID
	require.NoError(t, db.Create(&database.SeriesCacheModel{
		InstanceName:     domain.InstanceName(instance),
		SonarrSeriesID:   sonarrID,
		SeriesID:         &sid,
		TitleSlug:        "slug",
		EpisodeFileCount: episodeFiles,
		SizeOnDiskBytes:  size,
		UpdatedAt:        statsNow,
		DeletedAt:        deletedAt,
	}).Error)
}

func seedStatsGenre(t *testing.T, db *gorm.DB, genreID int64) {
	t.Helper()
	require.NoError(t, db.Create(&database.GenreModel{ID: genreID, CreatedAt: statsNow, UpdatedAt: statsNow}).Error)
}

func seedStatsGenreI18n(t *testing.T, db *gorm.DB, genreID int64, lang, name string) {
	t.Helper()
	require.NoError(t, db.Create(&database.GenreI18nModel{GenreID: genreID, Language: lang, Name: name, UpdatedAt: statsNow}).Error)
}

func seedStatsSeriesGenre(t *testing.T, db *gorm.DB, seriesID domain.SeriesID, genreID int64) {
	t.Helper()
	require.NoError(t, db.Create(&database.SeriesGenreModel{SeriesID: seriesID, GenreID: genreID}).Error)
}

func seedStatsNetwork(t *testing.T, db *gorm.DB, networkID int64, name string) {
	t.Helper()
	require.NoError(t, db.Create(&database.NetworkModel{ID: networkID, Name: name, CreatedAt: statsNow, UpdatedAt: statsNow}).Error)
}

func seedStatsSeriesNetwork(t *testing.T, db *gorm.DB, seriesID domain.SeriesID, networkID int64) {
	t.Helper()
	require.NoError(t, db.Create(&database.SeriesNetworkModel{SeriesID: seriesID, NetworkID: networkID}).Error)
}

func seedStatsGrab(t *testing.T, db *gorm.DB, instance string, status grab.Status) {
	t.Helper()
	require.NoError(t, db.Create(&database.GrabRecordModel{
		ID:           uuid.NewString(),
		InstanceName: domain.InstanceName(instance),
		Status:       string(status),
		CreatedAt:    statsNow,
		UpdatedAt:    statsNow,
	}).Error)
}

func seedStatsTorrent(t *testing.T, db *gorm.DB, instance, hash string, uploaded, downloaded int64, ratio float64, present bool) {
	t.Helper()
	require.NoError(t, db.Create(&database.QbitTorrentModel{
		InstanceName: domain.InstanceName(instance),
		Hash:         hash,
		Name:         "torrent-" + hash,
		StateRaw:     "seeding",
		StateGroup:   "complete",
		Uploaded:     uploaded,
		Downloaded:   downloaded,
		Ratio:        ratio,
		Present:      present,
		FirstSeenAt:  statsNow,
		UpdatedAt:    statsNow,
	}).Error)
	// GORM omits the zero-value bool on Create because the column carries
	// `default:true`, so a present=false seed silently lands present=true.
	// Force the false explicitly.
	if !present {
		require.NoError(t, db.Model(&database.QbitTorrentModel{}).
			Where("instance_name = ? AND hash = ?", domain.InstanceName(instance), hash).
			Update("present", false).Error)
	}
}

func TestStatsRepository_TotalsGenreNetwork(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewStatsRepository(db)
			ctx := context.Background()

			// Series 1: Drama+Sci-Fi on HBO, 10 files, 1000 bytes.
			// Series 2: Drama on HBO, 5 files, 3000 bytes.
			// Series 3: Comedy (genre WITHOUT i18n) on Netflix, 2 files, 500 bytes.
			// Series 4: soft-deleted cache row — must be excluded everywhere.
			for _, id := range []domain.SeriesID{1, 2, 3, 4} {
				seedStatsSeries(t, db, id)
			}
			seedStatsGenre(t, db, 10) // Drama
			seedStatsGenreI18n(t, db, 10, "en-US", "Drama")
			seedStatsGenreI18n(t, db, 10, "ru", "Драма")
			seedStatsGenre(t, db, 11) // Sci-Fi (only ru i18n → any-lang fallback)
			seedStatsGenreI18n(t, db, 11, "ru", "Фантастика")
			seedStatsGenre(t, db, 12) // Comedy — NO i18n row at all → name ""
			seedStatsNetwork(t, db, 20, "HBO")
			seedStatsNetwork(t, db, 21, "Netflix")

			seedStatsCache(t, db, "main", 100, 1, 10, 1000, nil)
			seedStatsCache(t, db, "main", 101, 2, 5, 3000, nil)
			seedStatsCache(t, db, "main", 102, 3, 2, 500, nil)
			seedStatsCache(t, db, "main", 103, 4, 99, 999999, &statsNow) // soft-deleted

			seedStatsSeriesGenre(t, db, 1, 10)
			seedStatsSeriesGenre(t, db, 1, 11)
			seedStatsSeriesGenre(t, db, 2, 10)
			seedStatsSeriesGenre(t, db, 3, 12)
			seedStatsSeriesGenre(t, db, 4, 10) // on deleted series → excluded

			seedStatsSeriesNetwork(t, db, 1, 20)
			seedStatsSeriesNetwork(t, db, 2, 20)
			seedStatsSeriesNetwork(t, db, 3, 21)
			seedStatsSeriesNetwork(t, db, 4, 20) // deleted → excluded

			// --- Totals: 3 live series, 17 files, 4500 bytes ---
			totals, err := repo.Totals(ctx, "main")
			require.NoError(t, err)
			assert.Equal(t, 3, totals.SeriesCount)
			assert.Equal(t, 17, totals.EpisodesOnDisk)
			assert.Equal(t, int64(4500), totals.TotalSizeBytes)

			// --- ByGenre ordered by size DESC: Drama(4000,2), Sci-Fi(1000,1), Comedy(500,1,"") ---
			genres, err := repo.ByGenre(ctx, "main", 20)
			require.NoError(t, err)
			require.Len(t, genres, 3)
			assert.Equal(t, "Drama", genres[0].Name)
			assert.Equal(t, 2, genres[0].SeriesCount)
			assert.Equal(t, int64(4000), genres[0].SizeBytes)
			assert.Equal(t, "Фантастика", genres[1].Name) // any-lang fallback (no en)
			assert.Equal(t, int64(1000), genres[1].SizeBytes)
			assert.Equal(t, "", genres[2].Name) // no i18n row at all
			assert.Equal(t, int64(500), genres[2].SizeBytes)

			// --- ByNetwork: HBO(4000,2), Netflix(500,1) ---
			nets, err := repo.ByNetwork(ctx, "main", 20)
			require.NoError(t, err)
			require.Len(t, nets, 2)
			assert.Equal(t, "HBO", nets[0].Name)
			assert.Equal(t, 2, nets[0].SeriesCount)
			assert.Equal(t, int64(4000), nets[0].SizeBytes)
			assert.Equal(t, "Netflix", nets[1].Name)
			assert.Equal(t, int64(500), nets[1].SizeBytes)

			// --- top-N cap ---
			capped, err := repo.ByGenre(ctx, "main", 1)
			require.NoError(t, err)
			require.Len(t, capped, 1)
			assert.Equal(t, "Drama", capped[0].Name)
		})
	}
}

func TestStatsRepository_GrabAndTorrent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewStatsRepository(db)
			ctx := context.Background()

			// grabs: 1 grabbed, 3 imported, 1 grab_failed, 1 import_failed → failed=2.
			seedStatsGrab(t, db, "main", grab.StatusGrabbed)
			seedStatsGrab(t, db, "main", grab.StatusImported)
			seedStatsGrab(t, db, "main", grab.StatusImported)
			seedStatsGrab(t, db, "main", grab.StatusImported)
			seedStatsGrab(t, db, "main", grab.StatusGrabFailed)
			seedStatsGrab(t, db, "main", grab.StatusImportFailed)
			seedStatsGrab(t, db, "other", grab.StatusImported) // isolation

			g, err := repo.GrabSuccess(ctx, "main")
			require.NoError(t, err)
			assert.Equal(t, 1, g.Grabbed)
			assert.Equal(t, 3, g.Imported)
			assert.Equal(t, 2, g.Failed)

			// torrents: 2 present (up 100+300, down 40+60, ratio 2.0+4.0),
			// 1 absent (must be excluded).
			seedStatsTorrent(t, db, "main", "aaa", 100, 40, 2.0, true)
			seedStatsTorrent(t, db, "main", "bbb", 300, 60, 4.0, true)
			seedStatsTorrent(t, db, "main", "ccc", 999, 999, 9.0, false) // present=false
			seedStatsTorrent(t, db, "other", "ddd", 1, 1, 1.0, true)     // isolation

			tt, err := repo.TorrentTotals(ctx, "main")
			require.NoError(t, err)
			assert.Equal(t, 2, tt.TorrentCount)
			assert.Equal(t, int64(400), tt.TotalUploadedBytes)
			assert.Equal(t, int64(100), tt.TotalDownloadedBytes)
			assert.InDelta(t, 3.0, tt.AvgRatio, 1e-9) // (2+4)/2

			names, err := repo.DistinctInstances(ctx)
			require.NoError(t, err)
			assert.Empty(t, names, "no series_cache rows → no instances even with grabs/torrents")
		})
	}
}

func TestStatsRepository_EmptyAndIsolation(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewStatsRepository(db)
			ctx := context.Background()

			// Empty DB — every aggregation is zero/empty, no NULL scan error.
			totals, err := repo.Totals(ctx, "main")
			require.NoError(t, err)
			assert.Equal(t, ports0Totals(), totals)

			genres, err := repo.ByGenre(ctx, "main", 20)
			require.NoError(t, err)
			assert.Empty(t, genres)

			nets, err := repo.ByNetwork(ctx, "main", 20)
			require.NoError(t, err)
			assert.Empty(t, nets)

			g, err := repo.GrabSuccess(ctx, "main")
			require.NoError(t, err)
			assert.Zero(t, g.Grabbed+g.Imported+g.Failed)

			tt, err := repo.TorrentTotals(ctx, "main")
			require.NoError(t, err)
			assert.Zero(t, tt.TorrentCount)
			assert.Equal(t, float64(0), tt.AvgRatio)

			// Two instances, isolation on totals + DistinctInstances order.
			seedStatsSeries(t, db, 1)
			seedStatsSeries(t, db, 2)
			seedStatsCache(t, db, "b", 200, 1, 4, 400, nil)
			seedStatsCache(t, db, "a", 201, 2, 9, 900, nil)

			names, err := repo.DistinctInstances(ctx)
			require.NoError(t, err)
			assert.Equal(t, []string{"a", "b"}, names)

			aTotals, err := repo.Totals(ctx, "a")
			require.NoError(t, err)
			assert.Equal(t, 1, aTotals.SeriesCount)
			assert.Equal(t, int64(900), aTotals.TotalSizeBytes)

			bTotals, err := repo.Totals(ctx, "b")
			require.NoError(t, err)
			assert.Equal(t, int64(400), bTotals.TotalSizeBytes)
		})
	}
}
