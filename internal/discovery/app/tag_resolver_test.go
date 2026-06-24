package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeTagCache is a TagCachePort double with explicit hit/miss control.
type fakeTagCache struct {
	row       admin.UserInstanceTag
	hasRow    bool
	getErr    error
	upsertErr error
	getCalls  atomic.Int32
	upserted  atomic.Int32
	last      admin.UserInstanceTag
}

func (f *fakeTagCache) Get(_ context.Context, _ uint, _ domain.InstanceName) (admin.UserInstanceTag, error) {
	f.getCalls.Add(1)
	if f.getErr != nil {
		return admin.UserInstanceTag{}, f.getErr
	}
	if !f.hasRow {
		return admin.UserInstanceTag{}, ports.ErrNotFound
	}
	return f.row, nil
}

func (f *fakeTagCache) Upsert(_ context.Context, t admin.UserInstanceTag) error {
	f.upserted.Add(1)
	f.last = t
	return f.upsertErr
}

// fakeSonarrTagPort doubles SonarrTagPort with explicit ListTags/CreateTag stubs.
type fakeSonarrTagPort struct {
	listTags  []ports.Tag
	listErr   error
	createTag ports.Tag
	createErr error
	listCalls atomic.Int32
	createN   atomic.Int32
}

func (f *fakeSonarrTagPort) ListTags(_ context.Context) ([]ports.Tag, error) {
	f.listCalls.Add(1)
	return f.listTags, f.listErr
}

func (f *fakeSonarrTagPort) CreateTag(_ context.Context, _ string) (ports.Tag, error) {
	f.createN.Add(1)
	return f.createTag, f.createErr
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestNormalizeUsername(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"Alex_Morbo", "alex-morbo"},
		{"alex@morbo.ru", "alex-morbo-ru"},
		{"alex\xf0\x9f\x8e\xac", "alex"},
		{"   ", "user"},
		{"---", "user"},
		{"Test User 123", "test-user-123"},
	}
	for _, tc := range cases {
		got := NormalizeUsername(tc.in)
		assert.Equal(t, tc.want, got, "in=%q", tc.in)
	}
	long := NormalizeUsername(strings.Repeat("a", 50))
	assert.Len(t, long, 30, "long username must cap at 30")
}

func TestUserTagLabel_Nil(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "sf-system", UserTagLabel(nil))
}

func TestUserTagLabel_User(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "sf-alex", UserTagLabel(&admin.User{Username: "alex"}))
}

func TestTagResolver_CacheHit(t *testing.T) {
	t.Parallel()
	cache := &fakeTagCache{
		hasRow: true,
		row: admin.UserInstanceTag{
			UserID: 7, InstanceName: "main",
			SonarrTagID: 42, SonarrTagLabel: "sf-alex",
		},
	}
	sonarr := &fakeSonarrTagPort{}
	r := NewTagResolver(cache, discardLog())

	id, label, err := r.Resolve(t.Context(), sonarr,
		&admin.User{ID: 7, Username: "alex"}, "main")
	require.NoError(t, err)
	assert.Equal(t, 42, id)
	assert.Equal(t, "sf-alex", label)
	assert.Equal(t, int32(0), sonarr.listCalls.Load(), "cache hit MUST skip Sonarr")
	assert.Equal(t, int32(0), sonarr.createN.Load())
	assert.Equal(t, int32(0), cache.upserted.Load(), "no rewrite on cache hit")
}

func TestTagResolver_CacheMiss_TagExists(t *testing.T) {
	t.Parallel()
	cache := &fakeTagCache{} // ErrNotFound on Get
	sonarr := &fakeSonarrTagPort{
		listTags: []ports.Tag{
			{ID: 1, Label: "other"}, {ID: 9, Label: "sf-alex"},
		},
	}
	r := NewTagResolver(cache, discardLog())

	id, label, err := r.Resolve(t.Context(), sonarr,
		&admin.User{ID: 7, Username: "alex"}, "main")
	require.NoError(t, err)
	assert.Equal(t, 9, id)
	assert.Equal(t, "sf-alex", label)
	assert.Equal(t, int32(0), sonarr.createN.Load(), "tag already exists — no CreateTag")
	assert.Equal(t, int32(1), cache.upserted.Load())
	assert.Equal(t, 9, cache.last.SonarrTagID)
}

func TestTagResolver_CacheMiss_TagAbsent_Created(t *testing.T) {
	t.Parallel()
	cache := &fakeTagCache{}
	sonarr := &fakeSonarrTagPort{
		listTags:  []ports.Tag{{ID: 1, Label: "other"}},
		createTag: ports.Tag{ID: 42, Label: "sf-alex"},
	}
	r := NewTagResolver(cache, discardLog())

	id, label, err := r.Resolve(t.Context(), sonarr,
		&admin.User{ID: 7, Username: "alex"}, "main")
	require.NoError(t, err)
	assert.Equal(t, 42, id)
	assert.Equal(t, "sf-alex", label)
	assert.Equal(t, int32(1), sonarr.createN.Load())
	assert.Equal(t, int32(1), cache.upserted.Load())
	assert.Equal(t, "sf-alex", cache.last.SonarrTagLabel)
}

func TestTagResolver_ListTags_ErrorPropagates(t *testing.T) {
	t.Parallel()
	cache := &fakeTagCache{}
	sonarr := &fakeSonarrTagPort{listErr: errors.New("boom")}
	r := NewTagResolver(cache, discardLog())

	_, _, err := r.Resolve(t.Context(), sonarr, &admin.User{ID: 1, Username: "u"}, "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list tags")
}

func TestTagResolver_Bypass_SystemLabel(t *testing.T) {
	t.Parallel()
	cache := &fakeTagCache{}
	sonarr := &fakeSonarrTagPort{
		createTag: ports.Tag{ID: 1, Label: "sf-system"},
	}
	r := NewTagResolver(cache, discardLog())

	id, label, err := r.Resolve(t.Context(), sonarr, nil, "main")
	require.NoError(t, err)
	assert.Equal(t, 1, id)
	assert.Equal(t, "sf-system", label)
	assert.Equal(t, uint(0), cache.last.UserID, "bypass MUST persist userID=0")
}
