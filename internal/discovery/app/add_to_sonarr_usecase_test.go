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
