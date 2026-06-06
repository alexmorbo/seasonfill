package instance

import (
	"bytes"
	"context"
	"log/slog"
	"math"
	"strings"
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
	return inst.ID, nil
}
func (f *fakeInstanceRepo) UpdateWithOptions(
	_ context.Context,
	inst runtime.InstanceSnapshot,
	_ *crypto.Cipher,
	preserve bool,
	ifUnmodifiedSince *time.Time,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	if preserve {
		f.preserveCalls++
	}
	// Simulate the repo's in-tx precondition: stored is the recorded
	// f.updated entry; if header is strictly older than stored (at
	// second resolution) → ErrStaleWrite.
	if ifUnmodifiedSince != nil {
		stored, ok := f.updated[inst.Name]
		if ok {
			s := stored.Truncate(time.Second)
			p := ifUnmodifiedSince.Truncate(time.Second)
			if s.After(p) {
				return ports.ErrStaleWrite
			}
		}
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
	return nil
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
func (f *fakeRuntimeRepo) Upsert(_ context.Context, _ runtime.Snapshot, _ *time.Time) error {
	return nil
}
func (f *fakeRuntimeRepo) SaveAPIKey(_ context.Context, _ []byte, _ bool) error { return nil }
func (f *fakeRuntimeRepo) UpsertOIDCSecret(_ context.Context, _ string) error   { return nil }
func (f *fakeRuntimeRepo) DecryptOIDCSecret(_ context.Context) (string, error)  { return "", nil }

func setup(t *testing.T) (*UseCase, *fakeInstanceRepo, *runtime.Bus, <-chan runtime.Snapshot) {
	t.Helper()
	repo := newFakeRepo()
	bus := runtime.NewBus(nil)
	t.Cleanup(bus.Close)
	ch := bus.Subscribe("test")
	uc := New(repo, &fakeRuntimeRepo{}, nil, bus, nil)
	return uc, repo, bus, ch
}

// REPLACE the existing validSnap helper with this version. Sets all
// fields that the new range checks require to be non-zero: Timeout,
// SearchTimeout, and health_check intervals (whose min is 10s).
func validSnap(name string) runtime.InstanceSnapshot {
	return runtime.InstanceSnapshot{
		Name:          name,
		URL:           "http://sonarr:8989",
		APIKey:        "abc",
		Timeout:       10 * time.Second,
		SearchTimeout: 60 * time.Second,
		HealthCheck: runtime.HealthCheckSnapshot{
			RecheckAuth:    5 * time.Minute,
			RecheckNetwork: time.Minute,
		},
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
		runtime.InstanceSnapshot{Name: "beta", URL: "http://x", APIKey: "k"}, nil)
	assert.ErrorIs(t, err, ErrNameImmutable)
}

func TestUpdate_EmptyKey_PreservesSecret(t *testing.T) {
	t.Parallel()
	uc, repo, _, _ := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	upd := validSnap("alpha")
	upd.APIKey = ""
	require.NoError(t, uc.Update(context.Background(), "alpha", upd, nil))
	assert.Equal(t, 1, repo.preserveCalls, "preserveSecret must be true when api_key is empty")
}

func TestUpdate_StaleIfUnmodifiedSince(t *testing.T) {
	t.Parallel()
	uc, repo, _, _ := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	repo.updated["alpha"] = time.Now().UTC()
	past := time.Now().UTC().Add(-time.Hour)
	err := uc.Update(context.Background(), "alpha", validSnap("alpha"), &past)
	assert.ErrorIs(t, err, ErrStaleWrite)
}

func TestUpdate_NoHeader_LWWProceeds(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	err := uc.Update(context.Background(), "alpha", validSnap("alpha"), nil)
	assert.NoError(t, err)
}

func TestDelete_SoleInstance_Succeeds(t *testing.T) {
	t.Parallel()
	uc, repo, _, ch := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	<-ch // drain create snapshot
	require.NoError(t, uc.Delete(context.Background(), "alpha"))
	assert.Equal(t, 1, repo.deleteCalls)
	select {
	case snap := <-ch:
		assert.Len(t, snap.Instances, 0)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("delete must publish a snapshot")
	}
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

// --- 028h-1: range-boundary tests ---

type instanceRangeCase struct {
	name    string
	mutate  func(*runtime.InstanceSnapshot)
	code    string
	wantErr bool
}

func TestValidate_RangeBounds_Instance(t *testing.T) {
	t.Parallel()
	cases := []instanceRangeCase{
		// timeout_sec ∈ [1s, 300s]
		{"timeout_at_min",
			func(s *runtime.InstanceSnapshot) { s.Timeout = 1 * time.Second },
			"", false},
		{"timeout_at_max",
			func(s *runtime.InstanceSnapshot) { s.Timeout = 300 * time.Second },
			"", false},
		{"timeout_above_max",
			func(s *runtime.InstanceSnapshot) { s.Timeout = 301 * time.Second },
			"INVALID_INSTANCE_TIMEOUT_OUT_OF_RANGE", true},

		// search_timeout_sec ∈ [1s, 600s]
		{"search_timeout_at_max",
			func(s *runtime.InstanceSnapshot) { s.SearchTimeout = 600 * time.Second },
			"", false},
		{"search_timeout_above_max",
			func(s *runtime.InstanceSnapshot) { s.SearchTimeout = 601 * time.Second },
			"INVALID_INSTANCE_SEARCH_TIMEOUT_OUT_OF_RANGE", true},

		// rate_limit_rpm ∈ [0, 10000]
		{"rate_limit_rpm_at_min",
			func(s *runtime.InstanceSnapshot) { s.RateLimit.RPM = 0 },
			"", false},
		{"rate_limit_rpm_at_max",
			func(s *runtime.InstanceSnapshot) { s.RateLimit.RPM = 10000 },
			"", false},
		{"rate_limit_rpm_above_max",
			func(s *runtime.InstanceSnapshot) { s.RateLimit.RPM = 10001 },
			"INVALID_INSTANCE_RATE_LIMIT_RPM_OUT_OF_RANGE", true},

		// rate_limit_burst ∈ [0, 10000]
		{"rate_limit_burst_above_max",
			func(s *runtime.InstanceSnapshot) { s.RateLimit.Burst = 10001 },
			"INVALID_INSTANCE_RATE_LIMIT_BURST_OUT_OF_RANGE", true},

		// cooldown.series_after_grab ∈ [0, 168h]
		{"cooldown_series_above_max",
			func(s *runtime.InstanceSnapshot) { s.Cooldown.SeriesAfterGrab = 169 * time.Hour },
			"INVALID_INSTANCE_COOLDOWN_SERIES_OUT_OF_RANGE", true},
		{"cooldown_series_at_max",
			func(s *runtime.InstanceSnapshot) { s.Cooldown.SeriesAfterGrab = 168 * time.Hour },
			"", false},

		// cooldown.guid_after_failed_grab ∈ [0, 168h]
		{"cooldown_guid_grab_above_max",
			func(s *runtime.InstanceSnapshot) { s.Cooldown.GUIDAfterFailedGrab = 169 * time.Hour },
			"INVALID_INSTANCE_COOLDOWN_GUID_GRAB_OUT_OF_RANGE", true},

		// cooldown.guid_after_failed_import ∈ [0, 168h]
		{"cooldown_guid_import_above_max",
			func(s *runtime.InstanceSnapshot) { s.Cooldown.GUIDAfterFailedImport = 169 * time.Hour },
			"INVALID_INSTANCE_COOLDOWN_GUID_IMPORT_OUT_OF_RANGE", true},

		// retry.max_attempts ∈ [0, 10]
		{"retry_attempts_at_min",
			func(s *runtime.InstanceSnapshot) { s.Retry.MaxAttempts = 0 },
			"", false},
		{"retry_attempts_at_max",
			func(s *runtime.InstanceSnapshot) { s.Retry.MaxAttempts = 10 },
			"", false},
		{"retry_attempts_above_max",
			func(s *runtime.InstanceSnapshot) { s.Retry.MaxAttempts = 11 },
			"INVALID_INSTANCE_RETRY_MAX_ATTEMPTS_OUT_OF_RANGE", true},

		// retry.initial_backoff ∈ [0, 1h]
		{"retry_initial_above_max",
			func(s *runtime.InstanceSnapshot) { s.Retry.InitialBackoff = 2 * time.Hour },
			"INVALID_INSTANCE_RETRY_INITIAL_BACKOFF_OUT_OF_RANGE", true},

		// retry.max_backoff ∈ [0, 1h]
		{"retry_max_above_max",
			func(s *runtime.InstanceSnapshot) { s.Retry.MaxBackoff = 2 * time.Hour },
			"INVALID_INSTANCE_RETRY_MAX_BACKOFF_OUT_OF_RANGE", true},

		// health_check.recheck_auth ∈ [10s, 24h]
		{"health_auth_below_min",
			func(s *runtime.InstanceSnapshot) { s.HealthCheck.RecheckAuth = 5 * time.Second },
			"INVALID_INSTANCE_HEALTH_RECHECK_AUTH_OUT_OF_RANGE", true},
		{"health_auth_at_min",
			func(s *runtime.InstanceSnapshot) { s.HealthCheck.RecheckAuth = 10 * time.Second },
			"", false},
		{"health_auth_above_max",
			func(s *runtime.InstanceSnapshot) { s.HealthCheck.RecheckAuth = 25 * time.Hour },
			"INVALID_INSTANCE_HEALTH_RECHECK_AUTH_OUT_OF_RANGE", true},

		// health_check.recheck_network ∈ [10s, 24h]
		{"health_net_below_min",
			func(s *runtime.InstanceSnapshot) { s.HealthCheck.RecheckNetwork = 5 * time.Second },
			"INVALID_INSTANCE_HEALTH_RECHECK_NET_OUT_OF_RANGE", true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			uc, _, _, _ := setup(t)
			snap := validSnap("alpha")
			tc.mutate(&snap)
			err := uc.Create(context.Background(), snap)
			if !tc.wantErr {
				require.NoError(t, err, "boundary value must be accepted")
				return
			}
			var verr *ValidationError
			require.ErrorAs(t, err, &verr)
			assert.Equal(t, tc.code, verr.Code)
			assert.ErrorIs(t, err, ErrValidation,
				"ValidationError must unwrap to ErrValidation for legacy callers")
		})
	}
}

// TestValidate_InstanceLegacyCallersUnwrap proves the new typed
// ValidationError keeps `errors.Is(err, ErrValidation)` true so the
// existing HTTP handler / test code paths stay compatible.
func TestValidate_InstanceLegacyCallersUnwrap(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup(t)
	bad := runtime.InstanceSnapshot{
		Name:          "has space",
		URL:           "http://x",
		APIKey:        "k",
		Timeout:       10 * time.Second,
		SearchTimeout: 60 * time.Second,
	}
	err := uc.Create(context.Background(), bad)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidation,
		"legacy callers must keep working via errors.Is")
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_INSTANCE_NAME", verr.Code)
}

// TestCreate_OmitsHealthCheck_GetsDefaults locks the H-3 contract:
// a snapshot whose health_check intervals are zero (the DTO layer's
// "omitted" representation) must survive validation and round-trip
// through Get as the defaulted values from ApplyInstanceDefaults.
//
// Before H-3, validate ran on the raw snapshot and rejected zero
// values as out-of-range. After H-3, ApplyInstanceDefaults runs
// first and zero → 5m / 1m, which passes the [10s, 24h] bound.
func TestCreate_OmitsHealthCheck_GetsDefaults(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup(t)
	// Snapshot with NO health_check fields set — pre-H-3 this was
	// guaranteed-rejected by validate. Timeout / SearchTimeout are
	// also zero to prove defaults flow through every range-checked
	// field, not just health_check.
	snap := runtime.InstanceSnapshot{
		Name: "alpha", URL: "http://sonarr:8989", APIKey: "abc",
	}
	require.NoError(t, uc.Create(context.Background(), snap))

	got, _, err := uc.Get(context.Background(), "alpha")
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, got.Timeout,
		"zero Timeout must default to 10s after H-3")
	assert.Equal(t, 60*time.Second, got.SearchTimeout,
		"zero SearchTimeout must default to 60s (Timeout*6) after H-3")
	assert.Equal(t, 5*time.Minute, got.HealthCheck.RecheckAuth,
		"zero RecheckAuth must default to 5m after H-3")
	assert.Equal(t, time.Minute, got.HealthCheck.RecheckNetwork,
		"zero RecheckNetwork must default to 1m after H-3")
	assert.Equal(t, "smart", got.Cooldown.Mode,
		"empty Cooldown.Mode must default to 'smart'")
	assert.Equal(t, "auto", got.Mode,
		"empty Mode must default to 'auto'")
}

