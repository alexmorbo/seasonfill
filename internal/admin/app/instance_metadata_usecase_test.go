package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admininfra "github.com/alexmorbo/seasonfill/internal/admin/infrastructure"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

type stubLookup struct {
	name   string
	id     int64
	client ports.SonarrClient
}

func (s stubLookup) Lookup(name string) (int64, ports.SonarrClient, bool) {
	if name != s.name {
		return 0, nil, false
	}
	return s.id, s.client, true
}

type metadataMock struct {
	*ports.SonarrClientMock
	qpCalls atomic.Int32
	rfCalls atomic.Int32

	qpItems []ports.QualityProfile
	rfItems []ports.RootFolder
	qpErr   error
	rfErr   error
}

func newMetadataMock() *metadataMock {
	m := &metadataMock{}
	m.SonarrClientMock = &ports.SonarrClientMock{
		ListQualityProfilesFunc: func(_ context.Context) ([]ports.QualityProfile, error) {
			m.qpCalls.Add(1)
			return m.qpItems, m.qpErr
		},
		ListRootFoldersFunc: func(_ context.Context) ([]ports.RootFolder, error) {
			m.rfCalls.Add(1)
			return m.rfItems, m.rfErr
		},
	}
	return m
}

func newUC(t *testing.T, mock *metadataMock) *InstanceMetadataUseCase {
	t.Helper()
	cache := admininfra.NewMetadataCache("_uc_" + t.Name())
	t.Cleanup(func() { _ = cache.Close() })
	return NewInstanceMetadataUseCase(stubLookup{name: "main", id: 7, client: mock}, cache, nil)
}

func TestUC_QualityProfiles_MissThenHit(t *testing.T) {
	t.Parallel()
	mock := newMetadataMock()
	mock.qpItems = []ports.QualityProfile{{ID: 1, Name: "Any"}, {ID: 2, Name: "HD-1080p"}}
	uc := newUC(t, mock)

	res, err := uc.GetQualityProfiles(context.Background(), "main")
	require.NoError(t, err)
	assert.Equal(t, CacheStatusMiss, res.CacheStatus)
	assert.Equal(t, "main", res.InstanceName)
	assert.Len(t, res.Items, 2)

	res2, err := uc.GetQualityProfiles(context.Background(), "main")
	require.NoError(t, err)
	assert.Equal(t, CacheStatusHit, res2.CacheStatus)
	assert.Equal(t, int32(1), mock.qpCalls.Load(),
		"second call MUST be served from cache")
}

func TestUC_RootFolders_MissThenHit(t *testing.T) {
	t.Parallel()
	mock := newMetadataMock()
	mock.rfItems = []ports.RootFolder{{ID: 1, Path: "/tv", Accessible: true, FreeSpace: 1 << 40}}
	uc := newUC(t, mock)

	res, err := uc.GetRootFolders(context.Background(), "main")
	require.NoError(t, err)
	assert.Equal(t, CacheStatusMiss, res.CacheStatus)
	assert.True(t, res.Items[0].Accessible)

	res2, err := uc.GetRootFolders(context.Background(), "main")
	require.NoError(t, err)
	assert.Equal(t, CacheStatusHit, res2.CacheStatus)
	assert.Equal(t, int32(1), mock.rfCalls.Load())
}

func TestUC_RefreshMetadata_ForcesRefetch(t *testing.T) {
	t.Parallel()
	mock := newMetadataMock()
	mock.qpItems = []ports.QualityProfile{{ID: 1, Name: "Any"}}
	mock.rfItems = []ports.RootFolder{{ID: 1, Path: "/tv"}}
	uc := newUC(t, mock)

	_, _ = uc.GetQualityProfiles(context.Background(), "main")
	_, _ = uc.GetRootFolders(context.Background(), "main")
	require.NoError(t, uc.RefreshMetadata(context.Background(), "main"))

	res, err := uc.GetQualityProfiles(context.Background(), "main")
	require.NoError(t, err)
	assert.Equal(t, CacheStatusMiss, res.CacheStatus)
	assert.Equal(t, int32(2), mock.qpCalls.Load())

	res2, err := uc.GetRootFolders(context.Background(), "main")
	require.NoError(t, err)
	assert.Equal(t, CacheStatusMiss, res2.CacheStatus)
	assert.Equal(t, int32(2), mock.rfCalls.Load())
}

