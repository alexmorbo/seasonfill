package scan_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type syncFixture struct {
	db   *gorm.DB
	deps scan.SyncDeps
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))

	seriesRepo := enrichpersistence.NewSeriesRepository(db)
	seriesCacheRepo := catalogpersistence.NewSeriesCacheRepository(db, seriesRepo)
	episodesRepo := enrichpersistence.NewEpisodesRepository(db)
	episodeStatesRepo := catalogpersistence.NewEpisodeStatesRepository(db)
	episodeTextsRepo := enrichpersistence.NewEpisodeTextsRepository(db)
	seasonStatsRepo := catalogpersistence.NewSeasonStatsRepository(db)
	genresRepo := enrichpersistence.NewGenresRepository(db)
	genresI18nRepo := enrichpersistence.NewGenresI18nRepository(db)
	networksRepo := enrichpersistence.NewNetworksRepository(db)

	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))

	deps := scan.SyncDeps{
		Series:        seriesRepo,
		SeriesCache:   seriesCacheRepo,
		Episodes:      episodesRepo,
		EpisodeStates: episodeStatesRepo,
		EpisodeTexts:  episodeTextsRepo,
		SeasonStats:   seasonStatsRepo,
		Genres:        scan.NewGenresAdapter(genresRepo, genresI18nRepo),
		Networks:      scan.NewNetworksAdapter(networksRepo),
		Logger:        lg,
	}
	return &syncFixture{db: db, deps: deps}
}

func (f *syncFixture) countTable(t *testing.T, table string) int {
	t.Helper()
	var n int64
	require.NoError(t, f.db.Table(table).Count(&n).Error)
	return int(n)
}

// TestSyncSeriesFromSonarr_TwoInstances_DedupCanon — the load-bearing
// two-instance dedupe invariant (PRD §5.11): two Sonarr instances of
// the same show converge on ONE canon row, ONE set of join rows, but
// TWO cache rows.
func TestSyncSeriesFromSonarr_TwoInstances_DedupCanon(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	cases := []struct {
		name         string
		payloads     map[string]sonarr.SeriesPayload
		canonRows    int
		cacheRows    int
		networkJoins int
		genreJoins   int
	}{
		{
			name: "shared tmdb_id collapses canon",
			payloads: map[string]sonarr.SeriesPayload{
				"homelab":    {ID: 122, Title: "Severance", TVDBID: 386818, TMDBID: 95396, Year: 2022, Network: "Apple TV+", Genres: []string{"Drama", "Sci-Fi"}, Monitored: true, TitleSlug: "severance"},
				"homelab-4k": {ID: 87, Title: "Severance", TVDBID: 386818, TMDBID: 95396, Year: 2022, Network: "Apple TV+", Genres: []string{"Drama", "Sci-Fi"}, Monitored: true, TitleSlug: "severance"},
			},
			canonRows:    1,
			cacheRows:    2,
			networkJoins: 1,
			genreJoins:   2,
		},
		{
			name: "shared tvdb_id when tmdb_id missing",
			payloads: map[string]sonarr.SeriesPayload{
				"homelab":    {ID: 50, Title: "Local Show", TVDBID: 999111, Year: 2024, Monitored: true, TitleSlug: "local-show"},
				"homelab-4k": {ID: 70, Title: "Local Show", TVDBID: 999111, Year: 2024, Monitored: true, TitleSlug: "local-show"},
			},
			canonRows: 1,
			cacheRows: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newSyncFixture(t)
			ctx := context.Background()
			for inst, p := range tc.payloads {
				_, err := scan.SyncSeriesFromSonarr(ctx, f.deps, domain.InstanceName(inst), scan.SonarrPayloadBundle{Series: p})
				require.NoError(t, err)
			}
			assert.Equal(t, tc.canonRows, f.countTable(t, "series"), "series rows")
			assert.Equal(t, tc.cacheRows, f.countTable(t, "series_cache"), "series_cache rows")
			if tc.networkJoins > 0 {
				assert.Equal(t, tc.networkJoins, f.countTable(t, "series_networks"), "series_networks joins")
			}
			if tc.genreJoins > 0 {
				assert.Equal(t, tc.genreJoins, f.countTable(t, "series_genres"), "series_genres joins")
			}
		})
	}
}

