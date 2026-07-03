package rest

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// --- minimal port fakes (mirror composer_test.go but inline) ---

type fakeCachePort struct {
	entries map[string]series.CacheEntry
	byCanon map[domain.SeriesID][]series.CacheEntry
}

func (f *fakeCachePort) Get(_ context.Context, instance domain.InstanceName, sonarrID domain.SonarrSeriesID) (series.CacheEntry, error) {
	k := string(instance) + "|" + itoa(int(sonarrID))
	e, ok := f.entries[k]
	if !ok {
		return series.CacheEntry{}, ports.ErrNotFound
	}
	return e, nil
}

func (f *fakeCachePort) ListBySeriesID(_ context.Context, id domain.SeriesID) ([]series.CacheEntry, error) {
	return f.byCanon[id], nil
}

func (f *fakeCachePort) ListBySeriesIDs(_ context.Context, ids []domain.SeriesID) (map[domain.SeriesID][]series.CacheEntry, error) {
	out := make(map[domain.SeriesID][]series.CacheEntry, len(ids))
	for _, id := range ids {
		if rows, ok := f.byCanon[id]; ok && len(rows) > 0 {
			out[id] = rows
		}
	}
	return out, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	out := ""
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	if neg {
		out = "-" + out
	}
	return out
}

type fakeSeriesPort struct {
	rows map[domain.SeriesID]series.Canon
}

func (f *fakeSeriesPort) Get(_ context.Context, id domain.SeriesID) (series.Canon, error) {
	c, ok := f.rows[id]
	if !ok {
		return series.Canon{}, ports.ErrNotFound
	}
	return c, nil
}

func (f *fakeSeriesPort) GetByTMDBID(_ context.Context, tmdbID domain.TMDBID) (series.Canon, error) {
	for _, c := range f.rows {
		if c.TMDBID != nil && *c.TMDBID == tmdbID {
			return c, nil
		}
	}
	return series.Canon{}, ports.ErrNotFound
}

func (f *fakeSeriesPort) ListByIDs(_ context.Context, ids []domain.SeriesID) ([]series.Canon, error) {
	out := make([]series.Canon, 0, len(ids))
	for _, id := range ids {
		if c, ok := f.rows[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeSeriesPort) ListByTMDBIDs(_ context.Context, tmdbIDs []domain.TMDBID) ([]series.Canon, error) {
	out := make([]series.Canon, 0, len(tmdbIDs))
	for _, id := range tmdbIDs {
		for _, c := range f.rows {
			if c.TMDBID != nil && *c.TMDBID == id {
				out = append(out, c)
				break
			}
		}
	}
	return out, nil
}

type fakeNoTexts struct{}

func (fakeNoTexts) GetWithFallback(_ context.Context, _ domain.SeriesID, _ string) (series.SeriesText, error) {
	return series.SeriesText{}, ports.ErrNotFound
}

func (fakeNoTexts) ListByIDsWithFallback(_ context.Context, _ []domain.SeriesID, _ string) (map[domain.SeriesID]series.SeriesText, error) {
	return map[domain.SeriesID]series.SeriesText{}, nil
}

type fakeNoEpTexts struct{}

func (fakeNoEpTexts) GetWithFallback(_ context.Context, _ domain.EpisodeID, _ string) (series.EpisodeText, error) {
	return series.EpisodeText{}, ports.ErrNotFound
}

func (fakeNoEpTexts) ListByEpisodeIDsWithFallback(_ context.Context, _ []domain.EpisodeID, _ string) (map[domain.EpisodeID]series.EpisodeText, error) {
	return map[domain.EpisodeID]series.EpisodeText{}, nil
}

type emptyList struct{}

func (emptyList) ListBySeries(_ context.Context, _ domain.SeriesID) ([]series.CanonSeason, error) {
	return nil, nil
}

type emptyEpisodes struct{}

func (emptyEpisodes) ListBySeries(_ context.Context, _ domain.SeriesID) ([]series.CanonEpisode, error) {
	return nil, nil
}

type emptyStates struct{}

func (emptyStates) ListBySeries(_ context.Context, _ domain.InstanceName, _ domain.SeriesID) ([]series.EpisodeState, error) {
	return nil, nil
}

type emptyPeople struct{}

func (emptyPeople) ListBySeries(_ context.Context, _ domain.SeriesID, _ people.SeriesCreditKind, _ string) ([]people.SeriesCredit, error) {
	return nil, nil
}
func (emptyPeople) ListByIDs(_ context.Context, _ []int64) ([]people.Person, error) {
	return nil, nil
}

type emptyTaxRefs struct{}

func (emptyTaxRefs) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return nil, nil
}
func (emptyTaxRefs) Get(_ context.Context, id int64, lang string) (taxonomy.Genre, error) {
	return taxonomy.Genre{ID: id, Language: lang}, nil
}
func (emptyTaxRefs) ListByIDsWithFallback(_ context.Context, ids []int64, lang string) ([]taxonomy.Genre, error) {
	out := make([]taxonomy.Genre, 0, len(ids))
	for _, id := range ids {
		out = append(out, taxonomy.Genre{ID: id, Language: lang})
	}
	return out, nil
}

type emptyKwRefs struct{}

func (emptyKwRefs) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return nil, nil
}
func (emptyKwRefs) Get(_ context.Context, id int64, lang string) (taxonomy.Keyword, error) {
	return taxonomy.Keyword{ID: id, Language: lang}, nil
}
func (emptyKwRefs) ListByIDsWithFallback(_ context.Context, ids []int64, lang string) ([]taxonomy.Keyword, error) {
	out := make([]taxonomy.Keyword, 0, len(ids))
	for _, id := range ids {
		out = append(out, taxonomy.Keyword{ID: id, Language: lang})
	}
	return out, nil
}

