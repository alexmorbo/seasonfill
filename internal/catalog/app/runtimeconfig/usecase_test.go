package runtimeconfig

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
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

func (f *fakeRuntimeRepo) Upsert(_ context.Context, snap runtime.Snapshot, ifUnmodifiedSince *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return f.upsertErr
	}
	if ifUnmodifiedSince != nil && f.exists {
		stored := f.row.UpdatedAt.Truncate(time.Second)
		provided := ifUnmodifiedSince.Truncate(time.Second)
		if stored.After(provided) {
			return ports.ErrStaleWrite
		}
	}
	f.upserts++
	f.row = ports.RuntimeConfigRow{
		Cron: snap.Cron, Scan: snap.Scan, DryRun: snap.DryRun,
		GlobalRateLimit: snap.GlobalRateLimit, Auth: snap.Auth,
		GUIDRewrites: append([]runtime.GUIDRewriteRule(nil), snap.GUIDRewrites...),
		UpdatedAt:    time.Now().UTC(),
	}
	f.exists = true
	return nil
}

func (f *fakeRuntimeRepo) SaveAPIKey(_ context.Context, _ []byte, _ bool) error {
	return nil
}

func (f *fakeRuntimeRepo) UpsertOIDCSecret(_ context.Context, _ string) error {
	return nil
}

func (f *fakeRuntimeRepo) DecryptOIDCSecret(_ context.Context) (string, error) {
	return "", nil
}

func (f *fakeRuntimeRepo) GetTimezone(_ context.Context) (string, error) { return "", nil }

func (f *fakeRuntimeRepo) SetTimezone(_ context.Context, _ string) error { return nil }

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
func (fakeInstanceRepo) UpdateWithOptions(_ context.Context, _ runtime.InstanceSnapshot, _ *crypto.Cipher, _ bool, _ *time.Time) error {
	return nil
}
func (fakeInstanceRepo) Delete(_ context.Context, _ string) error { return nil }
func (fakeInstanceRepo) GetUpdatedAt(_ context.Context, _ string) (time.Time, error) {
	return time.Time{}, ports.ErrNotFound
}

