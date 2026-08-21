package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/arrcore"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// fakeRadarrLookup implements AddRadarrInstanceLookup with a single-name match.
type fakeRadarrLookup struct {
	name   string
	client ports.RadarrClient
}

func (f fakeRadarrLookup) Lookup(name string) (ports.RadarrClient, bool) {
	if name != f.name {
		return nil, false
	}
	return f.client, true
}

// oneMovieLookup is the default LookupMovie stub returning a single match.
func oneMovieLookup(_ context.Context, _ string) ([]ports.RadarrLookupResult, error) {
	return []ports.RadarrLookupResult{{
		Title:     "Dune",
		TitleSlug: "dune-2021",
		Year:      2021,
		TMDBID:    438631,
		Images:    []ports.LookupImage{{CoverType: "poster", URL: "/p.jpg"}},
	}}, nil
}

func TestRadarrAdd_HappyPath(t *testing.T) {
	t.Parallel()
	var captured ports.AddMoviePayload
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: oneMovieLookup,
		AddMovieFunc: func(_ context.Context, p ports.AddMoviePayload) (ports.AddMovieResult, error) {
			captured = p
			return ports.AddMovieResult{RadarrMovieID: 42}, nil
		},
	}
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog())

	res, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 4,
		RootFolderPath: "/movies", Monitored: true, SearchOnAdd: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 42, res.RadarrMovieID)
	assert.False(t, res.AlreadyAdded)
	assert.Equal(t, "movies", string(res.InstanceName))
	// Lookup metadata copied onto the add payload.
	assert.Equal(t, "Dune", captured.Title)
	assert.Equal(t, "dune-2021", captured.TitleSlug)
	assert.Equal(t, 2021, captured.Year)
	assert.Equal(t, 438631, captured.TMDBID)
	assert.Equal(t, 4, captured.QualityProfileID)
	assert.Equal(t, "/movies", captured.RootFolderPath)
	assert.True(t, captured.Monitored)
	assert.True(t, captured.SearchOnAdd)
	require.Len(t, captured.Images, 1)
	assert.Equal(t, "poster", captured.Images[0].CoverType)
	// "" forwarded verbatim — the client defaults it to "released".
	assert.Equal(t, "", captured.MinimumAvailability)
}

func TestRadarrAdd_MinimumAvailabilityOverride(t *testing.T) {
	t.Parallel()
	var captured ports.AddMoviePayload
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: oneMovieLookup,
		AddMovieFunc: func(_ context.Context, p ports.AddMoviePayload) (ports.AddMovieResult, error) {
			captured = p
			return ports.AddMovieResult{RadarrMovieID: 7}, nil
		},
	}
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog())

	_, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 4,
		RootFolderPath: "/movies", Monitored: true, MinimumAvailability: "announced",
	})
	require.NoError(t, err)
	assert.Equal(t, "announced", captured.MinimumAvailability)
}

func TestRadarrAdd_AlreadyAdded_Idempotent(t *testing.T) {
	t.Parallel()
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: oneMovieLookup,
		AddMovieFunc: func(_ context.Context, _ ports.AddMoviePayload) (ports.AddMovieResult, error) {
			return ports.AddMovieResult{}, &arrcore.StatusError{
				Endpoint: "/api/v3/movie",
				Status:   400,
				Body:     `[{"errorMessage":"This movie has already been added","propertyName":"TmdbId","errorCode":"MovieExistsValidator"}]`,
				Arr:      "radarr",
			}
		},
	}
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog())

	res, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 4, RootFolderPath: "/movies", Monitored: true,
	})
	require.NoError(t, err, "duplicate tmdbId MUST be an idempotent success")
	assert.True(t, res.AlreadyAdded)
	assert.Equal(t, 0, res.RadarrMovieID)
	assert.Equal(t, "movies", string(res.InstanceName))
}

func TestRadarrAdd_InstanceNotFound(t *testing.T) {
	t.Parallel()
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies"}, discardLog())

	_, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "ghost", TMDBID: 1, QualityProfileID: 1, RootFolderPath: "/movies",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)
	var nf *sharedErrors.InstanceNotFoundError
	assert.ErrorAs(t, err, &nf)
}

func TestRadarrAdd_LookupEmpty_NotFound(t *testing.T) {
	t.Parallel()
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: func(_ context.Context, _ string) ([]ports.RadarrLookupResult, error) {
			return nil, nil
		},
	}
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog())

	_, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 999999, QualityProfileID: 1, RootFolderPath: "/movies",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)
	var nf *sharedErrors.InstanceNotFoundError
	assert.ErrorAs(t, err, &nf)
}