type emptyNetCo struct{}

func (emptyNetCo) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return nil, nil
}
func (emptyNetCo) ListByIDs(_ context.Context, _ []int64) ([]taxonomy.Network, error) {
	return nil, nil
}

type emptyCompanies struct{}

func (emptyCompanies) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return nil, nil
}
func (emptyCompanies) ListByIDs(_ context.Context, _ []int64) ([]taxonomy.ProductionCompany, error) {
	return nil, nil
}

type emptyVideos struct{}

func (emptyVideos) ListBySeriesAndType(_ context.Context, _ domain.SeriesID, _ string) ([]database.VideoModel, error) {
	return nil, nil
}

type emptyRatings struct{}

func (emptyRatings) ListBySeries(_ context.Context, _ domain.SeriesID) ([]database.ContentRatingModel, error) {
	return nil, nil
}

type emptyExtIDs struct{}

func (emptyExtIDs) ListByEntity(_ context.Context, _ enrichment.EntityType, _ int64) ([]database.ExternalIDModel, error) {
	return nil, nil
}

type emptyRecs struct{}

func (emptyRecs) ListBySeries(_ context.Context, _ domain.SeriesID) ([]domain.SeriesID, error) {
	return nil, nil
}

// emptyFreshness — 464b: a freshness adapter that returns "no
// synced_at, no error rows" for every entity. Composer treats both
// nil maps as rule-1 ("never synced") for any source the caller
// declares via SyncedAt — handler tests don't drive the degraded[]
// path here, so the empty input is the cleanest seed.
type emptyFreshness struct{}

func (emptyFreshness) SyncedAtFor(_ context.Context, _ domain.SeriesID, _ enrichment.Source) (*time.Time, error) {
	return nil, nil
}

func (emptyFreshness) ErrorsFor(_ context.Context, _ domain.SeriesID) ([]enrichment.EnrichmentError, error) {
	return nil, nil
}

func i64p(v int64) *domain.SeriesID { sid := domain.SeriesID(v); return &sid }

func newComposerForHandlerTest(canon series.Canon, cacheEntries map[string]series.CacheEntry) *seriesdetail.Composer {
	return seriesdetail.NewComposer(seriesdetail.Deps{
		SeriesCache:       &fakeCachePort{entries: cacheEntries, byCanon: map[domain.SeriesID][]series.CacheEntry{}},
		SeriesCacheLookup: &fakeCachePort{entries: cacheEntries, byCanon: map[domain.SeriesID][]series.CacheEntry{}},
		Series:            &fakeSeriesPort{rows: map[domain.SeriesID]series.Canon{canon.ID: canon}},
		SeriesTexts:       fakeNoTexts{},
		Seasons:           emptyList{},
		Episodes:          emptyEpisodes{},
		EpisodeStates:     emptyStates{},
		EpisodeTexts:      fakeNoEpTexts{},
		SeriesPeople:      emptyPeople{},
		People:            emptyPeople{},
		Genres:            emptyTaxRefs{},
		Keywords:          emptyKwRefs{},
		Networks:          emptyNetCo{},
		Companies:         emptyCompanies{},
		Videos:            emptyVideos{},
		ContentRatings:    emptyRatings{},
		ExternalIDs:       emptyExtIDs{},
		Recommendations:   emptyRecs{},
		Freshness:         emptyFreshness{},
		SonarrFor: func(_ domain.InstanceName) (seriesdetail.SonarrQueueLister, bool) {
			return fakeSonarrQ{}, true
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Now().UTC() },
	})
}

type fakeSonarrQ struct{}

func (fakeSonarrQ) Queue(_ context.Context, _ domain.SonarrSeriesID) (sonarr.QueuePayload, error) {
	return sonarr.QueuePayload{}, nil
}

// --- tests ---

