package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// fakeLookup implements AddInstanceLookup with a single-name match.
type fakeLookup struct {
	name   string
	client ports.SonarrClient
}

func (f fakeLookup) Lookup(name string) (ports.SonarrClient, bool) {
	if name != f.name {
		return nil, false
	}
	return f.client, true
}

// fakeUsers implements CurrentUserResolver: optional user + error path.
type fakeUsers struct {
	user *admin.User
	err  error
}

func (f fakeUsers) GetCurrent(_ context.Context, _ string) (*admin.User, error) {
	return f.user, f.err
}

func buildClient(t *testing.T,
	addFn func(context.Context, ports.AddSeriesPayload) (ports.AddSeriesResult, error),
	listFn func(context.Context) ([]ports.Tag, error),
) *ports.SonarrClientMock {
	t.Helper()
	return &ports.SonarrClientMock{
		ListTagsFunc: listFn,
		CreateTagFunc: func(_ context.Context, label string) (ports.Tag, error) {
			return ports.Tag{ID: 99, Label: label}, nil
		},
		AddSeriesFunc: addFn,
	}
}

func TestAdd_HappyPath(t *testing.T) {
	t.Parallel()
	var captured ports.AddSeriesPayload
	cli := buildClient(t,
		func(_ context.Context, p ports.AddSeriesPayload) (ports.AddSeriesResult, error) {
			captured = p
			return ports.AddSeriesResult{SonarrSeriesID: 555}, nil
		},
		func(_ context.Context) ([]ports.Tag, error) {
			return []ports.Tag{{ID: 7, Label: "sf-alex"}}, nil
		},
	)
	resolver := NewTagResolver(&fakeTagCache{}, discardLog())
	uc := NewAddToSonarrUseCase(
		fakeLookup{name: "main", client: cli},
		fakeUsers{user: &admin.User{ID: 1, Username: "alex"}},
		resolver,
		discardLog(),
	)

	res, err := uc.Add(t.Context(), AddRequest{
		InstanceName: "main", TVDBID: 81189, QualityProfileID: 6,
		RootFolderPath: "/tv", Monitored: true, MonitorMode: "all",
		SearchOnAdd: true, Username: "alex",
	})
	require.NoError(t, err)
	assert.Equal(t, 555, res.SonarrSeriesID)
	assert.Equal(t, "sf-alex", res.UserTagLabel)
	assert.Equal(t, 7, res.UserTagID)
	assert.Equal(t, []int{7}, captured.Tags)
	assert.Equal(t, 81189, captured.TVDBID)
}

