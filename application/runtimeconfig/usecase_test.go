package runtimeconfig

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

type fakeRuntimeRepo struct {
	mu        sync.Mutex
	row       ports.RuntimeConfigRow
	exists    bool
	getErr    error
	upsertErr error
	upserts   int
}

func (f *fakeRuntimeRepo) Get(_ context.Context) (ports.RuntimeConfigRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return ports.RuntimeConfigRow{}, f.getErr
	}
	if !f.exists {
		return ports.RuntimeConfigRow{}, ports.ErrNotFound
	}
	return f.row, nil
}

func (f *fakeRuntimeRepo) Upsert(_ context.Context, snap runtime.Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserts++
	f.row = ports.RuntimeConfigRow{
		Cron: snap.Cron, Scan: snap.Scan, DryRun: snap.DryRun,
		GlobalRateLimit: snap.GlobalRateLimit, Auth: snap.Auth,
		UpdatedAt: time.Now().UTC(),
	}
	f.exists = true
	return nil
}

func (f *fakeRuntimeRepo) SaveAPIKey(_ context.Context, _ []byte, _ bool) error {
	return nil
}

type fakeInstanceRepo struct{}

func (fakeInstanceRepo) List(_ context.Context, _ *crypto.Cipher) ([]runtime.InstanceSnapshot, error) {
	return []runtime.InstanceSnapshot{{Name: "alpha", URL: "http://x", APIKey: "k"}}, nil
}
func (fakeInstanceRepo) GetByName(_ context.Context, _ string, _ *crypto.Cipher) (runtime.InstanceSnapshot, error) {
	return runtime.InstanceSnapshot{}, ports.ErrNotFound
}
func (fakeInstanceRepo) Create(_ context.Context, _ runtime.InstanceSnapshot, _ *crypto.Cipher) (uint, error) {
	return 0, nil
}
func (fakeInstanceRepo) Update(_ context.Context, _ runtime.InstanceSnapshot, _ *crypto.Cipher) error {
	return nil
}
func (fakeInstanceRepo) UpdateWithOptions(_ context.Context, _ runtime.InstanceSnapshot, _ *crypto.Cipher, _ bool) error {
	return nil
}
func (fakeInstanceRepo) Delete(_ context.Context, _ string) error { return nil }
func (fakeInstanceRepo) Count(_ context.Context) (int, error)     { return 1, nil }
func (fakeInstanceRepo) GetUpdatedAt(_ context.Context, _ string) (time.Time, error) {
	return time.Time{}, ports.ErrNotFound
}

func validDTO() dto.RuntimeConfigDTO {
	return dto.RuntimeConfigDTO{
		Cron: dto.RuntimeCronDTO{
			Enabled: true, Schedule: "0 */6 * * *", OnStart: false, Jitter: "1m",
		},
		Scan: dto.RuntimeScanDTO{
			ShutdownGrace: "60s", CooldownSweep: "15m",
		},
		DryRun: true,
		GlobalRateLimit: dto.RuntimeRateLimitDTO{RPM: 30, Burst: 10},
		Auth: dto.RuntimeAuthDTO{
			SessionTTL:     "12h",
			SecureCookie:   false,
			TrustedProxies: []string{"127.0.0.1", "::1", "10.0.0.0/8"},
		},
	}
}

func setup(t *testing.T) (*UseCase, *fakeRuntimeRepo, <-chan runtime.Snapshot) {
	t.Helper()
	repo := &fakeRuntimeRepo{}
	bus := runtime.NewBus(nil)
	t.Cleanup(bus.Close)
	ch := bus.Subscribe("test")
	uc := New(repo, fakeInstanceRepo{}, nil, bus, nil)
	return uc, repo, ch
}

func TestGet_Defaults_WhenRowMissing(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	got, ts, err := uc.Get(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "0 */6 * * *", got.Cron.Schedule)
	assert.Equal(t, "12h0m0s", got.Auth.SessionTTL)
	assert.True(t, ts.IsZero())
}

func TestUpdate_OK_PersistsAndPublishes(t *testing.T) {
	t.Parallel()
	uc, repo, ch := setup(t)
	out, ts, err := uc.Update(context.Background(), validDTO(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, repo.upserts)
	assert.False(t, ts.IsZero())
	assert.Equal(t, "12h0m0s", out.Auth.SessionTTL)
	select {
	case snap := <-ch:
		assert.Equal(t, "0 */6 * * *", snap.Cron.Schedule)
		assert.Len(t, snap.Instances, 1)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected snapshot on bus within 100ms")
	}
}

func TestUpdate_StaleIUS(t *testing.T) {
	t.Parallel()
	uc, repo, _ := setup(t)
	_, _, err := uc.Update(context.Background(), validDTO(), nil)
	require.NoError(t, err)
	// Force stored row to be "in the future" — any IUS in the past is stale.
	repo.mu.Lock()
	repo.row.UpdatedAt = time.Now().UTC().Add(time.Hour)
	repo.mu.Unlock()
	past := time.Now().UTC().Add(-time.Hour)
	_, _, err = uc.Update(context.Background(), validDTO(), &past)
	assert.ErrorIs(t, err, ErrStaleWrite)
}

func TestValidate_InvalidCron(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validDTO()
	in.Cron.Schedule = "not a cron"
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_CRON", verr.Code)
}

func TestValidate_BadCIDR(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validDTO()
	in.Auth.TrustedProxies = []string{"127.0.0.1", "not.an.ip", "10.0.0.0/8"}
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_TRUSTED_PROXY", verr.Code)
	assert.Contains(t, verr.Message, "not.an.ip")
}

func TestValidate_SessionTTLTooShort(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validDTO()
	in.Auth.SessionTTL = "1m"
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_SESSION_TTL", verr.Code)
}

func TestValidate_SessionTTLTooLong(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validDTO()
	in.Auth.SessionTTL = "200h" // beyond 7d (168h) ceiling
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_SESSION_TTL", verr.Code)
}

func TestValidate_NegativeRateLimit(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validDTO()
	in.GlobalRateLimit.RPM = -1
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_RATE_LIMIT", verr.Code)
}

func TestValidate_BadDurationString(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validDTO()
	in.Scan.ShutdownGrace = "thirty seconds"
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_DURATION", verr.Code)
}

func TestValidate_NegativeJitter(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validDTO()
	in.Cron.Jitter = "-1s"
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_JITTER", verr.Code)
}