func validInput() Input {
	return Input{
		Cron: CronInput{
			Enabled: true, Schedule: "0 */6 * * *", OnStart: false,
			Jitter: time.Minute,
		},
		Scan: ScanInput{
			ShutdownGrace: 60 * time.Second,
			CooldownSweep: 15 * time.Minute,
		},
		DryRun:          true,
		GlobalRateLimit: GlobalRateLimitInput{RPM: 30, Burst: 10},
		Auth: AuthInput{
			SessionTTL:     12 * time.Hour,
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
	assert.Equal(t, 12*time.Hour, got.Auth.SessionTTL)
	assert.True(t, ts.IsZero())
}

func TestUpdate_OK_PersistsAndPublishes(t *testing.T) {
	t.Parallel()
	uc, repo, ch := setup(t)
	out, ts, err := uc.Update(context.Background(), validInput(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, repo.upserts)
	assert.False(t, ts.IsZero())
	assert.Equal(t, 12*time.Hour, out.Auth.SessionTTL)
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
	_, _, err := uc.Update(context.Background(), validInput(), nil)
	require.NoError(t, err)
	// Force stored row to be "in the future" — any IUS in the past is stale.
	repo.mu.Lock()
	repo.row.UpdatedAt = time.Now().UTC().Add(time.Hour)
	repo.mu.Unlock()
	past := time.Now().UTC().Add(-time.Hour)
	_, _, err = uc.Update(context.Background(), validInput(), &past)
	assert.ErrorIs(t, err, ErrStaleWrite)
}

func TestValidate_InvalidCron(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.Cron.Schedule = "not a cron"
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_CRON", verr.Code)
}

func TestValidate_BadCIDR(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.Auth.TrustedProxies = []string{"127.0.0.1", "not.an.ip", "10.0.0.0/8"}
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_TRUSTED_PROXY", verr.Code)
	assert.Contains(t, verr.Message, "not.an.ip")
}

// TestValidate_TrustedProxy_TooBroad locks B-4: 0.0.0.0/0, ::/0, the
// bare unspecified IPs (0.0.0.0, ::) all match the entire address
// space and would defeat the proxy allow-list. The new
// INVALID_TRUSTED_PROXY_TOO_BROAD sentinel calls them out
// separately from the generic "neither IP nor CIDR" branch.
func TestValidate_TrustedProxy_TooBroad(t *testing.T) {
	t.Parallel()
	cases := []string{"0.0.0.0/0", "::/0", "0.0.0.0", "::"}
	for _, entry := range cases {
		t.Run(entry, func(t *testing.T) {
			t.Parallel()
			uc, _, _ := setup(t)
			in := validInput()
			in.Auth.TrustedProxies = []string{entry}
			_, _, err := uc.Update(context.Background(), in, nil)
			var verr *ValidationError
			require.ErrorAs(t, err, &verr)
			assert.Equal(t, "INVALID_TRUSTED_PROXY_TOO_BROAD", verr.Code,
				"entry=%q must be rejected as too broad", entry)
		})
	}
}

// TestValidate_TrustedProxies_TooMany locks B-4: a list longer than
// trustedProxiesMaxLen is rejected before per-entry parsing so a
// caller can't blow the gin XFF parser with arbitrary input.
func TestValidate_TrustedProxies_TooMany(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	long := make([]string, trustedProxiesMaxLen+1)
	for i := range long {
		long[i] = "127.0.0.1"
	}
	in.Auth.TrustedProxies = long
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_TRUSTED_PROXIES_TOO_MANY", verr.Code)
}

func TestValidate_SessionTTLTooShort(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.Auth.SessionTTL = time.Minute
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_SESSION_TTL", verr.Code)
}

func TestValidate_SessionTTLTooLong(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.Auth.SessionTTL = 200 * time.Hour // beyond 7d (168h) ceiling
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_SESSION_TTL", verr.Code)
}

func TestValidate_NegativeRateLimit(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.GlobalRateLimit.RPM = -1
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_RATE_LIMIT", verr.Code)
}

func TestValidate_NegativeJitter(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.Cron.Jitter = -time.Second
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_JITTER_OUT_OF_RANGE", verr.Code)
}

// --- 028h-1: range-boundary tests ---

// rangeCase is a small typed helper for table-driven boundary tests.
// Each case mutates a copy of validInput() to put exactly one field
// out of range; the test asserts the matching sentinel code fires.
type rangeCase struct {
	name    string
	mutate  func(*Input)
	code    string
	wantErr bool
}

func TestValidate_RangeBounds_Runtime(t *testing.T) {
	t.Parallel()
	cases := []rangeCase{
		// scan.shutdown_grace ∈ [1s, 10m]
		{"shutdown_grace_below_min",
			func(d *Input) { d.Scan.ShutdownGrace = 500 * time.Millisecond },
			"INVALID_SCAN_SHUTDOWN_GRACE_OUT_OF_RANGE", true},
		{"shutdown_grace_at_min",
			func(d *Input) { d.Scan.ShutdownGrace = time.Second },
			"", false},
		{"shutdown_grace_at_max",
			func(d *Input) { d.Scan.ShutdownGrace = 10 * time.Minute },
			"", false},
		{"shutdown_grace_above_max",
			func(d *Input) { d.Scan.ShutdownGrace = 11 * time.Minute },
			"INVALID_SCAN_SHUTDOWN_GRACE_OUT_OF_RANGE", true},

		// scan.cooldown_sweep ∈ [10s, 24h]
		{"cooldown_sweep_below_min",
			func(d *Input) { d.Scan.CooldownSweep = time.Second },
			"INVALID_SCAN_COOLDOWN_SWEEP_OUT_OF_RANGE", true},
		{"cooldown_sweep_at_min",
			func(d *Input) { d.Scan.CooldownSweep = 10 * time.Second },
			"", false},
		{"cooldown_sweep_at_max",
			func(d *Input) { d.Scan.CooldownSweep = 24 * time.Hour },
			"", false},
		{"cooldown_sweep_above_max",
			func(d *Input) { d.Scan.CooldownSweep = 25 * time.Hour },
			"INVALID_SCAN_COOLDOWN_SWEEP_OUT_OF_RANGE", true},

		// cron.jitter ∈ [0, 1h]
		{"jitter_at_min",
			func(d *Input) { d.Cron.Jitter = 0 },
			"", false},
		{"jitter_at_max",
			func(d *Input) { d.Cron.Jitter = time.Hour },
			"", false},
		{"jitter_above_max",
			func(d *Input) { d.Cron.Jitter = 2 * time.Hour },
			"INVALID_JITTER_OUT_OF_RANGE", true},

		// global_rate_limit.rpm ∈ [0, 10000]
		{"rpm_at_min",
			func(d *Input) { d.GlobalRateLimit.RPM = 0 },
			"", false},
		{"rpm_at_max",
			func(d *Input) { d.GlobalRateLimit.RPM = 10000 },
			"", false},
		{"rpm_above_max",
			func(d *Input) { d.GlobalRateLimit.RPM = 10001 },
			"INVALID_RATE_LIMIT_RPM_OUT_OF_RANGE", true},
		{"rpm_far_above_max",
			func(d *Input) { d.GlobalRateLimit.RPM = 2147483647 },
			"INVALID_RATE_LIMIT_RPM_OUT_OF_RANGE", true},

		// global_rate_limit.burst ∈ [0, 10000]
		{"burst_above_max",
			func(d *Input) { d.GlobalRateLimit.Burst = 10001 },
			"INVALID_RATE_LIMIT_BURST_OUT_OF_RANGE", true},
		{"burst_far_above_max",
			func(d *Input) { d.GlobalRateLimit.Burst = 2147483647 },
			"INVALID_RATE_LIMIT_BURST_OUT_OF_RANGE", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			uc, _, _ := setup(t)
			in := validInput()
			tc.mutate(&in)
			_, _, err := uc.Update(context.Background(), in, nil)
			if !tc.wantErr {
				require.NoError(t, err, "boundary value must be accepted")
				return
			}
			var verr *ValidationError
			require.ErrorAs(t, err, &verr)
			assert.Equal(t, tc.code, verr.Code)
		})
	}
}

// TestValidate_RuntimeMaxBoundary_RoundTrips is a smoke check that the
// cron parser, jitter range, sweep range, and shutdown range all line
// up — a "happy max" snapshot must round-trip clean.
func TestValidate_RuntimeMaxBoundary_RoundTrips(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.Cron.Jitter = time.Hour
	in.Scan.ShutdownGrace = 10 * time.Minute
	in.Scan.CooldownSweep = 24 * time.Hour
	in.GlobalRateLimit.RPM = 10000
	in.GlobalRateLimit.Burst = 10000
	in.Auth.SessionTTL = 168 * time.Hour
	_, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
}

// staleOnIUSRepo is a deliberately broken fake: it ignores the IUS
// pointer's value and unconditionally returns ports.ErrStaleWrite
// whenever one is provided. After the CR-1 fix the usecase MUST
// surface that as runtimeconfig.ErrStaleWrite. If the usecase were to
// short-circuit in its own Get→compare block (the regression) and
// pass nil to Upsert, this fake would happily succeed and the test
// would fail — locking the contract.
type staleOnIUSRepo struct{}

func (staleOnIUSRepo) Get(_ context.Context) (ports.RuntimeConfigRow, error) {
	return ports.RuntimeConfigRow{UpdatedAt: time.Now().UTC()}, nil
}

func (staleOnIUSRepo) Upsert(_ context.Context, _ runtime.Snapshot, ius *time.Time) error {
	if ius != nil {
		return ports.ErrStaleWrite
	}
	return nil
}

func (staleOnIUSRepo) SaveAPIKey(_ context.Context, _ []byte, _ bool) error {
	return nil
}

func (staleOnIUSRepo) UpsertOIDCSecret(_ context.Context, _ string) error {
	return nil
}

func (staleOnIUSRepo) DecryptOIDCSecret(_ context.Context) (string, error) {
	return "", nil
}

func (staleOnIUSRepo) GetTimezone(_ context.Context) (string, error) { return "", nil }

func (staleOnIUSRepo) SetTimezone(_ context.Context, _ string) error { return nil }

// --- 107: guid_rewrites validation + round-trip --------------------------

func TestValidate_GUIDRewrites_EmptyOK(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.GUIDRewrites = nil
	out, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	assert.NotNil(t, out.GUIDRewrites, "Output must surface [] not nil")
	assert.Empty(t, out.GUIDRewrites)
}

func TestValidate_GUIDRewrites_AtMax(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.GUIDRewrites = make([]runtime.GUIDRewriteRule, guidRewritesMaxLen)
	for i := range in.GUIDRewrites {
		in.GUIDRewrites[i] = runtime.GUIDRewriteRule{
			From: "http://internal" + grItoa(i),
			To:   "https://public" + grItoa(i),
		}
	}
	out, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	assert.Len(t, out.GUIDRewrites, guidRewritesMaxLen)
}

func TestValidate_GUIDRewrites_TooMany(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.GUIDRewrites = make([]runtime.GUIDRewriteRule, guidRewritesMaxLen+1)
	for i := range in.GUIDRewrites {
		in.GUIDRewrites[i] = runtime.GUIDRewriteRule{
			From: "http://internal" + grItoa(i),
			To:   "https://public",
		}
	}
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_GUID_REWRITES_TOO_MANY", verr.Code)
}

func TestValidate_GUIDRewrites_EmptyFrom(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.GUIDRewrites = []runtime.GUIDRewriteRule{
		{From: "   ", To: "https://x"},
	}
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_GUID_REWRITE_FROM_EMPTY", verr.Code)
}

func TestValidate_GUIDRewrites_Duplicate(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.GUIDRewrites = []runtime.GUIDRewriteRule{
		{From: "http://a", To: "https://x"},
		{From: "http://a", To: "https://y"},
	}
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_GUID_REWRITE_DUPLICATE_FROM", verr.Code)
}

func TestValidate_GUIDRewrites_FromTooLong(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	long := make([]byte, guidRewriteFromMaxLen+1)
	for i := range long {
		long[i] = 'x'
	}
	in.GUIDRewrites = []runtime.GUIDRewriteRule{{From: string(long), To: "y"}}
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_GUID_REWRITE_FROM_TOO_LONG", verr.Code)
}

func TestValidate_GUIDRewrites_ToTooLong(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	long := make([]byte, guidRewriteToMaxLen+1)
	for i := range long {
		long[i] = 'x'
	}
	in.GUIDRewrites = []runtime.GUIDRewriteRule{{From: "a", To: string(long)}}
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_GUID_REWRITE_TO_TOO_LONG", verr.Code)
}

func TestValidate_GUIDRewrites_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.GUIDRewrites = []runtime.GUIDRewriteRule{
		{From: "  http://internal  ", To: "  https://public  "},
	}
	out, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	require.Len(t, out.GUIDRewrites, 1)
	assert.Equal(t, "http://internal", out.GUIDRewrites[0].From)
	assert.Equal(t, "https://public", out.GUIDRewrites[0].To)
}

func TestValidate_GUIDRewrites_OrderPreserved(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.GUIDRewrites = []runtime.GUIDRewriteRule{
		{From: "http://z", To: "https://z"},
		{From: "http://a", To: "https://a"},
		{From: "http://m", To: "https://m"},
	}
	out, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	require.Len(t, out.GUIDRewrites, 3)
	assert.Equal(t, "http://z", out.GUIDRewrites[0].From)
	assert.Equal(t, "http://a", out.GUIDRewrites[1].From)
	assert.Equal(t, "http://m", out.GUIDRewrites[2].From)

	got, _, err := uc.Get(context.Background())
	require.NoError(t, err)
	require.Len(t, got.GUIDRewrites, 3)
	assert.Equal(t, "http://z", got.GUIDRewrites[0].From)
	assert.Equal(t, "http://a", got.GUIDRewrites[1].From)
	assert.Equal(t, "http://m", got.GUIDRewrites[2].From)
}

// grItoa avoids importing strconv just for the test stubs above.
func grItoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

// TestUpdate_StaleIUS_FromRepo is the CR-1 + M-2 regression test.
// The fake never compares timestamps — it just signals stale whenever
// an IUS pointer is forwarded. The usecase must:
//  1. NOT do an out-of-tx precondition Get itself.
//  2. Forward the IUS pointer verbatim to Upsert.
//  3. Translate ports.ErrStaleWrite to runtimeconfig.ErrStaleWrite.
func TestUpdate_StaleIUS_FromRepo(t *testing.T) {
	t.Parallel()
	bus := runtime.NewBus(nil)
	t.Cleanup(bus.Close)
	uc := New(staleOnIUSRepo{}, fakeInstanceRepo{}, nil, bus, nil)
	past := time.Now().UTC().Add(-time.Hour)
	_, _, err := uc.Update(context.Background(), validInput(), &past)
	require.ErrorIs(t, err, ErrStaleWrite,
		"usecase must surface ports.ErrStaleWrite from Upsert as runtimeconfig.ErrStaleWrite")
}

// TestUpdate_NoIUS_SucceedsThroughRepo is the companion: when no IUS
// is provided, the same non-checking fake must succeed. This locks
// "the usecase forwards IUS=nil verbatim and never invents one".
func TestUpdate_NoIUS_SucceedsThroughRepo(t *testing.T) {
	t.Parallel()
	bus := runtime.NewBus(nil)
	t.Cleanup(bus.Close)
	uc := New(staleOnIUSRepo{}, fakeInstanceRepo{}, nil, bus, nil)
	_, _, err := uc.Update(context.Background(), validInput(), nil)
	require.NoError(t, err)
}

func TestUsecase_EpochUnchangedWhenAuthFieldsStable(t *testing.T) {
	t.Parallel()
	repo := &fakeRuntimeRepo{}
	uc := New(repo, fakeInstanceRepo{}, nil, runtime.NewBus(nil), nil)
	in := validInput()
	_, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	first := repo.row.Auth.SessionEpoch

	// Change a non-auth field — epoch must stay put.
	in.Cron.Schedule = "0 0 * * *"
	_, _, err = uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	assert.Equal(t, first, repo.row.Auth.SessionEpoch)
}

// validOIDCInput returns a minimal-valid set of OIDC fields for use in
// OIDC configuration tests. Callers may further mutate individual sub-fields.
func validOIDCInput() OIDCInput {
	secret := "test-client-secret"
	return OIDCInput{
		Issuer:        "https://sso.example.com",
		ClientID:      "seasonfill",
		ClientSecret:  &secret,
		RedirectURL:   "https://app.example.com/callback",
		Scopes:        []string{"openid", "profile", "email"},
		UsernameClaim: "preferred_username",
		AllowedGroups: []string{},
		GroupsClaim:   "groups",
	}
}

// --- OIDC validation tests ---

func TestValidate_OIDC_MissingIssuer(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.Auth.OIDC = validOIDCInput()
	in.Auth.OIDC.Issuer = ""
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_OIDC_ISSUER", verr.Code)
}

func TestValidate_OIDC_MissingClientID(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.Auth.OIDC = validOIDCInput()
	in.Auth.OIDC.ClientID = ""
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_OIDC_CLIENT_ID", verr.Code)
}

func TestValidate_OIDC_MissingRedirectURL_NowOptional(t *testing.T) {
	// AC-B5: redirect_url is optional when mode=oidc — Start derives it.
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.Auth.OIDC = validOIDCInput()
	in.Auth.OIDC.RedirectURL = ""
	_, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
}

func TestValidate_OIDC_ScopesWithoutOpenID(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.Auth.OIDC = validOIDCInput()
	in.Auth.OIDC.Scopes = []string{"profile", "email"} // missing "openid"
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_OIDC_SCOPES", verr.Code)
}

func TestValidate_OIDC_AllowedGroupsTooMany(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.Auth.OIDC = validOIDCInput()
	groups := make([]string, 65)
	for i := range groups {
		groups[i] = "group"
	}
	in.Auth.OIDC.AllowedGroups = groups
	_, _, err := uc.Update(context.Background(), in, nil)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_OIDC_GROUPS_TOO_MANY", verr.Code)
}

// TestValidate_OIDC_EmptyFieldsOK confirms that an entirely-empty OIDC
// subtree passes validation — OIDC simply stays unconfigured / not ready.
func TestValidate_OIDC_EmptyFieldsOK(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	in := validInput()
	in.Auth.OIDC = OIDCInput{} // all empty
	_, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err, "empty OIDC must not fail")
}

