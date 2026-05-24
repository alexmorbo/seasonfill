package instance

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

type fakeInstanceRepo struct {
	mu      sync.Mutex
	rows    map[string]runtime.InstanceSnapshot
	updated map[string]time.Time
	count   int
	nextID  uint

	updateCalls   int
	preserveCalls int
	deleteCalls   int

	listErr   error
	createErr error
}

func newFakeRepo() *fakeInstanceRepo {
	return &fakeInstanceRepo{
		rows:    map[string]runtime.InstanceSnapshot{},
		updated: map[string]time.Time{},
		nextID:  1,
	}
}

func (f *fakeInstanceRepo) List(_ context.Context, _ *crypto.Cipher) ([]runtime.InstanceSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]runtime.InstanceSnapshot, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}
func (f *fakeInstanceRepo) GetByName(_ context.Context, name string, _ *crypto.Cipher) (runtime.InstanceSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[name]
	if !ok {
		return runtime.InstanceSnapshot{}, ports.ErrNotFound
	}
	return r, nil
}
func (f *fakeInstanceRepo) Create(_ context.Context, inst runtime.InstanceSnapshot, _ *crypto.Cipher) (uint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return 0, f.createErr
	}
	inst.ID = f.nextID
	f.nextID++
	f.rows[inst.Name] = inst
	f.updated[inst.Name] = time.Now().UTC()
	f.count++
	return inst.ID, nil
}
func (f *fakeInstanceRepo) Update(ctx context.Context, inst runtime.InstanceSnapshot, c *crypto.Cipher) error {
	return f.UpdateWithOptions(ctx, inst, c, false)
}
func (f *fakeInstanceRepo) UpdateWithOptions(_ context.Context, inst runtime.InstanceSnapshot, _ *crypto.Cipher, preserve bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	if preserve {
		f.preserveCalls++
	}
	f.rows[inst.Name] = inst
	f.updated[inst.Name] = time.Now().UTC()
	return nil
}
func (f *fakeInstanceRepo) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	if _, ok := f.rows[name]; !ok {
		return ports.ErrNotFound
	}
	delete(f.rows, name)
	delete(f.updated, name)
	f.count--
	return nil
}
func (f *fakeInstanceRepo) Count(_ context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count, nil
}
func (f *fakeInstanceRepo) GetUpdatedAt(_ context.Context, name string) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ts, ok := f.updated[name]
	if !ok {
		return time.Time{}, ports.ErrNotFound
	}
	return ts, nil
}

type fakeRuntimeRepo struct{}

func (f *fakeRuntimeRepo) Get(_ context.Context) (ports.RuntimeConfigRow, error) {
	return ports.RuntimeConfigRow{}, nil
}
func (f *fakeRuntimeRepo) Upsert(_ context.Context, _ runtime.Snapshot) error { return nil }
func (f *fakeRuntimeRepo) SaveAPIKey(_ context.Context, _ []byte, _ bool) error { return nil }

func setup(t *testing.T) (*UseCase, *fakeInstanceRepo, *runtime.Bus, <-chan runtime.Snapshot) {
	t.Helper()
	repo := newFakeRepo()
	bus := runtime.NewBus(nil)
	t.Cleanup(bus.Close)
	ch := bus.Subscribe("test")
	uc := New(repo, &fakeRuntimeRepo{}, nil, bus, nil)
	return uc, repo, bus, ch
}

func validSnap(name string) runtime.InstanceSnapshot {
	return runtime.InstanceSnapshot{
		Name: name, URL: "http://sonarr:8989", APIKey: "abc",
	}
}

func TestCreate_OK_PublishesSnapshot(t *testing.T) {
	t.Parallel()
	uc, _, _, ch := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	select {
	case snap := <-ch:
		require.Len(t, snap.Instances, 1)
		assert.Equal(t, "alpha", snap.Instances[0].Name)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected snapshot on bus within 100ms")
	}
}

func TestCreate_Duplicate(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	err := uc.Create(context.Background(), validSnap("alpha"))
	assert.ErrorIs(t, err, ErrDuplicateName)
}

func TestCreate_ValidationFailures(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup(t)
	cases := []runtime.InstanceSnapshot{
		{Name: "has space", URL: "http://x", APIKey: "k"},
		{Name: "alpha", URL: "", APIKey: "k"},
		{Name: "alpha", URL: "http://x", APIKey: ""},
		{Name: "alpha", URL: "http://x", APIKey: "k", Mode: "weird"},
	}
	for _, tc := range cases {
		assert.ErrorIs(t, uc.Create(context.Background(), tc), ErrValidation)
	}
}

func TestUpdate_NameImmutable(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	err := uc.Update(context.Background(), "alpha",
		runtime.InstanceSnapshot{Name: "beta", URL: "http://x", APIKey: "k"}, time.Time{})
	assert.ErrorIs(t, err, ErrNameImmutable)
}

func TestUpdate_EmptyKey_PreservesSecret(t *testing.T) {
	t.Parallel()
	uc, repo, _, _ := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	upd := validSnap("alpha")
	upd.APIKey = ""
	require.NoError(t, uc.Update(context.Background(), "alpha", upd, time.Time{}))
	assert.Equal(t, 1, repo.preserveCalls, "preserveSecret must be true when api_key is empty")
}

func TestUpdate_StaleIfUnmodifiedSince(t *testing.T) {
	t.Parallel()
	uc, repo, _, _ := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	// Simulate "client snapshot was taken 1h ago" — stored updated_at is now.
	repo.updated["alpha"] = time.Now().UTC()
	err := uc.Update(context.Background(), "alpha", validSnap("alpha"),
		time.Now().UTC().Add(-time.Hour))
	assert.ErrorIs(t, err, ErrStaleWrite)
}

func TestUpdate_NoHeader_LWWProceeds(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	err := uc.Update(context.Background(), "alpha", validSnap("alpha"), time.Time{})
	assert.NoError(t, err)
}

func TestDelete_LastInstance_Blocked(t *testing.T) {
	t.Parallel()
	uc, repo, _, _ := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	err := uc.Delete(context.Background(), "alpha")
	assert.ErrorIs(t, err, ErrLastInstance)
	assert.Equal(t, 0, repo.deleteCalls, "must not call repo.Delete when blocked")
}

func TestDelete_NonLast_Cascades(t *testing.T) {
	t.Parallel()
	uc, repo, _, ch := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	<-ch // drain create snapshot
	require.NoError(t, uc.Create(context.Background(), validSnap("beta")))
	<-ch // drain
	require.NoError(t, uc.Delete(context.Background(), "alpha"))
	assert.Equal(t, 1, repo.deleteCalls)
	select {
	case snap := <-ch:
		assert.Len(t, snap.Instances, 1)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("delete must publish a snapshot")
	}
}

func TestDelete_NotFound(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup(t)
	err := uc.Delete(context.Background(), "ghost")
	assert.ErrorIs(t, err, ports.ErrNotFound)
}

func TestGet_MasksKey(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	got, ts, err := uc.Get(context.Background(), "alpha")
	require.NoError(t, err)
	assert.Equal(t, "***", got.APIKey)
	assert.False(t, ts.IsZero())
}