func TestRadarrAdd_LookupError_502(t *testing.T) {
	t.Parallel()
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: func(_ context.Context, _ string) ([]ports.RadarrLookupResult, error) {
			return nil, errors.New("dial tcp: refused")
		},
	}
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog())

	_, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 1, RootFolderPath: "/movies",
	})
	require.Error(t, err)
	var ru *sharedErrors.RadarrUnreachableError
	require.ErrorAs(t, err, &ru)
	assert.Equal(t, "movies", string(ru.Instance))
}

func TestRadarrAdd_AddMovieError_502(t *testing.T) {
	t.Parallel()
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: oneMovieLookup,
		AddMovieFunc: func(_ context.Context, _ ports.AddMoviePayload) (ports.AddMovieResult, error) {
			// Non-StatusError transport failure → not idempotent, surfaces 502.
			return ports.AddMovieResult{}, errors.New("dial tcp: refused")
		},
	}
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog())

	_, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 1, RootFolderPath: "/movies",
	})
	require.Error(t, err)
	var ru *sharedErrors.RadarrUnreachableError
	require.ErrorAs(t, err, &ru)
	assert.Equal(t, "movies", string(ru.Instance))
}

// TestRadarrAdd_AddMovie500_NotIdempotent guards the isMovieAlreadyAdded
// status gate: a 500 StatusError (even mentioning "already") is a real
// failure, not an idempotent add.
func TestRadarrAdd_AddMovie500_NotIdempotent(t *testing.T) {
	t.Parallel()
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: oneMovieLookup,
		AddMovieFunc: func(_ context.Context, _ ports.AddMoviePayload) (ports.AddMovieResult, error) {
			return ports.AddMovieResult{}, &arrcore.StatusError{
				Endpoint: "/api/v3/movie", Status: 500, Body: "internal error", Arr: "radarr",
			}
		},
	}
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog())

	_, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 1, RootFolderPath: "/movies",
	})
	require.Error(t, err)
	var ru *sharedErrors.RadarrUnreachableError
	require.ErrorAs(t, err, &ru)
}

// TestRadarrAdd_UserTagApplied — R-6 parity: the movie add payload carries the
// resolved sf-<user> tag and the result echoes it.
func TestRadarrAdd_UserTagApplied(t *testing.T) {
	t.Parallel()
	var captured ports.AddMoviePayload
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: oneMovieLookup,
		ListTagsFunc: func(_ context.Context) ([]ports.Tag, error) {
			return []ports.Tag{{ID: 5, Label: "sf-alex"}}, nil
		},
		AddMovieFunc: func(_ context.Context, p ports.AddMoviePayload) (ports.AddMovieResult, error) {
			captured = p
			return ports.AddMovieResult{RadarrMovieID: 42}, nil
		},
	}
	resolver := NewTagResolver(&fakeTagCache{}, discardLog())
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog()).
		WithCurrentUserResolver(fakeUsers{user: &admin.User{ID: 7, Username: "alex", Role: admin.RoleAdmin}}).
		WithTagResolver(resolver)

	res, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 4,
		RootFolderPath: "/movies", Monitored: true, Username: "alex",
	})
	require.NoError(t, err)
	assert.Equal(t, []int{5}, captured.Tags, "payload MUST carry the resolved tag id")
	assert.Equal(t, 5, res.UserTagID)
	assert.Equal(t, "sf-alex", res.UserTagLabel)
	assert.Equal(t, 42, res.RadarrMovieID)
}

// TestRadarrAdd_BypassUser_SystemTag — no username ⇒ user==nil ⇒ "sf-system",
// created on the arr when absent.
func TestRadarrAdd_BypassUser_SystemTag(t *testing.T) {
	t.Parallel()
	var captured ports.AddMoviePayload
	var createdLabel string
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: oneMovieLookup,
		ListTagsFunc:    func(_ context.Context) ([]ports.Tag, error) { return nil, nil },
		CreateTagFunc: func(_ context.Context, label string) (ports.Tag, error) {
			createdLabel = label
			return ports.Tag{ID: 9, Label: label}, nil
		},
		AddMovieFunc: func(_ context.Context, p ports.AddMoviePayload) (ports.AddMovieResult, error) {
			captured = p
			return ports.AddMovieResult{RadarrMovieID: 1}, nil
		},
	}
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog()).
		WithTagResolver(NewTagResolver(&fakeTagCache{}, discardLog()))

	res, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 4,
		RootFolderPath: "/movies", Monitored: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "sf-system", createdLabel)
	assert.Equal(t, []int{9}, captured.Tags)
	assert.Equal(t, "sf-system", res.UserTagLabel)
}

