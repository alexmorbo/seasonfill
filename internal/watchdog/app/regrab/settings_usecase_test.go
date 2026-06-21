package regrab

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

const testMasterKey = "test-master-key-32-bytes-for-aes-gcm"

func newTestCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New(testMasterKey)
	require.NoError(t, err)
	return c
}

// fakeSettingsRepo is the in-process repo. Keyed on InstanceName.
type fakeSettingsRepo struct {
	rows     map[domain.InstanceName]ports.QbitSettingsRecord
	upsertEr error
	getEr    error
}

func newFakeSettingsRepo() *fakeSettingsRepo {
	return &fakeSettingsRepo{rows: map[domain.InstanceName]ports.QbitSettingsRecord{}}
}

func (f *fakeSettingsRepo) Upsert(_ context.Context, rec ports.QbitSettingsRecord) error {
	if f.upsertEr != nil {
		return f.upsertEr
	}
	f.rows[rec.InstanceName] = rec
	return nil
}
func (f *fakeSettingsRepo) GetByInstance(_ context.Context, name domain.InstanceName) (ports.QbitSettingsRecord, error) {
	if f.getEr != nil {
		return ports.QbitSettingsRecord{}, f.getEr
	}
	r, ok := f.rows[name]
	if !ok {
		return ports.QbitSettingsRecord{}, ports.ErrNotFound
	}
	return r, nil
}
func (f *fakeSettingsRepo) DeleteByInstance(_ context.Context, name domain.InstanceName) error {
	if _, ok := f.rows[name]; !ok {
		return ports.ErrNotFound
	}
	delete(f.rows, name)
	return nil
}
func (f *fakeSettingsRepo) List(_ context.Context) ([]ports.QbitSettingsRecord, error) {
	out := make([]ports.QbitSettingsRecord, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}

// fakeInstanceRepo — only GetByName is exercised by the use case.
type fakeInstanceRepo struct {
	rows  map[string]runtime.InstanceSnapshot
	getEr error
}

func newFakeInstanceRepo() *fakeInstanceRepo {
	return &fakeInstanceRepo{rows: map[string]runtime.InstanceSnapshot{}}
}
func (f *fakeInstanceRepo) Seed(name string, id uint) {
	f.rows[name] = runtime.InstanceSnapshot{ID: id, Name: name,
		URL: "http://sonarr.local:8989"}
}
func (f *fakeInstanceRepo) GetByName(_ context.Context, name string, _ *crypto.Cipher) (runtime.InstanceSnapshot, error) {
	if f.getEr != nil {
		return runtime.InstanceSnapshot{}, f.getEr
	}
	r, ok := f.rows[name]
	if !ok {
		return runtime.InstanceSnapshot{}, ports.ErrNotFound
	}
	return r, nil
}

// The rest of the SonarrInstanceRepository surface is unused by
// the use case under test; stubbed to nil-return so the interface
// is satisfied.
func (f *fakeInstanceRepo) List(_ context.Context, _ *crypto.Cipher) ([]runtime.InstanceSnapshot, error) {
	return nil, nil
}
func (f *fakeInstanceRepo) Create(_ context.Context, _ runtime.InstanceSnapshot, _ *crypto.Cipher) (uint, error) {
	return 0, nil
}
func (f *fakeInstanceRepo) UpdateWithOptions(_ context.Context, _ runtime.InstanceSnapshot, _ *crypto.Cipher, _ bool, _ *time.Time) error {
	return nil
}
func (f *fakeInstanceRepo) Delete(_ context.Context, _ string) error { return nil }
func (f *fakeInstanceRepo) GetUpdatedAt(_ context.Context, _ string) (time.Time, error) {
	return time.Time{}, nil
}

// stubWebhookChecker tracks calls + returns a controllable verdict.
type stubWebhookChecker struct {
	installed bool
	err       error
	calls     atomic.Int32
}

func (s *stubWebhookChecker) IsInstalled(_ context.Context, _ domain.InstanceName) (bool, error) {
	s.calls.Add(1)
	return s.installed, s.err
}

func validInput() UpsertInput {
	return UpsertInput{
		Enabled:                false,
		URL:                    "http://qbit.local:8080",
		PublicURL:              "https://qbit.example.com",
		Username:               "admin",
		Password:               "hunter2",
		Category:               "sonarr",
		PollIntervalMinutes:    30,
		RegrabCooldownHours:    120,
		MaxConsecutiveNoBetter: 3,
		CustomUnregisteredMsgs: []string{"раздача погашена"},
	}
}

func newUC(t *testing.T, repo *fakeSettingsRepo, instances *fakeInstanceRepo) *SettingsUseCase {
	t.Helper()
	return NewSettingsUseCase(repo, instances, newTestCipher(t), nil)
}

func TestUseCase_Upsert_CreatesAnonRowWhenPasswordEmpty(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	uc := newUC(t, repo, instances)

	in := validInput()
	in.Password = ""
	in.Username = ""
	view, err := uc.Upsert(context.Background(), "alpha", in)
	require.NoError(t, err)
	assert.False(t, view.PasswordSet)
	assert.Empty(t, view.Username)
	stored := repo.rows["alpha"]
	assert.Nil(t, stored.PasswordEncrypted)
	assert.Nil(t, stored.Username)
}

func TestUseCase_Upsert_EncryptsPasswordOnCreate(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	uc := newUC(t, repo, instances)

	view, err := uc.Upsert(context.Background(), "alpha", validInput())
	require.NoError(t, err)
	assert.True(t, view.PasswordSet)
	stored := repo.rows["alpha"]
	require.NotEmpty(t, stored.PasswordEncrypted)
	plaintext, err := uc.DecryptPassword(stored)
	require.NoError(t, err)
	assert.Equal(t, "hunter2", plaintext)
}

func TestUseCase_Upsert_PreservesPasswordOnEmpty(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	uc := newUC(t, repo, instances)

	_, err := uc.Upsert(context.Background(), "alpha", validInput())
	require.NoError(t, err)
	originalBlob := append([]byte{}, repo.rows["alpha"].PasswordEncrypted...)

	in := validInput()
	in.Password = "" // dirty-bit: preserve
	in.URL = "http://qbit2.local:8080"
	_, err = uc.Upsert(context.Background(), "alpha", in)
	require.NoError(t, err)

	assert.Equal(t, originalBlob, repo.rows["alpha"].PasswordEncrypted)
	assert.Equal(t, "http://qbit2.local:8080", repo.rows["alpha"].URL)
}

func TestUseCase_Upsert_ReplacesPasswordOnNonEmpty(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	uc := newUC(t, repo, instances)

	_, err := uc.Upsert(context.Background(), "alpha", validInput())
	require.NoError(t, err)
	originalBlob := append([]byte{}, repo.rows["alpha"].PasswordEncrypted...)

	in := validInput()
	in.Password = "newpass"
	_, err = uc.Upsert(context.Background(), "alpha", in)
	require.NoError(t, err)

	assert.NotEqual(t, originalBlob, repo.rows["alpha"].PasswordEncrypted)
	plaintext, err := uc.DecryptPassword(repo.rows["alpha"])
	require.NoError(t, err)
	assert.Equal(t, "newpass", plaintext)
}

func TestUseCase_Upsert_RejectsEnableWithoutWebhook(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	checker := &stubWebhookChecker{installed: false}
	uc := newUC(t, repo, instances).WithWebhookChecker(checker)

	in := validInput()
	in.Enabled = true
	_, err := uc.Upsert(context.Background(), "alpha", in)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrWebhookNotInstalled))
	assert.Equal(t, int32(1), checker.calls.Load())
	assert.Empty(t, repo.rows, "must not persist on gate failure")
}

