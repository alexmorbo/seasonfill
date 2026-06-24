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