// TestValidate_OIDC_PartialNotConfiguring_NoError reproduces the live
// prod symptom that triggered B-33 (story 481): an env OIDC_CLIENT_SECRET
// override with otherwise-empty issuer/client_id used to fail validation,
// blocking ALL runtime config saves on /settings. With the presence-gate,
// an unconfigured OIDC subtree (no issuer/client_id/incoming secret) passes.
func TestValidate_OIDC_PartialNotConfiguring_NoError(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	uc.WithClientSecretEnv("env-injected-secret")
	in := validInput()
	in.Auth.OIDC = OIDCInput{
		// Issuer/ClientID empty (DB cleared), redirect_url empty, no
		// incoming secret → not "configuring", so validation is skipped.
		Scopes: []string{"openid"},
	}
	_, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err, "env-only OIDC secret with empty issuer/client_id must save")
}

// TestValidate_OIDC_FullConfig_NoError confirms a fully-configured OIDC
// subtree passes and persists.
func TestValidate_OIDC_FullConfig_NoError(t *testing.T) {
	t.Parallel()
	uc, _, _ := setup(t)
	uc.WithClientSecretEnv("env-injected-secret")
	in := validInput()
	in.Auth.OIDC = validOIDCInput()
	_, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err, "full OIDC config must save")
}