func TestUseCase_Upsert_SkipsGateOnDisabled(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	checker := &stubWebhookChecker{installed: false}
	uc := newUC(t, repo, instances).WithWebhookChecker(checker)

	in := validInput()
	in.Enabled = false
	_, err := uc.Upsert(context.Background(), "alpha", in)
	require.NoError(t, err)
	assert.Equal(t, int32(0), checker.calls.Load(),
		"disabled save must not consult the gate")
	assert.Len(t, repo.rows, 1)
}

func TestUseCase_Upsert_SkipsGateWhenAlreadyEnabled(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	checker := &stubWebhookChecker{installed: true}
	uc := newUC(t, repo, instances).WithWebhookChecker(checker)

	in := validInput()
	in.Enabled = true
	_, err := uc.Upsert(context.Background(), "alpha", in)
	require.NoError(t, err)
	assert.Equal(t, int32(1), checker.calls.Load())

	// Second save with already-enabled row: gate skipped.
	checker.installed = false
	_, err = uc.Upsert(context.Background(), "alpha", in)
	require.NoError(t, err, "gate must NOT re-run on enabled→enabled saves")
	assert.Equal(t, int32(1), checker.calls.Load())
}

func TestUseCase_Upsert_WebhookCheckTransportErrorMapsToErrWebhookCheckFailed(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	checker := &stubWebhookChecker{err: errors.New("sonarr 500")}
	uc := newUC(t, repo, instances).WithWebhookChecker(checker)

	in := validInput()
	in.Enabled = true
	_, err := uc.Upsert(context.Background(), "alpha", in)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrWebhookCheckFailed))
	assert.Empty(t, repo.rows)
}

