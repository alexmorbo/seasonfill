package scan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func newDerivationDeps(t *testing.T) (SyncDeps, *catalogpersistence.EpisodeStatesRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	seriesRepo := enrichpersistence.NewSeriesRepository(db)
	statesRepo := catalogpersistence.NewEpisodeStatesRepository(db)
	deps := SyncDeps{
		Series:        seriesRepo,
		SeriesCache:   catalogpersistence.NewSeriesCacheRepository(db, seriesRepo),
		Episodes:      enrichpersistence.NewEpisodesRepository(db),
		EpisodeStates: statesRepo,
		EpisodeTexts:  enrichpersistence.NewEpisodeTextsRepository(db),
		SeasonStats:   catalogpersistence.NewSeasonStatsRepository(db),
		Genres:        NewGenresAdapter(enrichpersistence.NewGenresRepository(db), enrichpersistence.NewGenresI18nRepository(db)),
		Networks:      NewNetworksAdapter(enrichpersistence.NewNetworksRepository(db)),
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	return deps, statesRepo
}

func derivSeriesPayload() sonarr.SeriesPayload {
	return sonarr.SeriesPayload{ID: 140, TVDBID: 375903, Title: "Rick and Morty", TitleSlug: "rick-and-morty", Monitored: true}
}

// resolveCanonID looks up the canonical series_id for the seeded payload so
// the assertions don't hardcode the sqlite sequence value.
func resolveCanonID(t *testing.T, deps SyncDeps, sp sonarr.SeriesPayload) domain.SeriesID {
	t.Helper()
	tvdb := sp.TVDBID
	canon, err := deps.Series.FindByExternalIDs(context.Background(), nil, &tvdb, nil)
	require.NoError(t, err)
	require.NotZero(t, canon.ID)
	return canon.ID
}

// Seeds canon series + episodes + initial states (HasFile=false) via the full
// sync, then exercises the light piggyback path and asserts HasFile flips and
// rich metadata lands WITHOUT a re-add.
func TestRefreshEpisodeStatesFromBundle_FlipsHasFile_And_PreservesRichMeta(t *testing.T) {
	ctx := context.Background()
	deps, states := newDerivationDeps(t)
	sp := derivSeriesPayload()

	// initial full sync: S1E1 missing.
	_, err := SyncSeriesFromSonarr(ctx, deps, "homelab", SonarrPayloadBundle{
		Series:   sp,
		Episodes: []sonarr.EpisodePayload{{ID: 1001, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot", Monitored: true, HasFile: false}},
	})
	require.NoError(t, err)

	// operator downloads S1E1 -> now HasFile=true with a file + rich metadata.
	bundleEpisodes := []sonarr.EpisodePayload{{ID: 1001, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot", Monitored: true, HasFile: true, EpisodeFileID: 9001}}
	bundleFiles := []sonarr.EpisodeFilePayload{{ID: 9001, SeasonNumber: 1, QualityName: "WEBDL-1080p", SizeBytes: 1234, VideoCodec: "x264", AudioCodec: "AAC", AudioChannels: "5.1", ReleaseGroup: "GRP"}}

	require.NoError(t, refreshEpisodeStatesFromBundle(ctx, deps, "homelab", sp, bundleEpisodes, bundleFiles, deps.Logger))

	seriesID := resolveCanonID(t, deps, sp)
	rows, err := states.ListBySeries(ctx, "homelab", seriesID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].HasFile)
	require.NotNil(t, rows[0].Quality)
	assert.Equal(t, "WEBDL-1080p", *rows[0].Quality)
	require.NotNil(t, rows[0].SizeBytes)
	assert.Equal(t, int64(1234), *rows[0].SizeBytes)
	require.NotNil(t, rows[0].ReleaseGroup)
	assert.Equal(t, "GRP", *rows[0].ReleaseGroup)
}

// Regression: a delete flips HasFile back to false without a scan/re-add.
func TestRefreshEpisodeStatesFromBundle_FileDelete_FlipsHasFileFalse(t *testing.T) {
	ctx := context.Background()
	deps, states := newDerivationDeps(t)
	sp := derivSeriesPayload()
	_, err := SyncSeriesFromSonarr(ctx, deps, "homelab", SonarrPayloadBundle{
		Series:       sp,
		Episodes:     []sonarr.EpisodePayload{{ID: 1001, SeasonNumber: 1, EpisodeNumber: 1, Monitored: true, HasFile: true, EpisodeFileID: 9001}},
		EpisodeFiles: []sonarr.EpisodeFilePayload{{ID: 9001, SeasonNumber: 1, QualityName: "WEBDL-1080p", SizeBytes: 1234}},
	})
	require.NoError(t, err)

	// file removed -> HasFile false, EpisodeFileID 0, no file payload.
	require.NoError(t, refreshEpisodeStatesFromBundle(ctx, deps, "homelab", sp,
		[]sonarr.EpisodePayload{{ID: 1001, SeasonNumber: 1, EpisodeNumber: 1, Monitored: true, HasFile: false}}, nil, deps.Logger))

	seriesID := resolveCanonID(t, deps, sp)
	rows, err := states.ListBySeries(ctx, "homelab", seriesID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.False(t, rows[0].HasFile)
	assert.Nil(t, rows[0].EpisodeFileID)
	assert.Nil(t, rows[0].Quality) // straight-assign cleared it (single writer, full fidelity)
}

// Unresolved series (never synced) -> no episode_states, no error.
func TestRefreshEpisodeStatesFromBundle_UnknownSeries_NoOp(t *testing.T) {
	ctx := context.Background()
	deps, _ := newDerivationDeps(t)
	err := refreshEpisodeStatesFromBundle(ctx, deps, "homelab", derivSeriesPayload(),
		[]sonarr.EpisodePayload{{ID: 1001, SeasonNumber: 1, EpisodeNumber: 1, HasFile: true}}, nil, deps.Logger)
	require.NoError(t, err)
}

// NULL/error pair: repo Upsert error surfaces from the derivation.
func TestUpsertEpisodeStates_UpsertError_Propagates(t *testing.T) {
	ctx := context.Background()
	fake := &errEpisodeStates{err: errors.New("boom")}
	err := upsertEpisodeStates(ctx, fake, "homelab",
		[]sonarr.EpisodePayload{{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, HasFile: true}},
		[]int64{42}, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	require.Error(t, err)
}

type errEpisodeStates struct{ err error }

func (e *errEpisodeStates) Upsert(context.Context, series.EpisodeState) error { return e.err }

var _ EpisodeStatesRepository = (*errEpisodeStates)(nil)