func TestAdd_InstanceNotFound(t *testing.T) {
	t.Parallel()
	resolver := NewTagResolver(&fakeTagCache{}, discardLog())
	uc := NewAddToSonarrUseCase(
		fakeLookup{name: "main"}, // empty match
		fakeUsers{},
		resolver,
		discardLog(),
	)

	_, err := uc.Add(t.Context(), AddRequest{
		InstanceName: "ghost", TVDBID: 1, QualityProfileID: 1, RootFolderPath: "/tv",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)
	var nf *sharedErrors.InstanceNotFoundError
	assert.ErrorAs(t, err, &nf)
}

func TestAdd_TagResolveFailure_NonBlocking(t *testing.T) {
	t.Parallel()
	var captured ports.AddSeriesPayload
	cli := buildClient(t,
		func(_ context.Context, p ports.AddSeriesPayload) (ports.AddSeriesResult, error) {
			captured = p
			return ports.AddSeriesResult{SonarrSeriesID: 12}, nil
		},
		func(_ context.Context) ([]ports.Tag, error) {
			return nil, errors.New("list boom")
		},
	)
	resolver := NewTagResolver(&fakeTagCache{}, discardLog())
	uc := NewAddToSonarrUseCase(
		fakeLookup{name: "main", client: cli},
		fakeUsers{user: &admin.User{ID: 1, Username: "alex"}},
		resolver,
		discardLog(),
	)

	res, err := uc.Add(t.Context(), AddRequest{
		InstanceName: "main", TVDBID: 1, QualityProfileID: 1, RootFolderPath: "/tv",
	})
	require.NoError(t, err, "tag failure MUST NOT block AddSeries")
	assert.Equal(t, 12, res.SonarrSeriesID)
	assert.Equal(t, "", res.UserTagLabel)
	assert.Equal(t, 0, res.UserTagID)
	assert.Nil(t, captured.Tags, "no tag — Tags MUST be nil/omitempty")
}

func TestAdd_SonarrAddSeriesError_502(t *testing.T) {
	t.Parallel()
	cli := buildClient(t,
		func(_ context.Context, _ ports.AddSeriesPayload) (ports.AddSeriesResult, error) {
			return ports.AddSeriesResult{}, errors.New("dial tcp: refused")
		},
		func(_ context.Context) ([]ports.Tag, error) {
			return []ports.Tag{{ID: 7, Label: "sf-alex"}}, nil
		},
	)
	resolver := NewTagResolver(&fakeTagCache{}, discardLog())
	uc := NewAddToSonarrUseCase(
		fakeLookup{name: "main", client: cli},
		fakeUsers{user: &admin.User{ID: 1, Username: "alex"}},
		resolver,
		discardLog(),
	)

	_, err := uc.Add(t.Context(), AddRequest{
		InstanceName: "main", TVDBID: 1, QualityProfileID: 1, RootFolderPath: "/tv",
	})
	require.Error(t, err)
	var su *sharedErrors.SonarrUnreachableError
	require.ErrorAs(t, err, &su)
	assert.Equal(t, "main", string(su.Instance))
}

// TestAdd_MonitoredSeasons_LookupAndStamp verifies that when
// req.MonitoredSeasons is non-empty the use case calls LookupSeries to
// discover the full season list, then stamps `monitored=true` only on
// the requested numbers and forwards the explicit array on the payload.
// Story 524 N-4 per-season picker.
func TestAdd_MonitoredSeasons_LookupAndStamp(t *testing.T) {
	t.Parallel()
	var (
		capturedPayload ports.AddSeriesPayload
		gotLookupTerm   string
	)
	cli := &ports.SonarrClientMock{
		ListTagsFunc: func(_ context.Context) ([]ports.Tag, error) { return nil, nil },
		CreateTagFunc: func(_ context.Context, label string) (ports.Tag, error) {
			return ports.Tag{ID: 12, Label: label}, nil
		},
		LookupSeriesFunc: func(_ context.Context, term string) ([]ports.SonarrLookupResult, error) {
			gotLookupTerm = term
			return []ports.SonarrLookupResult{{
				Title:  "Rick and Morty",
				TVDBID: 275274,
				Seasons: []ports.SeasonInfo{
					{SeasonNumber: 0, EpisodeCount: 0, Monitored: false},
					{SeasonNumber: 1, EpisodeCount: 11, Monitored: true},
					{SeasonNumber: 2, EpisodeCount: 10, Monitored: true},
					{SeasonNumber: 3, EpisodeCount: 10, Monitored: true},
				},
			}}, nil
		},
		AddSeriesFunc: func(_ context.Context, p ports.AddSeriesPayload) (ports.AddSeriesResult, error) {
			capturedPayload = p
			return ports.AddSeriesResult{SonarrSeriesID: 888}, nil
		},
	}
	resolver := NewTagResolver(&fakeTagCache{}, discardLog())
	uc := NewAddToSonarrUseCase(
		fakeLookup{name: "main", client: cli},
		fakeUsers{user: &admin.User{ID: 1, Username: "alex"}},
		resolver,
		discardLog(),
	)

	res, err := uc.Add(t.Context(), AddRequest{
		InstanceName:     "main",
		TVDBID:           275274,
		QualityProfileID: 1,
		RootFolderPath:   "/tv",
		Monitored:        true,
		MonitorMode:      "none",
		Username:         "alex",
		MonitoredSeasons: []int{1, 3},
	})
	require.NoError(t, err)
	assert.Equal(t, 888, res.SonarrSeriesID)
	assert.Equal(t, "tvdb:275274", gotLookupTerm)
	require.Len(t, capturedPayload.Seasons, 4)
	assert.Equal(t, 0, capturedPayload.Seasons[0].SeasonNumber)
	assert.False(t, capturedPayload.Seasons[0].Monitored)
	assert.Equal(t, 1, capturedPayload.Seasons[1].SeasonNumber)
	assert.True(t, capturedPayload.Seasons[1].Monitored)
	assert.Equal(t, 2, capturedPayload.Seasons[2].SeasonNumber)
	assert.False(t, capturedPayload.Seasons[2].Monitored)
	assert.Equal(t, 3, capturedPayload.Seasons[3].SeasonNumber)
	assert.True(t, capturedPayload.Seasons[3].Monitored)
}

// TestAdd_MonitoredSeasons_LookupEmpty surfaces 404 when Sonarr's lookup
// returns no matches for the TVDB id (typed instance_not_found error
// joined to ports.ErrNotFound).
func TestAdd_MonitoredSeasons_LookupEmpty(t *testing.T) {
	t.Parallel()
	cli := &ports.SonarrClientMock{
		ListTagsFunc: func(_ context.Context) ([]ports.Tag, error) { return nil, nil },
		CreateTagFunc: func(_ context.Context, _ string) (ports.Tag, error) {
			return ports.Tag{ID: 1, Label: "x"}, nil
		},
		LookupSeriesFunc: func(_ context.Context, _ string) ([]ports.SonarrLookupResult, error) {
			return nil, nil
		},
	}
	resolver := NewTagResolver(&fakeTagCache{}, discardLog())
	uc := NewAddToSonarrUseCase(
		fakeLookup{name: "main", client: cli},
		fakeUsers{user: &admin.User{ID: 1, Username: "alex"}},
		resolver,
		discardLog(),
	)

	_, err := uc.Add(t.Context(), AddRequest{
		InstanceName: "main", TVDBID: 999, QualityProfileID: 1,
		RootFolderPath: "/tv", Username: "alex",
		MonitoredSeasons: []int{1},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

// TestAdd_MonitoredSeasons_LookupError surfaces 502 on Sonarr lookup
// failure (network / 5xx).
func TestAdd_MonitoredSeasons_LookupError(t *testing.T) {
	t.Parallel()
	cli := &ports.SonarrClientMock{
		ListTagsFunc: func(_ context.Context) ([]ports.Tag, error) { return nil, nil },
		CreateTagFunc: func(_ context.Context, _ string) (ports.Tag, error) {
			return ports.Tag{ID: 1, Label: "x"}, nil
		},
		LookupSeriesFunc: func(_ context.Context, _ string) ([]ports.SonarrLookupResult, error) {
			return nil, errors.New("dial: connection refused")
		},
	}
	resolver := NewTagResolver(&fakeTagCache{}, discardLog())
	uc := NewAddToSonarrUseCase(
		fakeLookup{name: "main", client: cli},
		fakeUsers{user: &admin.User{ID: 1, Username: "alex"}},
		resolver,
		discardLog(),
	)

	_, err := uc.Add(t.Context(), AddRequest{
		InstanceName: "main", TVDBID: 1, QualityProfileID: 1,
		RootFolderPath: "/tv", Username: "alex",
		MonitoredSeasons: []int{1},
	})
	require.Error(t, err)
	var su *sharedErrors.SonarrUnreachableError
	require.ErrorAs(t, err, &su)
}
