package moviecollection

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

type fakeRadarrColClient struct {
	cols       []ports.RadarrCollection
	getErr     error
	putErr     error
	putCalls   int
	putPayload ports.RadarrCollection
}

func (f *fakeRadarrColClient) GetCollections(_ context.Context) ([]ports.RadarrCollection, error) {
	return f.cols, f.getErr
}

func (f *fakeRadarrColClient) PutCollection(_ context.Context, col ports.RadarrCollection) error {
	f.putCalls++
	f.putPayload = col
	return f.putErr
}

type fakeLookup struct {
	client RadarrCollectionClient
	ok     bool
}

func (f *fakeLookup) Lookup(string) (RadarrCollectionClient, bool) { return f.client, f.ok }

type fakeMonitorStore struct {
	calls  int
	gotID  int
	gotMon bool
	err    error
}

func (f *fakeMonitorStore) SetRadarrMonitored(_ context.Context, id int, mon bool) error {
	f.calls++
	f.gotID = id
	f.gotMon = mon
	return f.err
}

func TestEnableNativeMonitor_HappyPath(t *testing.T) {
	client := &fakeRadarrColClient{cols: []ports.RadarrCollection{
		{ID: 12, TMDBID: 726871, Monitored: false},
	}}
	store := &fakeMonitorStore{}
	uc := NewRadarrMonitorUseCase(&fakeLookup{client: client, ok: true}, store, testLog())

	require.NoError(t, uc.EnableNativeMonitor(context.Background(), EnableMonitorRequest{
		InstanceName: "r1", TMDBCollectionID: 726871,
	}))
	require.Equal(t, 1, client.putCalls)
	assert.True(t, client.putPayload.Monitored)
	assert.Equal(t, 12, client.putPayload.ID)
	require.Equal(t, 1, store.calls)
	assert.Equal(t, 726871, store.gotID)
	assert.True(t, store.gotMon)
}

func TestEnableNativeMonitor_AlreadyMonitoredNoPut(t *testing.T) {
	client := &fakeRadarrColClient{cols: []ports.RadarrCollection{
		{ID: 12, TMDBID: 726871, Monitored: true},
	}}
	store := &fakeMonitorStore{}
	uc := NewRadarrMonitorUseCase(&fakeLookup{client: client, ok: true}, store, testLog())

	require.NoError(t, uc.EnableNativeMonitor(context.Background(), EnableMonitorRequest{
		InstanceName: "r1", TMDBCollectionID: 726871,
	}))
	assert.Equal(t, 0, client.putCalls, "already monitored → no redundant PUT")
	assert.Equal(t, 1, store.calls, "flag still recorded (idempotent)")
}

func TestEnableNativeMonitor_InstanceNotFound(t *testing.T) {
	uc := NewRadarrMonitorUseCase(&fakeLookup{ok: false}, &fakeMonitorStore{}, testLog())
	err := uc.EnableNativeMonitor(context.Background(), EnableMonitorRequest{InstanceName: "nope", TMDBCollectionID: 1})
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)
}

func TestEnableNativeMonitor_CollectionNotInRadarr(t *testing.T) {
	client := &fakeRadarrColClient{cols: []ports.RadarrCollection{{ID: 9, TMDBID: 111}}}
	uc := NewRadarrMonitorUseCase(&fakeLookup{client: client, ok: true}, &fakeMonitorStore{}, testLog())
	err := uc.EnableNativeMonitor(context.Background(), EnableMonitorRequest{InstanceName: "r1", TMDBCollectionID: 726871})
	require.ErrorIs(t, err, ErrRadarrCollectionNotFound)
}

func TestEnableNativeMonitor_GetErr(t *testing.T) {
	client := &fakeRadarrColClient{getErr: errors.New("radarr down")}
	uc := NewRadarrMonitorUseCase(&fakeLookup{client: client, ok: true}, &fakeMonitorStore{}, testLog())
	err := uc.EnableNativeMonitor(context.Background(), EnableMonitorRequest{InstanceName: "r1", TMDBCollectionID: 726871})
	require.Error(t, err)
}

func TestEnableNativeMonitor_StoreNotFoundTolerated(t *testing.T) {
	client := &fakeRadarrColClient{cols: []ports.RadarrCollection{{ID: 12, TMDBID: 726871}}}
	store := &fakeMonitorStore{err: ports.ErrNotFound}
	uc := NewRadarrMonitorUseCase(&fakeLookup{client: client, ok: true}, store, testLog())
	require.NoError(t, uc.EnableNativeMonitor(context.Background(), EnableMonitorRequest{
		InstanceName: "r1", TMDBCollectionID: 726871,
	}), "a missing collections row is tolerated (idempotent)")
}

func TestEnableNativeMonitor_ZeroID(t *testing.T) {
	uc := NewRadarrMonitorUseCase(&fakeLookup{ok: true, client: &fakeRadarrColClient{}}, &fakeMonitorStore{}, testLog())
	require.Error(t, uc.EnableNativeMonitor(context.Background(), EnableMonitorRequest{InstanceName: "r1"}))
}

func TestNewRadarrMonitorUseCase_NilDepsPanic(t *testing.T) {
	assert.Panics(t, func() { NewRadarrMonitorUseCase(nil, &fakeMonitorStore{}, testLog()) })
	assert.Panics(t, func() { NewRadarrMonitorUseCase(&fakeLookup{}, nil, testLog()) })
	assert.Panics(t, func() { NewRadarrMonitorUseCase(&fakeLookup{}, &fakeMonitorStore{}, nil) })
}
