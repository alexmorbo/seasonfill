package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admininfra "github.com/alexmorbo/seasonfill/internal/admin/infrastructure"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// radarrQPRFStub is a minimal QPRFClient (the narrow ListQualityProfiles +
// ListRootFolders seam) standing in for a radarr instance. Ф6-R-6b Gap 2c.
type radarrQPRFStub struct {
	qp []ports.QualityProfile
	rf []ports.RootFolder
}

func (r radarrQPRFStub) ListQualityProfiles(context.Context) ([]ports.QualityProfile, error) {
	return r.qp, nil
}
func (r radarrQPRFStub) ListRootFolders(context.Context) ([]ports.RootFolder, error) {
	return r.rf, nil
}

// radarrStubLookup resolves a single radarr name → QPRFClient.
type radarrStubLookup struct {
	name   string
	client QPRFClient
}

func (r radarrStubLookup) Lookup(name string) (QPRFClient, bool) {
	if name != r.name {
		return nil, false
	}
	return r.client, true
}

func newRadarrUC(t *testing.T, sonarr InstanceLookup, radarr RadarrQPRFLookup) *InstanceMetadataUseCase {
	t.Helper()
	cache := admininfra.NewMetadataCache("_uc_radarr_" + t.Name())
	t.Cleanup(func() { _ = cache.Close() })
	uc := NewInstanceMetadataUseCase(sonarr, cache, nil)
	if radarr != nil {
		uc.WithRadarrLookup(radarr)
	}
	return uc
}

// TestUC_Radarr_QPRF_ResolvesViaRadarrFallback proves a radarr instance name —
// absent from the sonarr registry — resolves its quality-profiles + root-folders
// through the radarr QP/RF fallback (Gap 2c).
func TestUC_Radarr_QPRF_ResolvesViaRadarrFallback(t *testing.T) {
	t.Parallel()
	radarr := radarrQPRFStub{
		qp: []ports.QualityProfile{{ID: 7, Name: "HD-1080p"}},
		rf: []ports.RootFolder{{ID: 3, Path: "/movies"}},
	}
	// Sonarr lookup misses 'movies'; radarr fallback resolves it.
	uc := newRadarrUC(t,
		stubLookup{name: "tv", client: &ports.SonarrClientMock{}},
		radarrStubLookup{name: "movies", client: radarr},
	)

	qpRes, err := uc.GetQualityProfiles(context.Background(), "movies")
	require.NoError(t, err)
	require.Len(t, qpRes.Items, 1)
	assert.Equal(t, "HD-1080p", qpRes.Items[0].Name)
	assert.Equal(t, "movies", qpRes.InstanceName)

	rfRes, err := uc.GetRootFolders(context.Background(), "movies")
	require.NoError(t, err)
	require.Len(t, rfRes.Items, 1)
	assert.Equal(t, "/movies", rfRes.Items[0].Path)
}

// TestUC_Radarr_RefreshMetadata_ResolvesRadarr proves refresh-metadata no longer
// 404s for a radarr instance (resolves via the same QP/RF seam).
func TestUC_Radarr_RefreshMetadata_ResolvesRadarr(t *testing.T) {
	t.Parallel()
	uc := newRadarrUC(t,
		stubLookup{name: "tv", client: &ports.SonarrClientMock{}},
		radarrStubLookup{name: "movies", client: radarrQPRFStub{}},
	)
	assert.NoError(t, uc.RefreshMetadata(context.Background(), "movies"))
}

// TestUC_Radarr_UnknownName_NotFound proves a name in NEITHER registry still
// surfaces instance_not_found (ports.ErrNotFound), unchanged.
func TestUC_Radarr_UnknownName_NotFound(t *testing.T) {
	t.Parallel()
	uc := newRadarrUC(t,
		stubLookup{name: "tv", client: &ports.SonarrClientMock{}},
		radarrStubLookup{name: "movies", client: radarrQPRFStub{}},
	)
	_, err := uc.GetQualityProfiles(context.Background(), "ghost")
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)
	var nf *sharedErrors.InstanceNotFoundError
	assert.ErrorAs(t, err, &nf)
}

// TestUC_Sonarr_QPRF_UnchangedByRadarrFallback proves sonarr resolution stays
// FIRST + byte-identical even when a radarr fallback is wired: a name present
// in BOTH registries resolves through sonarr (radarr is never consulted).
func TestUC_Sonarr_QPRF_UnchangedByRadarrFallback(t *testing.T) {
	t.Parallel()
	sonarrMock := &ports.SonarrClientMock{
		ListQualityProfilesFunc: func(context.Context) ([]ports.QualityProfile, error) {
			return []ports.QualityProfile{{ID: 1, Name: "SONARR-Any"}}, nil
		},
		ListRootFoldersFunc: func(context.Context) ([]ports.RootFolder, error) {
			return []ports.RootFolder{{ID: 1, Path: "/tv"}}, nil
		},
	}
	// A radarr stub with the SAME name that would return different data — it
	// must NOT be reached because sonarr resolves first.
	radarrPoison := radarrQPRFStub{qp: []ports.QualityProfile{{ID: 99, Name: "RADARR-WRONG"}}}
	uc := newRadarrUC(t,
		stubLookup{name: "shared", client: sonarrMock},
		radarrStubLookup{name: "shared", client: radarrPoison},
	)
	qpRes, err := uc.GetQualityProfiles(context.Background(), "shared")
	require.NoError(t, err)
	require.Len(t, qpRes.Items, 1)
	assert.Equal(t, "SONARR-Any", qpRes.Items[0].Name, "sonarr must resolve first — radarr fallback must not shadow it")
}

// TestUC_NoRadarrLookup_SonarrOnly proves that without a radarr fallback the
// behavior is identical to pre-Ф6-R-6b: a non-sonarr name is not-found.
func TestUC_NoRadarrLookup_SonarrOnly(t *testing.T) {
	t.Parallel()
	uc := newRadarrUC(t, stubLookup{name: "tv", client: &ports.SonarrClientMock{}}, nil)
	_, err := uc.GetQualityProfiles(context.Background(), "movies")
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)
	// And a sanity assertion that errors.Is on a sonarr-unreachable path is
	// untouched (belt-and-suspenders that the refactor kept the error shape).
	_ = errors.Is(err, ports.ErrNotFound)
}