// TestSyncSeriesFromSonarr_OrphanCreation — Sonarr payload with only
// tvdbId (no tmdb) creates a stub canon row with tmdb_id=NULL.
func TestSyncSeriesFromSonarr_OrphanCreation(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	p := sonarr.SeriesPayload{ID: 11, Title: "Orphan", TVDBID: 12345, Year: 2025, Monitored: true, TitleSlug: "orphan"}
	canonID, err := scan.SyncSeriesFromSonarr(ctx, f.deps, "homelab", scan.SonarrPayloadBundle{Series: p})
	require.NoError(t, err)
	assert.NotZero(t, canonID)

	var row database.SeriesModel
	require.NoError(t, f.db.Where("id = ?", canonID).First(&row).Error)
	assert.Nil(t, row.TMDBID, "orphan canon has NULL tmdb_id")
	require.NotNil(t, row.TVDBID)
	assert.Equal(t, domain.TVDBID(12345), *row.TVDBID)
	assert.Equal(t, "stub", row.Hydration)
}

// TestSyncSeriesFromSonarr_NetworkResolution_CreatesOnMiss — sync of
// a never-seen network creates the networks row and join.
func TestSyncSeriesFromSonarr_NetworkResolution_CreatesOnMiss(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	p := sonarr.SeriesPayload{ID: 1, Title: "S1", TVDBID: 100, Year: 2024, Network: "FuboTV", Monitored: true, TitleSlug: "s1"}
	_, err := scan.SyncSeriesFromSonarr(ctx, f.deps, "homelab", scan.SonarrPayloadBundle{Series: p})
	require.NoError(t, err)
	assert.Equal(t, 1, f.countTable(t, "networks"))
	assert.Equal(t, 1, f.countTable(t, "series_networks"))

	// Second show on FuboTV reuses the existing networks row.
	p2 := sonarr.SeriesPayload{ID: 2, Title: "S2", TVDBID: 200, Year: 2024, Network: "FuboTV", Monitored: true, TitleSlug: "s2"}
	_, err = scan.SyncSeriesFromSonarr(ctx, f.deps, "homelab", scan.SonarrPayloadBundle{Series: p2})
	require.NoError(t, err)
	assert.Equal(t, 1, f.countTable(t, "networks"), "FuboTV reused, not duplicated")
	assert.Equal(t, 2, f.countTable(t, "series_networks"))
}

// TestSyncSeriesFromSonarr_GenreResolution_CreatesI18n — sync with
// genres against an empty dictionary creates genres + genres_i18n rows.
func TestSyncSeriesFromSonarr_GenreResolution_CreatesI18n(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	p := sonarr.SeriesPayload{ID: 1, Title: "S1", TVDBID: 100, Year: 2024, Genres: []string{"Drama"}, Monitored: true, TitleSlug: "s1"}
	_, err := scan.SyncSeriesFromSonarr(ctx, f.deps, "homelab", scan.SonarrPayloadBundle{Series: p})
	require.NoError(t, err)
	assert.Equal(t, 1, f.countTable(t, "genres"))
	assert.Equal(t, 1, f.countTable(t, "genres_i18n"))
	assert.Equal(t, 1, f.countTable(t, "series_genres"))
}

// TestSyncSeriesFromSonarr_EpisodesPerInstance — single series, two
// instances; assert one canonical episode row per (season, episode),
// two episode_states rows per episode.
func TestSyncSeriesFromSonarr_EpisodesPerInstance(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	air := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	p := sonarr.SeriesPayload{ID: 1, Title: "S1", TVDBID: 100, TMDBID: 200, Year: 2024, Monitored: true, TitleSlug: "s1"}
	bundle := scan.SonarrPayloadBundle{
		Series: p,
		Episodes: []sonarr.EpisodePayload{
			{ID: 11, EpisodeNumber: 1, SeasonNumber: 1, Title: "E1", AirDateUTC: air, Monitored: true, HasFile: false},
			{ID: 12, EpisodeNumber: 2, SeasonNumber: 1, Title: "E2", AirDateUTC: air, Monitored: true, HasFile: true, EpisodeFileID: 99},
		},
		EpisodeFiles: []sonarr.EpisodeFilePayload{
			{ID: 99, SeasonNumber: 1, QualityName: "WEBDL-1080p", SizeBytes: 100},
		},
	}
	_, err := scan.SyncSeriesFromSonarr(ctx, f.deps, "homelab", bundle)
	require.NoError(t, err)
	// Same show synced from a second instance.
	p4k := sonarr.SeriesPayload{ID: 2, Title: "S1", TVDBID: 100, TMDBID: 200, Year: 2024, Monitored: true, TitleSlug: "s1"}
	bundle4k := scan.SonarrPayloadBundle{
		Series: p4k,
		Episodes: []sonarr.EpisodePayload{
			{ID: 21, EpisodeNumber: 1, SeasonNumber: 1, Title: "E1", AirDateUTC: air, Monitored: true, HasFile: true, EpisodeFileID: 200},
			{ID: 22, EpisodeNumber: 2, SeasonNumber: 1, Title: "E2", AirDateUTC: air, Monitored: true, HasFile: true, EpisodeFileID: 201},
		},
		EpisodeFiles: []sonarr.EpisodeFilePayload{
			{ID: 200, SeasonNumber: 1, QualityName: "WEBDL-2160p", SizeBytes: 400},
			{ID: 201, SeasonNumber: 1, QualityName: "WEBDL-2160p", SizeBytes: 500},
		},
	}
	_, err = scan.SyncSeriesFromSonarr(ctx, f.deps, "homelab-4k", bundle4k)
	require.NoError(t, err)
	assert.Equal(t, 1, f.countTable(t, "series"))
	assert.Equal(t, 2, f.countTable(t, "episodes"), "two canonical episodes")
	assert.Equal(t, 4, f.countTable(t, "episode_states"), "two instances x two episodes")
}

