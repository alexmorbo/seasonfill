package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	reqdomain "github.com/alexmorbo/seasonfill/internal/request/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// fakeRequestQueue records the queued spec + user for gate assertions.
type fakeRequestQueue struct {
	calls  int
	userID uint
	spec   reqdomain.AddSpec
	id     int64
	err    error
}

func (f *fakeRequestQueue) Queue(_ context.Context, userID uint, spec reqdomain.AddSpec) (int64, error) {
	f.calls++
	f.userID = userID
	f.spec = spec
	if f.err != nil {
		return 0, f.err
	}
	if f.id == 0 {
		f.id = 77
	}
	return f.id, nil
}

// --- Sonarr gate ---------------------------------------------------------

func TestSonarrAdd_Gate_AdminDirectAdd(t *testing.T) {
	t.Parallel()
	added := false
	cli := buildClient(t,
		func(_ context.Context, _ ports.AddSeriesPayload) (ports.AddSeriesResult, error) {
			added = true
			return ports.AddSeriesResult{SonarrSeriesID: 555}, nil
		},
		func(_ context.Context) ([]ports.Tag, error) { return nil, nil },
	)
	q := &fakeRequestQueue{}
	uc := NewAddToSonarrUseCase(
		fakeLookup{name: "main", client: cli},
		fakeUsers{user: &admin.User{ID: 1, Username: "alex", Role: admin.RoleAdmin}},
		NewTagResolver(&fakeTagCache{}, discardLog()),
		discardLog(),
	).WithRequestQueue(q)

	res, err := uc.Add(t.Context(), AddRequest{
		InstanceName: "main", TVDBID: 81189, QualityProfileID: 6,
		RootFolderPath: "/tv", Monitored: true, MonitorMode: "all", Username: "alex",
	})
	require.NoError(t, err)
	assert.True(t, added, "admin must add directly")
	assert.False(t, res.Requested)
	assert.Equal(t, 0, q.calls)
}

func TestSonarrAdd_Gate_AutoApproveDirectAdd(t *testing.T) {
	t.Parallel()
	added := false
	cli := buildClient(t,
		func(_ context.Context, _ ports.AddSeriesPayload) (ports.AddSeriesResult, error) {
			added = true
			return ports.AddSeriesResult{SonarrSeriesID: 1}, nil
		},
		func(_ context.Context) ([]ports.Tag, error) { return nil, nil },
	)
	q := &fakeRequestQueue{}
	uc := NewAddToSonarrUseCase(
		fakeLookup{name: "main", client: cli},
		fakeUsers{user: &admin.User{ID: 2, Username: "bob", Role: admin.RoleUser, AutoApprove: true}},
		NewTagResolver(&fakeTagCache{}, discardLog()),
		discardLog(),
	).WithRequestQueue(q)

	res, err := uc.Add(t.Context(), AddRequest{
		InstanceName: "main", TVDBID: 81189, QualityProfileID: 6,
		RootFolderPath: "/tv", Monitored: true, Username: "bob",
	})
	require.NoError(t, err)
	assert.True(t, added)
	assert.False(t, res.Requested)
	assert.Equal(t, 0, q.calls)
}

func TestSonarrAdd_Gate_RequestOnlyUserQueued(t *testing.T) {
	t.Parallel()
	added := false
	cli := buildClient(t,
		func(_ context.Context, _ ports.AddSeriesPayload) (ports.AddSeriesResult, error) {
			added = true
			return ports.AddSeriesResult{SonarrSeriesID: 1}, nil
		},
		func(_ context.Context) ([]ports.Tag, error) { return nil, nil },
	)
	q := &fakeRequestQueue{id: 42}
	seasons := []int{1, 2}
	uc := NewAddToSonarrUseCase(
		fakeLookup{name: "main", client: cli},
		fakeUsers{user: &admin.User{ID: 7, Username: "carol", Role: admin.RoleUser, Request: true}},
		NewTagResolver(&fakeTagCache{}, discardLog()),
		discardLog(),
	).WithRequestQueue(q)

	res, err := uc.Add(t.Context(), AddRequest{
		InstanceName: "main", TVDBID: 81189, QualityProfileID: 6,
		RootFolderPath: "/tv", Monitored: true, MonitorMode: "all",
		Username: "carol", MonitoredSeasons: &seasons,
	})
	require.NoError(t, err)
	assert.False(t, added, "request-only user must NOT add directly")
	assert.True(t, res.Requested)
	assert.Equal(t, int64(42), res.RequestID)
	require.Equal(t, 1, q.calls)
	assert.Equal(t, uint(7), q.userID)
	assert.Equal(t, reqdomain.MediaTypeTV, q.spec.MediaType)
	assert.Equal(t, int64(81189), q.spec.ExternalID)
	assert.Equal(t, "main", q.spec.InstanceName)
	assert.Equal(t, 6, q.spec.QualityProfileID)
	assert.Equal(t, "/tv", q.spec.RootFolderPath)
	require.NotNil(t, q.spec.Seasons)
	assert.Equal(t, []int{1, 2}, *q.spec.Seasons)
}