// TestCreate_ReservedName_TypedError locks L-3: the reserved-name
// branch now returns a typed *ValidationError with the new
// INVALID_INSTANCE_NAME_RESERVED code, while still unwrapping to
// ErrValidation for legacy callers.
func TestCreate_ReservedName_TypedError(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup(t)
	bad := validSnap("test")
	err := uc.Create(context.Background(), bad)
	require.Error(t, err)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "INVALID_INSTANCE_NAME_RESERVED", verr.Code)
	assert.ErrorIs(t, err, ErrValidation)
}

// TestCreate_SearchTimeoutClampsToMax locks the B-1 fix: when the
// caller sends Timeout=300s (the max) and SearchTimeout=0, the
// defaulter would naively set SearchTimeout = 6*300s = 1800s, which
// then fails the [1s,600s] validator. The clamp must hold the
// default at instanceSearchTimeoutMax so Create succeeds and the
// stored value matches the validator's ceiling.
func TestCreate_SearchTimeoutClampsToMax(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup(t)
	snap := validSnap("alpha")
	snap.Timeout = 300 * time.Second
	snap.SearchTimeout = 0 // force the default branch
	require.NoError(t, uc.Create(context.Background(), snap))
	got, _, err := uc.Get(context.Background(), "alpha")
	require.NoError(t, err)
	assert.Equal(t, instanceSearchTimeoutMax, got.SearchTimeout,
		"derived SearchTimeout must be clamped to the validator max")
}