// TestValidate_OIDC_PresenceGate locks the presence-gate: OIDC validation
// only kicks in once the operator starts configuring OIDC (issuer /
// client_id / an incoming client_secret present).
func TestValidate_OIDC_PresenceGate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		envSecret string
		oidc      OIDCInput
		wantErr   string
	}{
		{
			name: "all empty OIDC → OK (not configuring)",
			oidc: OIDCInput{Scopes: []string{"openid"}},
		},
		{
			name:      "full OIDC + env secret → OK",
			envSecret: "env-s",
			oidc: OIDCInput{
				Issuer: "https://kc.example.com", ClientID: "sf",
				Scopes: []string{"openid"},
			},
		},
		{
			name:      "redirect_url blank, env secret → OK (auto-derive)",
			envSecret: "env-s",
			oidc: OIDCInput{
				Issuer: "https://kc.example.com", ClientID: "sf",
				Scopes: []string{"openid"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			uc, _, _ := setup(t)
			if tc.envSecret != "" {
				uc.WithClientSecretEnv(tc.envSecret)
			}
			in := validInput()
			in.Auth.OIDC = tc.oidc
			_, _, err := uc.Update(context.Background(), in, nil)
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				var verr *ValidationError
				require.ErrorAs(t, err, &verr)
				assert.Equal(t, tc.wantErr, verr.Code)
			}
		})
	}
}