// TestSyncSeriesFromSonarr_EmptyEpisodes — bundle without episodes
// short-circuits cleanly (canon + cache rows present, no episode-side
// writes).
func TestSyncSeriesFromSonarr_EmptyEpisodes(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	p := sonarr.SeriesPayload{ID: 1, Title: "S1", TVDBID: 100, Year: 2024, Monitored: true, TitleSlug: "s1"}
	_, err := scan.SyncSeriesFromSonarr(ctx, f.deps, "homelab", scan.SonarrPayloadBundle{Series: p})
	require.NoError(t, err)
	assert.Equal(t, 1, f.countTable(t, "series"))
	assert.Equal(t, 1, f.countTable(t, "series_cache"))
	assert.Equal(t, 0, f.countTable(t, "episodes"))
	assert.Equal(t, 0, f.countTable(t, "episode_states"))
}

// TestSyncSeriesFromSonarr_Idempotent — re-running the same sync
// against an unchanged payload is a no-op modulo updated_at.
func TestSyncSeriesFromSonarr_Idempotent(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	p := sonarr.SeriesPayload{ID: 1, Title: "S1", TVDBID: 100, Year: 2024, Network: "HBO", Genres: []string{"Drama"}, Monitored: true, TitleSlug: "s1"}
	_, err := scan.SyncSeriesFromSonarr(ctx, f.deps, "homelab", scan.SonarrPayloadBundle{Series: p})
	require.NoError(t, err)
	_, err = scan.SyncSeriesFromSonarr(ctx, f.deps, "homelab", scan.SonarrPayloadBundle{Series: p})
	require.NoError(t, err)
	assert.Equal(t, 1, f.countTable(t, "series"))
	assert.Equal(t, 1, f.countTable(t, "series_cache"))
	assert.Equal(t, 1, f.countTable(t, "networks"))
	assert.Equal(t, 1, f.countTable(t, "series_networks"))
	assert.Equal(t, 1, f.countTable(t, "genres"))
	assert.Equal(t, 1, f.countTable(t, "series_genres"))
}