func TestUseCase_Upsert_InstanceNotFound(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	uc := newUC(t, repo, instances)
	_, err := uc.Upsert(context.Background(), "nope", validInput())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestUseCase_GetByInstanceName_NotFoundOnInstance(t *testing.T) {
	t.Parallel()
	uc := newUC(t, newFakeSettingsRepo(), newFakeInstanceRepo())
	_, err := uc.GetByInstanceName(context.Background(), "nope")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestUseCase_GetByInstanceName_NotFoundOnSettings(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	uc := newUC(t, repo, instances)
	_, err := uc.GetByInstanceName(context.Background(), "alpha")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestUseCase_Delete(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	uc := newUC(t, repo, instances)

	_, err := uc.Upsert(context.Background(), "alpha", validInput())
	require.NoError(t, err)
	require.NoError(t, uc.Delete(context.Background(), "alpha"))

	_, err = uc.GetByInstanceName(context.Background(), "alpha")
	require.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestUseCase_Delete_NotFound(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	uc := newUC(t, repo, instances)
	err := uc.Delete(context.Background(), "alpha")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestUseCase_DecryptPassword_EmptyBlob(t *testing.T) {
	t.Parallel()
	uc := newUC(t, newFakeSettingsRepo(), newFakeInstanceRepo())
	pt, err := uc.DecryptPassword(ports.QbitSettingsRecord{})
	require.NoError(t, err)
	assert.Equal(t, "", pt)
}

func TestUseCase_Upsert_NormalisesCustomMsgs(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	uc := newUC(t, repo, instances)

	in := validInput()
	in.CustomUnregisteredMsgs = []string{
		"  Раздача Погашена  ", "deleted", "Deleted", "  ", "unregistered",
	}
	view, err := uc.Upsert(context.Background(), "alpha", in)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"раздача погашена", "deleted", "unregistered",
	}, view.CustomUnregisteredMsgs,
		"trim+lower+dedup+drop-empty in order")
}

func TestValidate_Bounds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		mut  func(*UpsertInput)
		code string
	}{
		{"empty url", func(in *UpsertInput) { in.URL = "" }, "INVALID_QBIT_URL"},
		{"bad scheme", func(in *UpsertInput) { in.URL = "ftp://x" }, "INVALID_QBIT_URL"},
		{"userinfo url", func(in *UpsertInput) { in.URL = "http://u:p@x" }, "INVALID_QBIT_URL"},
		{"empty host", func(in *UpsertInput) { in.URL = "http://" }, "INVALID_QBIT_URL"},
		{"public_url bad scheme", func(in *UpsertInput) { in.PublicURL = "ftp://x" }, "INVALID_QBIT_PUBLIC_URL"},
		{"public_url userinfo", func(in *UpsertInput) { in.PublicURL = "http://u:p@x" }, "INVALID_QBIT_PUBLIC_URL"},
		{"public_url empty host", func(in *UpsertInput) { in.PublicURL = "http://" }, "INVALID_QBIT_PUBLIC_URL"},
		{"empty category", func(in *UpsertInput) { in.Category = "" }, "INVALID_QBIT_CATEGORY"},
		{"poll too small", func(in *UpsertInput) { in.PollIntervalMinutes = 1 }, "INVALID_POLL_INTERVAL"},
		{"poll too big", func(in *UpsertInput) { in.PollIntervalMinutes = 99999 }, "INVALID_POLL_INTERVAL"},
		{"cooldown too small", func(in *UpsertInput) { in.RegrabCooldownHours = 0 }, "INVALID_REGRAB_COOLDOWN"},
		{"cooldown too big", func(in *UpsertInput) { in.RegrabCooldownHours = 9999 }, "INVALID_REGRAB_COOLDOWN"},
		{"max consec too small", func(in *UpsertInput) { in.MaxConsecutiveNoBetter = 0 }, "INVALID_MAX_CONSECUTIVE"},
		{"max consec too big", func(in *UpsertInput) { in.MaxConsecutiveNoBetter = 9999 }, "INVALID_MAX_CONSECUTIVE"},
		{"msg too short", func(in *UpsertInput) { in.CustomUnregisteredMsgs = []string{"ab"} }, "INVALID_CUSTOM_MSGS"},
		{"msg too long", func(in *UpsertInput) {
			big := make([]byte, customUnregisteredMsgsEntryMax+1)
			for i := range big {
				big[i] = 'a'
			}
			in.CustomUnregisteredMsgs = []string{string(big)}
		}, "INVALID_CUSTOM_MSGS"},
		{"too many msgs", func(in *UpsertInput) {
			in.CustomUnregisteredMsgs = make([]string, customUnregisteredMsgsMaxCount+1)
			for i := range in.CustomUnregisteredMsgs {
				in.CustomUnregisteredMsgs[i] = "deleted"
			}
		}, "INVALID_CUSTOM_MSGS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := validInput()
			tc.mut(&in)
			err := validate(in)
			require.Error(t, err)
			var verr *ValidationError
			require.True(t, errors.As(err, &verr))
			assert.Equal(t, tc.code, verr.Code)
		})
	}
}