// TestUsecase_EpochBumpsOnOIDCIssuerChange confirms that changing the
// OIDC issuer URL forces session invalidation.
func TestUsecase_EpochBumpsOnOIDCIssuerChange(t *testing.T) {
	t.Parallel()
	repo := &fakeRuntimeRepo{}
	clock := time.Unix(0, 1_000_000_000).UTC()
	uc := New(repo, fakeInstanceRepo{}, nil, runtime.NewBus(nil), nil).
		WithClock(func() time.Time { return clock })
	in := validInput()
	in.Auth.OIDC = validOIDCInput()
	_, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	first := repo.row.Auth.SessionEpoch

	clock = time.Unix(0, 2_000_000_000).UTC()
	in.Auth.OIDC.Issuer = "https://other-sso.example.com"
	_, _, err = uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	assert.Greater(t, repo.row.Auth.SessionEpoch, first,
		"OIDC issuer change MUST bump epoch")
}

// TestUsecase_EpochBumpsOnOIDCClientIDChange confirms that changing the
// OIDC client_id forces session invalidation.
func TestUsecase_EpochBumpsOnOIDCClientIDChange(t *testing.T) {
	t.Parallel()
	repo := &fakeRuntimeRepo{}
	clock := time.Unix(0, 1_000_000_000).UTC()
	uc := New(repo, fakeInstanceRepo{}, nil, runtime.NewBus(nil), nil).
		WithClock(func() time.Time { return clock })
	in := validInput()
	in.Auth.OIDC = validOIDCInput()
	_, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	first := repo.row.Auth.SessionEpoch

	clock = time.Unix(0, 2_000_000_000).UTC()
	in.Auth.OIDC.ClientID = "new-client-id"
	_, _, err = uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	assert.Greater(t, repo.row.Auth.SessionEpoch, first,
		"OIDC client_id change MUST bump epoch")
}