func TestUC_InstanceNotFound(t *testing.T) {
	t.Parallel()
	uc := newUC(t, newMetadataMock())
	var typed *sharedErrors.InstanceNotFoundError

	_, err := uc.GetQualityProfiles(context.Background(), "ghost")
	require.Error(t, err)
	assert.True(t, errors.As(err, &typed))
	assert.True(t, errors.Is(err, ports.ErrNotFound))

	_, err = uc.GetRootFolders(context.Background(), "ghost")
	assert.True(t, errors.As(err, &typed))

	assert.True(t, errors.As(uc.RefreshMetadata(context.Background(), "ghost"), &typed))
}

func TestUC_SonarrUnreachable_NoCachePoison(t *testing.T) {
	t.Parallel()
	mock := newMetadataMock()
	mock.qpErr = errors.New("dial tcp: connection refused")
	mock.rfErr = errors.New("upstream 502")
	uc := newUC(t, mock)

	var typed *sharedErrors.SonarrUnreachableError
	_, err := uc.GetQualityProfiles(context.Background(), "main")
	require.Error(t, err)
	assert.True(t, errors.As(err, &typed))
	assert.Contains(t, err.Error(), "connection refused")

	_, err = uc.GetRootFolders(context.Background(), "main")
	assert.True(t, errors.As(err, &typed))

	// Cache MUST stay empty so a subsequent successful response surfaces.
	mock.qpErr = nil
	mock.qpItems = []ports.QualityProfile{{ID: 1, Name: "OK"}}
	res, err := uc.GetQualityProfiles(context.Background(), "main")
	require.NoError(t, err)
	assert.Equal(t, CacheStatusMiss, res.CacheStatus,
		"failed fetch must NOT populate the cache")
}

// stubSeasonsResolver records the (tvdb_id, tmdb_hint) tuple it is
// called with and returns a pinned ports.SeasonInfo slice + error.
// Story 525 — covers the TMDB-first override path.
type stubSeasonsResolver struct {
	gotTVDB int
	gotTMDB int
	out     []ports.SeasonInfo
	err     error
	calls   atomic.Int32
}

func (r *stubSeasonsResolver) ResolveSeasons(_ context.Context, tvdbID, tmdbHint int) ([]ports.SeasonInfo, error) {
	r.calls.Add(1)
	r.gotTVDB = tvdbID
	r.gotTMDB = tmdbHint
	return r.out, r.err
}

// TestUC_LookupSeries_OverridesEpisodeCount asserts that when the
// SeasonsResolver returns rows, the Sonarr-supplied seasons are
// replaced with the authoritative TMDB / catalog data. Sonarr's
// per-season `monitored` flag is preserved.
func TestUC_LookupSeries_OverridesEpisodeCount(t *testing.T) {
	t.Parallel()
	mock := newMetadataMock()
	mock.LookupSeriesFunc = func(_ context.Context, term string) ([]ports.SonarrLookupResult, error) {
		assert.Equal(t, "tvdb:42", term)
		return []ports.SonarrLookupResult{{
			Title: "Test Show", Year: 2024, TVDBID: 42, TMDBID: 1001,
			Seasons: []ports.SeasonInfo{
				{SeasonNumber: 0, EpisodeCount: 0, Monitored: false},
				{SeasonNumber: 1, EpisodeCount: 0, Monitored: true},
				{SeasonNumber: 2, EpisodeCount: 0, Monitored: true},
			},
		}}, nil
	}
	resolver := &stubSeasonsResolver{out: []ports.SeasonInfo{
		{SeasonNumber: 0, EpisodeCount: 3},
		{SeasonNumber: 1, EpisodeCount: 11},
		{SeasonNumber: 2, EpisodeCount: 8},
	}}
	uc := newUC(t, mock).WithSeasonsResolver(resolver)

	res, err := uc.LookupSeries(context.Background(), "main", 42)
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	got := res.Items[0].Seasons
	require.Len(t, got, 3)
	assert.Equal(t, 3, got[0].EpisodeCount, "season 0 episode_count comes from resolver")
	assert.Equal(t, 11, got[1].EpisodeCount)
	assert.Equal(t, 8, got[2].EpisodeCount)
	assert.False(t, got[0].Monitored, "monitored preserved from sonarr (S0 specials)")
	assert.True(t, got[1].Monitored, "monitored preserved from sonarr")
	assert.True(t, got[2].Monitored)
	assert.Equal(t, 42, resolver.gotTVDB)
	assert.Equal(t, 1001, resolver.gotTMDB, "tmdb hint forwarded from sonarr lookup")
	assert.Equal(t, int32(1), resolver.calls.Load())
}

