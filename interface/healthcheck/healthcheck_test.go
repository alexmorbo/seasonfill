package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
)

type fakeSonarr struct {
	name string
	err  error
}

func (f *fakeSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	if f.err != nil {
		return ports.SystemStatus{}, f.err
	}
	return ports.SystemStatus{Version: "test"}, nil
}
func (f *fakeSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (f *fakeSonarr) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (f *fakeSonarr) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (f *fakeSonarr) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return nil, nil
}
func (f *fakeSonarr) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	return nil, nil
}
func (f *fakeSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (f *fakeSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (f *fakeSonarr) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (f *fakeSonarr) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *fakeSonarr) ForceGrab(_ context.Context, _ string, _ int) error { return nil }
func (f *fakeSonarr) Name() string                                       { return f.name }

func openDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	return db
}

func TestChecker_New_InitialStateUnknown(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "a"}, &fakeSonarr{name: "b"}})
	snap := c.Snapshot()
	assert.Len(t, snap, 2)
	for _, h := range snap {
		assert.Equal(t, instance.HealthUnavailableUnknown, h.Health)
	}
	assert.False(t, c.AnyInstanceAvailable())
}

func TestChecker_Preflight_AllUp(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "main"}})
	c.Preflight(context.Background())

	assert.True(t, c.AnyInstanceAvailable())
	snap := c.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, instance.HealthAvailable, snap[0].Health)
	assert.Empty(t, snap[0].LastError)
}

func TestChecker_Preflight_Auth(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{
		name: "main",
		err:  fmt.Errorf("%w: 401", domain.ErrInstanceUnauthorized),
	}})
	c.Preflight(context.Background())
	snap := c.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, instance.HealthUnavailableAuth, snap[0].Health)
}

func TestChecker_Preflight_Network(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{
		name: "main",
		err:  fmt.Errorf("dial fail: %w", domain.ErrInstanceNetwork),
	}})
	c.Preflight(context.Background())
	snap := c.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, instance.HealthUnavailableNetwork, snap[0].Health)
}

func TestChecker_Preflight_UnknownErr(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "main", err: errors.New("???")}})
	c.Preflight(context.Background())
	snap := c.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, instance.HealthUnavailableUnknown, snap[0].Health)
}

func TestChecker_Preflight_Mixed_AnyAvailable(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{
		&fakeSonarr{name: "ok"},
		&fakeSonarr{name: "fail", err: errors.New("nope")},
	})
	c.Preflight(context.Background())
	assert.True(t, c.AnyInstanceAvailable(), "any available is enough")
}

func TestChecker_RecheckByName(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	a := &fakeSonarr{name: "a", err: errors.New("boom")}
	c := New(db, []ports.SonarrClient{a})
	c.Preflight(context.Background())
	a.err = nil
	c.RecheckByName(context.Background(), "a")
	snap := c.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, instance.HealthAvailable, snap[0].Health)
}

func TestChecker_RecheckByName_UnknownInstanceIsNoOp(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "a"}})
	c.Preflight(context.Background())
	c.RecheckByName(context.Background(), "missing")
	snap := c.Snapshot()
	require.Len(t, snap, 1)
}

func TestChecker_DatabaseUp(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, nil)
	assert.True(t, c.DatabaseUp(context.Background()))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	assert.False(t, c.DatabaseUp(context.Background()))
}

func TestChecker_Registry_Exposed(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "main"}})
	require.NotNil(t, c.Registry())
	assert.Len(t, c.Registry().Names(), 1)
}

func TestChecker_Run_StopsOnContextCancel(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "main"}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx, 50*time.Millisecond)
		close(done)
	}()

	time.Sleep(120 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}

	assert.True(t, c.AnyInstanceAvailable())
}