// TestUsecase_EpochBumpsOnOIDCScopesChange confirms that changing the
// OIDC scopes list forces session invalidation.
func TestUsecase_EpochBumpsOnOIDCScopesChange(t *testing.T) {
	t.Parallel()
	repo := &fakeRuntimeRepo{}
	clock := time.Unix(0, 1_000_000_000).UTC()
	uc := New(repo, fakeInstanceRepo{}, nil, runtime.NewBus(nil), nil).
		WithClock(func() time.Time { return clock })
	in := validInput()
	in.Auth.OIDC = validOIDCInput()
	_, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	first := repo.row.Auth.SessionEpoch

	clock = time.Unix(0, 2_000_000_000).UTC()
	in.Auth.OIDC.Scopes = []string{"openid", "groups"} // different set
	_, _, err = uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	assert.Greater(t, repo.row.Auth.SessionEpoch, first,
		"OIDC scopes change MUST bump epoch")
}

// TestUsecase_EpochBumpsOnOIDCAllowedGroupsChange confirms that changing
// the allowed_groups list forces session invalidation.
func TestUsecase_EpochBumpsOnOIDCAllowedGroupsChange(t *testing.T) {
	t.Parallel()
	repo := &fakeRuntimeRepo{}
	clock := time.Unix(0, 1_000_000_000).UTC()
	uc := New(repo, fakeInstanceRepo{}, nil, runtime.NewBus(nil), nil).
		WithClock(func() time.Time { return clock })
	in := validInput()
	in.Auth.OIDC = validOIDCInput()
	_, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	first := repo.row.Auth.SessionEpoch

	clock = time.Unix(0, 2_000_000_000).UTC()
	in.Auth.OIDC.AllowedGroups = []string{"admins"}
	_, _, err = uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	assert.Greater(t, repo.row.Auth.SessionEpoch, first,
		"OIDC allowed_groups change MUST bump epoch")
}

// TestUsecase_EpochUnchangedWhenOIDCFieldsStable confirms that re-saving
// identical OIDC values does NOT bump the epoch. Companion to the
// "bumps" tests — we lock both directions.
func TestUsecase_EpochUnchangedWhenOIDCFieldsStable(t *testing.T) {
	t.Parallel()
	repo := &fakeRuntimeRepo{}
	uc := New(repo, fakeInstanceRepo{}, nil, runtime.NewBus(nil), nil).
		WithClientSecretEnv("env-secret") // env-secret satisfies OIDC_CLIENT_SECRET_MISSING guard
	in := validInput()
	in.Auth.OIDC = validOIDCInput()
	in.Auth.OIDC.ClientSecret = nil // preserve, not dirty
	_, _, err := uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	first := repo.row.Auth.SessionEpoch

	// Identical OIDC fields — only a non-auth field changes. ClientSecret nil = no bump.
	in.Cron.Schedule = "0 0 * * *"
	_, _, err = uc.Update(context.Background(), in, nil)
	require.NoError(t, err)
	assert.Equal(t, first, repo.row.Auth.SessionEpoch,
		"stable OIDC fields must NOT bump epoch")
}