// TestUC_LookupSeries_ResolverEmpty keeps Sonarr seasons when the
// resolver returns an empty slice — best-effort fallback.
func TestUC_LookupSeries_ResolverEmpty(t *testing.T) {
	t.Parallel()
	mock := newMetadataMock()
	mock.LookupSeriesFunc = func(_ context.Context, _ string) ([]ports.SonarrLookupResult, error) {
		return []ports.SonarrLookupResult{{
			Title: "Sonarr-Only", TVDBID: 5,
			Seasons: []ports.SeasonInfo{
				{SeasonNumber: 1, EpisodeCount: 6, Monitored: true},
			},
		}}, nil
	}
	uc := newUC(t, mock).WithSeasonsResolver(&stubSeasonsResolver{out: nil})

	res, err := uc.LookupSeries(context.Background(), "main", 5)
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	assert.Equal(t, 6, res.Items[0].Seasons[0].EpisodeCount,
		"empty resolver result MUST leave sonarr seasons unchanged")
}

// TestUC_LookupSeries_ResolverError swallows resolver errors and keeps
// the Sonarr seasons — the modal MUST still render even if TMDB is
// offline.
func TestUC_LookupSeries_ResolverError(t *testing.T) {
	t.Parallel()
	mock := newMetadataMock()
	mock.LookupSeriesFunc = func(_ context.Context, _ string) ([]ports.SonarrLookupResult, error) {
		return []ports.SonarrLookupResult{{
			Title: "Resilient", TVDBID: 7,
			Seasons: []ports.SeasonInfo{
				{SeasonNumber: 1, EpisodeCount: 4, Monitored: true},
			},
		}}, nil
	}
	uc := newUC(t, mock).WithSeasonsResolver(&stubSeasonsResolver{err: errors.New("tmdb 502")})

	res, err := uc.LookupSeries(context.Background(), "main", 7)
	require.NoError(t, err, "resolver error MUST NOT bubble — lookup is best-effort")
	assert.Equal(t, 4, res.Items[0].Seasons[0].EpisodeCount)
}

// TestUC_LookupSeries_ResolverAddsTMDBOnlySeasons asserts that when
// the resolver returns a season number Sonarr did not, the merge
// defaults monitored to true for non-specials and false for S0.
func TestUC_LookupSeries_ResolverAddsTMDBOnlySeasons(t *testing.T) {
	t.Parallel()
	mock := newMetadataMock()
	mock.LookupSeriesFunc = func(_ context.Context, _ string) ([]ports.SonarrLookupResult, error) {
		return []ports.SonarrLookupResult{{
			Title: "Sparse", TVDBID: 9,
			// Sonarr returns nothing (stub series): no seasons at all.
			Seasons: nil,
		}}, nil
	}
	uc := newUC(t, mock).WithSeasonsResolver(&stubSeasonsResolver{out: []ports.SeasonInfo{
		{SeasonNumber: 0, EpisodeCount: 2},
		{SeasonNumber: 1, EpisodeCount: 10},
	}})

	res, err := uc.LookupSeries(context.Background(), "main", 9)
	require.NoError(t, err)
	got := res.Items[0].Seasons
	require.Len(t, got, 2)
	assert.False(t, got[0].Monitored, "specials default to unmonitored")
	assert.True(t, got[1].Monitored, "regular season defaults to monitored")
}

func TestUC_Clock_Override(t *testing.T) {
	t.Parallel()
	frozen := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	cache := admininfra.NewMetadataCache("_uc_clock_" + t.Name())
	t.Cleanup(func() { _ = cache.Close() })
	uc := NewInstanceMetadataUseCase(
		stubLookup{name: "main", id: 3, client: newMetadataMock()},
		cache,
		func() time.Time { return frozen },
	)
	res, err := uc.GetQualityProfiles(context.Background(), "main")
	require.NoError(t, err)
	assert.Equal(t, frozen, res.RefreshedAt)
}