// TestValidate_RangeBounds_NewlyBoundedFields locks B-2: the four
// previously-unbounded fields now reject out-of-range inputs with
// dedicated sentinel codes (so the SPA can map each to a specific
// field-level toast).
func TestValidate_RangeBounds_NewlyBoundedFields(t *testing.T) {
	t.Parallel()
	cases := []instanceRangeCase{
		// search.min_custom_format_score ∈ [-1000, 1000]
		{"min_cf_score_below_min",
			func(s *runtime.InstanceSnapshot) { s.Search.MinCustomFormatScore = -1001 },
			"INVALID_INSTANCE_MIN_CUSTOM_FORMAT_SCORE_OUT_OF_RANGE", true},
		{"min_cf_score_above_max",
			func(s *runtime.InstanceSnapshot) { s.Search.MinCustomFormatScore = 1001 },
			"INVALID_INSTANCE_MIN_CUSTOM_FORMAT_SCORE_OUT_OF_RANGE", true},
		{"min_cf_score_at_min",
			func(s *runtime.InstanceSnapshot) { s.Search.MinCustomFormatScore = -1000 },
			"", false},
		{"min_cf_score_at_max",
			func(s *runtime.InstanceSnapshot) { s.Search.MinCustomFormatScore = 1000 },
			"", false},

		// limits.scan_max_series ∈ [0, 100000]
		{"scan_max_series_below_min",
			func(s *runtime.InstanceSnapshot) { s.Limits.ScanMaxSeries = -1 },
			"INVALID_INSTANCE_SCAN_MAX_SERIES_OUT_OF_RANGE", true},
		{"scan_max_series_above_max",
			func(s *runtime.InstanceSnapshot) { s.Limits.ScanMaxSeries = 100001 },
			"INVALID_INSTANCE_SCAN_MAX_SERIES_OUT_OF_RANGE", true},

		// limits.max_grabs_per_scan ∈ [0, 100]
		{"max_grabs_above_max",
			func(s *runtime.InstanceSnapshot) { s.Limits.MaxGrabsPerScan = 101 },
			"INVALID_INSTANCE_MAX_GRABS_PER_SCAN_OUT_OF_RANGE", true},
		{"max_grabs_below_min",
			func(s *runtime.InstanceSnapshot) { s.Limits.MaxGrabsPerScan = -1 },
			"INVALID_INSTANCE_MAX_GRABS_PER_SCAN_OUT_OF_RANGE", true},

		// ranking.origin_bonus ∈ [-100, 100]
		{"origin_bonus_above_max",
			func(s *runtime.InstanceSnapshot) { s.Ranking.OriginBonus = 100.5 },
			"INVALID_INSTANCE_ORIGIN_BONUS_OUT_OF_RANGE", true},
		{"origin_bonus_below_min",
			func(s *runtime.InstanceSnapshot) { s.Ranking.OriginBonus = -100.5 },
			"INVALID_INSTANCE_ORIGIN_BONUS_OUT_OF_RANGE", true},
		{"origin_bonus_nan",
			func(s *runtime.InstanceSnapshot) { s.Ranking.OriginBonus = math.NaN() },
			"INVALID_INSTANCE_ORIGIN_BONUS_OUT_OF_RANGE", true},
		{"origin_bonus_pos_inf",
			func(s *runtime.InstanceSnapshot) { s.Ranking.OriginBonus = math.Inf(1) },
			"INVALID_INSTANCE_ORIGIN_BONUS_OUT_OF_RANGE", true},
		{"origin_bonus_neg_inf",
			func(s *runtime.InstanceSnapshot) { s.Ranking.OriginBonus = math.Inf(-1) },
			"INVALID_INSTANCE_ORIGIN_BONUS_OUT_OF_RANGE", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			uc, _, _, _ := setup(t)
			snap := validSnap("alpha")
			tc.mutate(&snap)
			err := uc.Create(context.Background(), snap)
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

// TestValidate_URLScheme locks B-3: the URL must parse, use http or
// https, carry no userinfo, and stay <= 512 chars.
func TestValidate_URLScheme(t *testing.T) {
	t.Parallel()
	long513 := "http://" + strings.Repeat("a", 506) // total len 513
	cases := []struct {
		name string
		url  string
	}{
		{"ftp_scheme", "ftp://sonarr:8989"},
		{"file_scheme", "file:///etc/passwd"},
		{"http_userinfo", "http://user:pass@sonarr:8989"},
		{"https_userinfo", "https://user@sonarr:8989"},
		{"raw_token", "x"},
		{"too_long", long513},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			uc, _, _, _ := setup(t)
			snap := validSnap("alpha")
			snap.URL = tc.url
			err := uc.Create(context.Background(), snap)
			var verr *ValidationError
			require.ErrorAs(t, err, &verr)
			assert.Equal(t, "INVALID_INSTANCE_URL_SCHEME", verr.Code,
				"url=%q must be rejected", tc.url)
		})
	}
}

// TestUpdate_MaskedKey_PreservesSecret guards the 032b regression: a
// frontend that leaks a masked GET response ("***") into the PUT body
// must NOT cause a re-encrypt of the placeholder over the stored key.
// The use case must treat the masked shape as "preserve" and emit a
// structured warning so the regression is observable.
func TestUpdate_MaskedKey_PreservesSecret(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	bus := runtime.NewBus(nil)
	t.Cleanup(bus.Close)
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	uc := New(repo, &fakeRuntimeRepo{}, nil, bus, logger)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	repo.preserveCalls = 0 // reset after Create

	cases := []struct {
		name string
		key  string
	}{
		{name: "three_stars", key: "***"},
		{name: "eight_bullets", key: "••••••••"},
		{name: "padded_stars", key: "        ***        "}, // trim then mask
		{name: "short_token", key: "abcdef0123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := repo.preserveCalls
			upd := validSnap("alpha")
			upd.APIKey = tc.key
			require.NoError(t, uc.Update(context.Background(), "alpha", upd, nil))
			assert.Equal(t, before+1, repo.preserveCalls,
				"preserveSecret must be true for placeholder-shaped api_key %q", tc.key)
		})
	}

	assert.Contains(t, logBuf.String(), "instance.update.suspicious_api_key_preserved",
		"placeholder rejection must emit a structured warning")
}