// TestSyncEpisodes_PopulatesMediaMeta — verify that episode-file media
// metadata (codecs, channels, release group) propagates from the payload
// into episode_states.
func TestSyncEpisodes_PopulatesMediaMeta(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	seriesPayload := sonarr.SeriesPayload{
		ID:        1,
		Title:     "MediaMetaTest",
		TVDBID:    888,
		Year:      2024,
		Monitored: true,
		TitleSlug: "test",
	}
	canonID, err := scan.SyncSeriesFromSonarr(ctx, f.deps, "main", scan.SonarrPayloadBundle{Series: seriesPayload})
	require.NoError(t, err)

	// Now sync episodes with mediaInfo. We need to call SyncSeriesFromSonarr
	// with the full bundle including episodes and files.
	bundleWithFiles := scan.SonarrPayloadBundle{
		Series: seriesPayload,
		Episodes: []sonarr.EpisodePayload{{
			ID:            10,
			EpisodeNumber: 1,
			SeasonNumber:  5,
			HasFile:       true,
			EpisodeFileID: 100,
			AirDateUTC:    time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		}},
		EpisodeFiles: []sonarr.EpisodeFilePayload{{
			ID:            100,
			QualityName:   "WEBDL-1080p",
			SizeBytes:     1024,
			VideoCodec:    "HEVC",
			AudioCodec:    "DDP",
			AudioChannels: "5.1",
			ReleaseGroup:  "RARBG",
		}},
	}
	_, err = scan.SyncSeriesFromSonarr(ctx, f.deps, "main", bundleWithFiles)
	require.NoError(t, err)

	// Look up the episode state we just created
	// The episode ID comes from the database — we can query by series + season + episode
	var ep database.EpisodeModel
	require.NoError(t, f.db.Where("series_id = ? AND season_number = ? AND episode_number = ?",
		canonID, 5, 1).First(&ep).Error)

	// Query the episode_states table directly
	var state database.EpisodeStateModel
	require.NoError(t, f.db.Where("instance_name = ? AND episode_id = ?", "main", ep.ID).First(&state).Error)
	require.NotNil(t, state.VideoCodec)
	assert.Equal(t, "HEVC", *state.VideoCodec)
	require.NotNil(t, state.AudioCodec)
	assert.Equal(t, "DDP", *state.AudioCodec)
	require.NotNil(t, state.AudioChannels)
	assert.Equal(t, "5.1", *state.AudioChannels)
	require.NotNil(t, state.ReleaseGroup)
	assert.Equal(t, "RARBG", *state.ReleaseGroup)
}

// TestSyncSeriesFromSonarr_WritesSeasonStats — story 377.
// One Upsert per Sonarr season; counters come straight from
// p.Seasons[].Statistics.
func TestSyncSeriesFromSonarr_WritesSeasonStats(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	p := sonarr.SeriesPayload{
		ID: 140, Title: "Rick and Morty", TitleSlug: "rick-and-morty",
		Monitored: true,
		Seasons: []series.Season{
			{
				Number: 1, Monitored: true,
				Statistics: series.Statistics{
					EpisodeCount:     11,
					EpisodeFileCount: 11,
					Total:            11,
					Aired:            11,
					SizeOnDisk:       12_000_000_000,
				},
			},
			{
				Number: 2, Monitored: true,
				Statistics: series.Statistics{
					EpisodeCount:     10,
					EpisodeFileCount: 10,
					Total:            10,
					Aired:            10,
					SizeOnDisk:       11_000_000_000,
				},
			},
		},
	}

	_, err := scan.SyncSeriesFromSonarr(ctx, f.deps, "homelab", scan.SonarrPayloadBundle{Series: p})
	require.NoError(t, err)
	require.Equal(t, 2, f.countTable(t, "season_stats"))

	repo := catalogpersistence.NewSeasonStatsRepository(f.db)
	got, err := repo.ListBySeries(ctx, "homelab", 140)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, 11, got[0].EpisodeFileCount)
	require.Equal(t, 11, got[0].TotalEpisodeCount)
	require.Equal(t, 11, got[0].AiredEpisodeCount)
	require.Equal(t, int64(12_000_000_000), got[0].SizeOnDiskBytes)
	require.Equal(t, 10, got[1].EpisodeFileCount)
}

// TestSyncSeriesFromSonarr_SeasonStats_PartialPack — story 377 covers
// the "aired-but-not-on-disk" delta the handler will clamp into
// MissingCount. Aired=10, on disk=8 → expected missing=2.
func TestSyncSeriesFromSonarr_SeasonStats_PartialPack(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	p := sonarr.SeriesPayload{
		ID: 369, Title: "FROM", TitleSlug: "from",
		Monitored: true,
		Seasons: []series.Season{
			{
				Number: 4, Monitored: true,
				Statistics: series.Statistics{
					EpisodeCount:     8,
					EpisodeFileCount: 8,
					Total:            10,
					Aired:            8,
					SizeOnDisk:       9_000_000_000,
				},
			},
		},
	}
	_, err := scan.SyncSeriesFromSonarr(ctx, f.deps, "homelab", scan.SonarrPayloadBundle{Series: p})
	require.NoError(t, err)
	repo := catalogpersistence.NewSeasonStatsRepository(f.db)
	got, err := repo.ListBySeries(ctx, "homelab", 369)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 4, got[0].SeasonNumber)
	assert.Equal(t, 8, got[0].EpisodeFileCount)
	assert.Equal(t, 8, got[0].AiredEpisodeCount)
	assert.Equal(t, 10, got[0].TotalEpisodeCount)
}