func TestMapSeasons_PopulatesMediaMeta(t *testing.T) {
	t.Parallel()
	vc, ac, ach, rg := "HEVC", "DDP", "5.1", "RARBG"
	qn := "WEBDL-1080p"
	seasons := []seriesdetail.SeasonDetail{{
		Canon: series.CanonSeason{SeasonNumber: 5},
		Episodes: []seriesdetail.EpisodeDetail{{
			Canon: series.CanonEpisode{EpisodeNumber: 1, SeasonNumber: 5},
			State: &series.EpisodeState{
				HasFile:       true,
				Quality:       &qn,
				VideoCodec:    &vc,
				AudioCodec:    &ac,
				AudioChannels: &ach,
				ReleaseGroup:  &rg,
			},
		}},
	}}
	out := mapSeasons(&seriesdetail.Detail{Seasons: seasons})
	require.Len(t, out, 1)
	require.Len(t, out[0].Episodes, 1)
	ep := out[0].Episodes[0]
	require.Equal(t, &vc, ep.VideoCodec)
	require.Equal(t, &ac, ep.AudioCodec)
	require.Equal(t, &ach, ep.AudioChannels)
	require.Equal(t, &rg, ep.ReleaseGroup)
	require.Equal(t, &qn, ep.Quality)
}

// --- Story 377: mapSeasons SeasonStats branches ---

func TestMapSeasons_PrefersStatsOverEpisodeWalk(t *testing.T) {
	t.Parallel()
	stats := &series.SeasonStat{
		SeasonNumber:      1,
		EpisodeFileCount:  10,
		AiredEpisodeCount: 10,
		TotalEpisodeCount: 10,
		Monitored:         true,
	}
	d := []seriesdetail.SeasonDetail{{
		Canon: series.CanonSeason{SeasonNumber: 1},
		Stats: stats,
		// Episode walk would yield 0 on disk if we were reading State;
		// the stats branch must win.
		Episodes: []seriesdetail.EpisodeDetail{
			{Canon: series.CanonEpisode{EpisodeNumber: 1, SeasonNumber: 1}},
			{Canon: series.CanonEpisode{EpisodeNumber: 2, SeasonNumber: 1}},
		},
	}}
	out := mapSeasons(&seriesdetail.Detail{Seasons: d})
	require.Len(t, out, 1)
	require.Equal(t, 10, out[0].OnDiskCount)
	require.Equal(t, 0, out[0].MissingCount)
	require.Equal(t, 10, out[0].EpisodeCount)
	require.True(t, out[0].Monitored)
}

func TestMapSeasons_ClampsMissingNegative(t *testing.T) {
	t.Parallel()
	stats := &series.SeasonStat{
		SeasonNumber:      1,
		EpisodeFileCount:  12,
		AiredEpisodeCount: 10,
		TotalEpisodeCount: 10,
	}
	d := []seriesdetail.SeasonDetail{{
		Canon: series.CanonSeason{SeasonNumber: 1},
		Stats: stats,
	}}
	out := mapSeasons(&seriesdetail.Detail{Seasons: d})
	require.Len(t, out, 1)
	require.Equal(t, 12, out[0].OnDiskCount)
	require.Equal(t, 0, out[0].MissingCount, "missing must clamp to 0 when file_count > aired")
}

func TestMapSeasons_FallsBackToEpisodeWalkWhenStatsNil(t *testing.T) {
	t.Parallel()
	qWEB := "WEB-DL 1080p"
	d := []seriesdetail.SeasonDetail{{
		Canon: series.CanonSeason{SeasonNumber: 1},
		Episodes: []seriesdetail.EpisodeDetail{
			{
				Canon: series.CanonEpisode{EpisodeNumber: 1, SeasonNumber: 1},
				State: &series.EpisodeState{HasFile: true, Quality: &qWEB},
			},
			{
				Canon: series.CanonEpisode{EpisodeNumber: 2, SeasonNumber: 1},
				State: &series.EpisodeState{HasFile: false},
			},
			{
				Canon: series.CanonEpisode{EpisodeNumber: 3, SeasonNumber: 1},
				// no State — counts toward missing
			},
		},
	}}
	out := mapSeasons(&seriesdetail.Detail{Seasons: d})
	require.Len(t, out, 1)
	require.Equal(t, 1, out[0].OnDiskCount)
	require.Equal(t, 2, out[0].MissingCount)
	require.Equal(t, 3, out[0].EpisodeCount)
}

func TestMapSeasons_PartialPack_FROM(t *testing.T) {
	t.Parallel()
	// Acceptance smoke shape: FROM S4 with 8 aired / 10 total / 8 on disk.
	stats := &series.SeasonStat{
		SeasonNumber:      4,
		EpisodeFileCount:  8,
		AiredEpisodeCount: 8,
		TotalEpisodeCount: 10,
		Monitored:         true,
	}
	d := []seriesdetail.SeasonDetail{{
		Canon: series.CanonSeason{SeasonNumber: 4},
		Stats: stats,
	}}
	out := mapSeasons(&seriesdetail.Detail{Seasons: d})
	require.Len(t, out, 1)
	require.Equal(t, 8, out[0].OnDiskCount)
	require.Equal(t, 0, out[0].MissingCount, "all aired episodes are on disk")
	require.Equal(t, 10, out[0].EpisodeCount, "EpisodeCount must surface TotalEpisodeCount")
}