// TestUpdate_RealKey_DoesNotPreserve confirms a 32-hex realistic key
// still flows to the repository as a real overwrite (preserve=false).
func TestUpdate_RealKey_DoesNotPreserve(t *testing.T) {
	t.Parallel()
	uc, repo, _, _ := setup(t)
	require.NoError(t, uc.Create(context.Background(), validSnap("alpha")))
	before := repo.preserveCalls
	upd := validSnap("alpha")
	upd.APIKey = "0123456789abcdef0123456789abcdef" // 32 hex
	require.NoError(t, uc.Update(context.Background(), "alpha", upd, nil))
	assert.Equal(t, before, repo.preserveCalls,
		"a 32-hex real key must overwrite, not preserve")
}

// TestIsPlaceholderAPIKey is a table-driven micro-test of the guard
// helper so regressions in the predicate are caught independently of
// the Update flow.
func TestIsPlaceholderAPIKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
		desc string
	}{
		{in: "", want: true, desc: "empty"},
		{in: "***", want: true, desc: "three stars"},
		{in: "********", want: true, desc: "eight stars"},
		{in: "••••", want: true, desc: "bullets"},
		{in: "····", want: true, desc: "middle dots"},
		{in: "abc", want: true, desc: "too short real-ish"},
		{in: "0123456789abcdef", want: false, desc: "16-hex boundary"},
		{in: "0123456789abcdef0123456789abcdef", want: false, desc: "32-hex sonarr"},
		{in: "a-real-api-key-with-32-chars-here", want: false, desc: "non-hex 33ch"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isPlaceholderAPIKey(tc.in))
		})
	}
}