func TestValidate_HappyPath(t *testing.T) {
	t.Parallel()
	require.NoError(t, validate(validInput()))
}

// TestValidate_PublicURL_OptionalEmptyAllowed asserts the empty-string
// path on the optional public-URL field. The current validInput()
// already populates the field, so this is the explicit guard against
// a regression where the empty branch starts demanding a value.
func TestValidate_PublicURL_OptionalEmptyAllowed(t *testing.T) {
	t.Parallel()
	in := validInput()
	in.PublicURL = ""
	require.NoError(t, validate(in))
}

// TestUseCase_Upsert_PublicURLRoundTrip asserts the field is persisted
// through Upsert → repo → recordToView intact, and that a subsequent
// dirty-bit (empty password) update preserves the previously stored
// public URL.
func TestUseCase_Upsert_PublicURLRoundTrip(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	uc := newUC(t, repo, instances)

	view, err := uc.Upsert(context.Background(), "alpha", validInput())
	require.NoError(t, err)
	assert.Equal(t, "https://qbit.example.com", view.PublicURL)
	assert.Equal(t, "https://qbit.example.com", repo.rows["alpha"].PublicURL)

	in := validInput()
	in.Password = ""
	in.PublicURL = "https://qbit2.example.com"
	view, err = uc.Upsert(context.Background(), "alpha", in)
	require.NoError(t, err)
	assert.Equal(t, "https://qbit2.example.com", view.PublicURL)
}

// TestUseCase_Upsert_PublicURLClear asserts an empty string on update
// clears the previously-stored public URL (frontend "delete" path).
func TestUseCase_Upsert_PublicURLClear(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	uc := newUC(t, repo, instances)

	_, err := uc.Upsert(context.Background(), "alpha", validInput())
	require.NoError(t, err)
	require.Equal(t, "https://qbit.example.com", repo.rows["alpha"].PublicURL)

	in := validInput()
	in.PublicURL = ""
	view, err := uc.Upsert(context.Background(), "alpha", in)
	require.NoError(t, err)
	assert.Equal(t, "", view.PublicURL)
	assert.Equal(t, "", repo.rows["alpha"].PublicURL)
}