func TestSonarrAdd_Gate_NilQueueDirectAdd(t *testing.T) {
	t.Parallel()
	added := false
	cli := buildClient(t,
		func(_ context.Context, _ ports.AddSeriesPayload) (ports.AddSeriesResult, error) {
			added = true
			return ports.AddSeriesResult{SonarrSeriesID: 1}, nil
		},
		func(_ context.Context) ([]ports.Tag, error) { return nil, nil },
	)
	uc := NewAddToSonarrUseCase(
		fakeLookup{name: "main", client: cli},
		fakeUsers{user: &admin.User{ID: 7, Username: "carol", Role: admin.RoleUser, Request: true}},
		NewTagResolver(&fakeTagCache{}, discardLog()),
		discardLog(),
	) // no queue wired

	res, err := uc.Add(t.Context(), AddRequest{
		InstanceName: "main", TVDBID: 81189, QualityProfileID: 6,
		RootFolderPath: "/tv", Monitored: true, Username: "carol",
	})
	require.NoError(t, err)
	assert.True(t, added, "no queue wired → direct add (zero regression)")
	assert.False(t, res.Requested)
}

// --- Radarr gate (+ resolver seam) --------------------------------------

func TestRadarrAdd_Gate_RequestOnlyUserQueued(t *testing.T) {
	t.Parallel()
	added := false
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: oneMovieLookup,
		AddMovieFunc: func(_ context.Context, _ ports.AddMoviePayload) (ports.AddMovieResult, error) {
			added = true
			return ports.AddMovieResult{RadarrMovieID: 42}, nil
		},
	}
	q := &fakeRequestQueue{id: 99}
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog()).
		WithCurrentUserResolver(fakeUsers{user: &admin.User{ID: 3, Username: "dora", Role: admin.RoleUser, Request: true}}).
		WithRequestQueue(q)

	res, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 4,
		RootFolderPath: "/movies", Monitored: true, Username: "dora",
	})
	require.NoError(t, err)
	assert.False(t, added, "request-only user must NOT add directly")
	assert.True(t, res.Requested)
	assert.Equal(t, int64(99), res.RequestID)
	require.Equal(t, 1, q.calls)
	assert.Equal(t, uint(3), q.userID)
	assert.Equal(t, reqdomain.MediaTypeMovie, q.spec.MediaType)
	assert.Equal(t, int64(438631), q.spec.ExternalID)
	assert.Equal(t, "movies", q.spec.InstanceName)
	assert.Equal(t, 4, q.spec.QualityProfileID)
	assert.Equal(t, "/movies", q.spec.RootFolderPath)
}

func TestRadarrAdd_Gate_AdminDirectAdd(t *testing.T) {
	t.Parallel()
	added := false
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: oneMovieLookup,
		AddMovieFunc: func(_ context.Context, _ ports.AddMoviePayload) (ports.AddMovieResult, error) {
			added = true
			return ports.AddMovieResult{RadarrMovieID: 42}, nil
		},
	}
	q := &fakeRequestQueue{}
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog()).
		WithCurrentUserResolver(fakeUsers{user: &admin.User{ID: 1, Role: admin.RoleAdmin}}).
		WithRequestQueue(q)

	res, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 4,
		RootFolderPath: "/movies", Monitored: true, Username: "admin",
	})
	require.NoError(t, err)
	assert.True(t, added, "admin must add directly")
	assert.False(t, res.Requested)
	assert.Equal(t, 0, q.calls)
}

func TestRadarrAdd_Gate_NoResolverDirectAdd(t *testing.T) {
	t.Parallel()
	added := false
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: oneMovieLookup,
		AddMovieFunc: func(_ context.Context, _ ports.AddMoviePayload) (ports.AddMovieResult, error) {
			added = true
			return ports.AddMovieResult{RadarrMovieID: 42}, nil
		},
	}
	q := &fakeRequestQueue{}
	// Queue wired but NO resolver → gate disabled (direct add).
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog()).
		WithRequestQueue(q)

	res, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 4,
		RootFolderPath: "/movies", Monitored: true, Username: "dora",
	})
	require.NoError(t, err)
	assert.True(t, added, "no resolver → direct add")
	assert.False(t, res.Requested)
	assert.Equal(t, 0, q.calls)
}