// TestRadarrAdd_TagFailure_NonBlocking — a ListTags outage must NOT block the
// add: the movie lands untagged with zero UserTag*.
func TestRadarrAdd_TagFailure_NonBlocking(t *testing.T) {
	t.Parallel()
	var captured ports.AddMoviePayload
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: oneMovieLookup,
		ListTagsFunc: func(_ context.Context) ([]ports.Tag, error) {
			return nil, errors.New("radarr tag endpoint down")
		},
		AddMovieFunc: func(_ context.Context, p ports.AddMoviePayload) (ports.AddMovieResult, error) {
			captured = p
			return ports.AddMovieResult{RadarrMovieID: 42}, nil
		},
	}
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog()).
		WithCurrentUserResolver(fakeUsers{user: &admin.User{ID: 7, Username: "alex", Role: admin.RoleAdmin}}).
		WithTagResolver(NewTagResolver(&fakeTagCache{}, discardLog()))

	res, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 4,
		RootFolderPath: "/movies", Monitored: true, Username: "alex",
	})
	require.NoError(t, err, "tag failure MUST NOT fail the add")
	assert.Equal(t, 42, res.RadarrMovieID)
	assert.Empty(t, captured.Tags, "untagged payload on tag failure")
	assert.Equal(t, 0, res.UserTagID)
	assert.Equal(t, "", res.UserTagLabel)
}

// TestRadarrAdd_NoResolver_TagLess — the pre-R-6 shape stays available: without
// WithTagResolver the payload carries no tags and ListTags is never called.
func TestRadarrAdd_NoResolver_TagLess(t *testing.T) {
	t.Parallel()
	var captured ports.AddMoviePayload
	var listCalls int
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: oneMovieLookup,
		ListTagsFunc: func(_ context.Context) ([]ports.Tag, error) {
			listCalls++
			return nil, nil
		},
		AddMovieFunc: func(_ context.Context, p ports.AddMoviePayload) (ports.AddMovieResult, error) {
			captured = p
			return ports.AddMovieResult{RadarrMovieID: 42}, nil
		},
	}
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog())

	res, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 4,
		RootFolderPath: "/movies", Monitored: true, Username: "alex",
	})
	require.NoError(t, err)
	assert.Empty(t, captured.Tags)
	assert.Equal(t, 0, listCalls, "nil resolver MUST NOT touch the arr tag endpoint")
	assert.Equal(t, 0, res.UserTagID)
}

// TestRadarrAdd_AlreadyAdded_KeepsTag — the idempotent duplicate path still
// reports the tag that was resolved for the attempt.
func TestRadarrAdd_AlreadyAdded_KeepsTag(t *testing.T) {
	t.Parallel()
	cli := &ports.RadarrClientMock{
		LookupMovieFunc: oneMovieLookup,
		ListTagsFunc: func(_ context.Context) ([]ports.Tag, error) {
			return []ports.Tag{{ID: 5, Label: "sf-alex"}}, nil
		},
		AddMovieFunc: func(_ context.Context, _ ports.AddMoviePayload) (ports.AddMovieResult, error) {
			return ports.AddMovieResult{}, &arrcore.StatusError{
				Endpoint: "/api/v3/movie",
				Status:   400,
				Body:     `[{"errorMessage":"This movie has already been added","errorCode":"MovieExistsValidator"}]`,
				Arr:      "radarr",
			}
		},
	}
	uc := NewAddToRadarrUseCase(fakeRadarrLookup{name: "movies", client: cli}, discardLog()).
		WithCurrentUserResolver(fakeUsers{user: &admin.User{ID: 7, Username: "alex", Role: admin.RoleAdmin}}).
		WithTagResolver(NewTagResolver(&fakeTagCache{}, discardLog()))

	res, err := uc.Add(t.Context(), AddMovieRequest{
		InstanceName: "movies", TMDBID: 438631, QualityProfileID: 4,
		RootFolderPath: "/movies", Monitored: true, Username: "alex",
	})
	require.NoError(t, err)
	assert.True(t, res.AlreadyAdded)
	assert.Equal(t, "sf-alex", res.UserTagLabel)
	assert.Equal(t, 5, res.UserTagID)
}