// --- 041a: Phase 11 instance field validation ---

// validBaseSnapshot returns a snapshot that satisfies every existing
// validation rule. Each helper test then mutates the one field under
// test so a failure points unambiguously at the new validator.
func validBaseSnapshot(name string) runtime.InstanceSnapshot {
	s := runtime.InstanceSnapshot{
		Name: name, URL: "http://sonarr.local", APIKey: "0123456789abcdef0123456789abcdef",
		Mode: "auto",
	}
	runtime.ApplyInstanceDefaults(&s)
	return s
}

func TestValidate_PublicURL_NilAndHTTPSAccepted(t *testing.T) {
	t.Parallel()
	s := validBaseSnapshot("x")
	require.NoError(t, validate(s, true), "nil PublicURL must pass")

	v := "https://sonarr.example.com"
	s.PublicURL = &v
	require.NoError(t, validate(s, true), "http(s) PublicURL must pass")

	o := "https://seasonfill.example.com"
	s.WebhookURLOverride = &o
	require.NoError(t, validate(s, true), "http(s) WebhookURLOverride must pass")
}

func TestValidate_PhaseElevenURLs_Rejections(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		apply func(s *runtime.InstanceSnapshot, v string)
		val   string
		code  string
		msg   string // optional substring on the error message
	}{
		{"public_url empty", func(s *runtime.InstanceSnapshot, v string) { s.PublicURL = &v }, "", "INVALID_INSTANCE_PUBLIC_URL", ""},
		{"public_url bad scheme", func(s *runtime.InstanceSnapshot, v string) { s.PublicURL = &v }, "ftp://x.example.com", "INVALID_INSTANCE_PUBLIC_URL", ""},
		{"public_url trailing slash", func(s *runtime.InstanceSnapshot, v string) { s.PublicURL = &v }, "https://x.example.com/", "INVALID_INSTANCE_PUBLIC_URL", "trailing slash"},
		{"public_url userinfo", func(s *runtime.InstanceSnapshot, v string) { s.PublicURL = &v }, "https://u:p@x.example.com", "INVALID_INSTANCE_PUBLIC_URL", ""},
		{"webhook_url_override empty", func(s *runtime.InstanceSnapshot, v string) { s.WebhookURLOverride = &v }, "", "INVALID_INSTANCE_WEBHOOK_URL_OVERRIDE", ""},
		{"webhook_url_override trailing slash", func(s *runtime.InstanceSnapshot, v string) { s.WebhookURLOverride = &v }, "https://y.example.com/", "INVALID_INSTANCE_WEBHOOK_URL_OVERRIDE", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := validBaseSnapshot("x")
			tc.apply(&s, tc.val)
			err := validate(s, true)
			var verr *ValidationError
			require.ErrorAs(t, err, &verr)
			assert.Equal(t, tc.code, verr.Code)
			if tc.msg != "" {
				assert.Contains(t, verr.Message, tc.msg)
			}
		})
	}
}