// TestUseCase_Upsert_PublicURLTrimsWhitespace ensures stray whitespace
// from copy-paste in the form is stripped before persisting. Matches
// the URL field's existing trim behaviour.
func TestUseCase_Upsert_PublicURLTrimsWhitespace(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	uc := newUC(t, repo, instances)

	in := validInput()
	in.PublicURL = "  https://qbit.example.com  "
	view, err := uc.Upsert(context.Background(), "alpha", in)
	require.NoError(t, err)
	assert.Equal(t, "https://qbit.example.com", view.PublicURL)
}

func TestNullWebhookChecker_AlwaysInstalled(t *testing.T) {
	t.Parallel()
	ok, err := nullWebhookChecker{}.IsInstalled(context.Background(), "alpha")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestUseCase_WithWebhookChecker_NilFallsBackToNullChecker(t *testing.T) {
	t.Parallel()
	uc := newUC(t, newFakeSettingsRepo(), newFakeInstanceRepo()).WithWebhookChecker(nil)
	// re-assert default is still null-checker by inspecting its
	// gate decision through Upsert with enabled=true and no row.
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	uc.instances = instances
	in := validInput()
	in.Enabled = true
	_, err := uc.Upsert(context.Background(), "alpha", in)
	require.NoError(t, err)
}

func TestUseCase_WithClock(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.Seed("alpha", 7)
	fixed := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	uc := newUC(t, repo, instances).WithClock(func() time.Time { return fixed })
	view, err := uc.Upsert(context.Background(), "alpha", validInput())
	require.NoError(t, err)
	assert.Equal(t, fixed, view.CreatedAt)
	assert.Equal(t, fixed, view.UpdatedAt)
}

// TestUseCase_GetByInstanceName_PreservesTypedInstanceNF asserts F-2c-2's
// typed-chain preservation contract: when the parent instance lookup
// misses, the use case returns InstanceNotFoundError joined with
// ports.ErrNotFound so middleware can dispatch instance_not_found
// instead of the generic not_found code.
func TestUseCase_GetByInstanceName_PreservesTypedInstanceNF(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	// Stub returns ports.ErrNotFound joined with the typed sentinel,
	// matching the production repo's actual return shape.
	instances.getEr = errors.Join(
		&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName("nope")},
		ports.ErrNotFound,
	)
	uc := newUC(t, repo, instances)

	_, err := uc.GetByInstanceName(context.Background(), "nope")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound),
		"errors.Is(ports.ErrNotFound) must keep working through use-case wrap")
	var typed *sharedErrors.InstanceNotFoundError
	require.True(t, errors.As(err, &typed),
		"InstanceNotFoundError chain must survive the use-case wrap (F-2c-2)")
	assert.Equal(t, domain.InstanceName("nope"), typed.Name)
}

// TestUseCase_Delete_PreservesTypedInstanceNF — same contract on Delete.
func TestUseCase_Delete_PreservesTypedInstanceNF(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.getEr = errors.Join(
		&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName("ghost")},
		ports.ErrNotFound,
	)
	uc := newUC(t, repo, instances)

	err := uc.Delete(context.Background(), "ghost")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
	var typed *sharedErrors.InstanceNotFoundError
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, domain.InstanceName("ghost"), typed.Name)
}

// TestUseCase_Lookup_PreservesTypedInstanceNF — same contract on Lookup
// (the regrab loop's read path).
func TestUseCase_Lookup_PreservesTypedInstanceNF(t *testing.T) {
	t.Parallel()
	repo := newFakeSettingsRepo()
	instances := newFakeInstanceRepo()
	instances.getEr = errors.Join(
		&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName("vanished")},
		ports.ErrNotFound,
	)
	uc := newUC(t, repo, instances)

	_, err := uc.Lookup(context.Background(), domain.InstanceName("vanished"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
	var typed *sharedErrors.InstanceNotFoundError
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, domain.InstanceName("vanished"), typed.Name)
}
