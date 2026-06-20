package regrab

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestNewSettingsFromRecord_AnonAuth(t *testing.T) {
	t.Parallel()
	rec := ports.QbitSettingsRecord{
		InstanceID:             7,
		Enabled:                true,
		URL:                    "http://qbit.local:8080",
		Username:               nil,
		PasswordEncrypted:      nil,
		Category:               "sonarr",
		PollIntervalMinutes:    30,
		RegrabCooldownHours:    120,
		MaxConsecutiveNoBetter: 3,
		CustomUnregisteredMsgs: []string{"unregistered"},
		PublicURL:              "https://qbit.example.com",
		UpdatedAt:              time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC),
	}
	got, err := NewSettingsFromRecord(rec, "alpha", nil)
	require.NoError(t, err)
	assert.Equal(t, domain.InstanceName("alpha"), got.InstanceName)
	assert.Equal(t, "", got.Username)
	assert.Equal(t, "", got.PasswordPlaintext)
	assert.Equal(t, 30*time.Minute, got.PollInterval)
	assert.Equal(t, 120*time.Hour, got.RegrabCooldown)
	assert.Equal(t, 3, got.MaxConsecutiveNoBetter)
	assert.Equal(t, "https://qbit.example.com", got.PublicURL)
}

func TestNewSettingsFromRecord_WithPassword(t *testing.T) {
	t.Parallel()
	cipher, err := crypto.New("0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	blob, err := cipher.Seal([]byte("hunter2"))
	require.NoError(t, err)

	user := "admin"
	rec := ports.QbitSettingsRecord{
		InstanceID:             7,
		URL:                    "http://qbit.local:8080",
		Username:               &user,
		PasswordEncrypted:      blob,
		Category:               "sonarr",
		PollIntervalMinutes:    30,
		RegrabCooldownHours:    120,
		MaxConsecutiveNoBetter: 3,
	}
	got, err := NewSettingsFromRecord(rec, "alpha", cipher)
	require.NoError(t, err)
	assert.Equal(t, "admin", got.Username)
	assert.Equal(t, "hunter2", got.PasswordPlaintext)
}

func TestNewSettingsFromRecord_PasswordWithoutCipher(t *testing.T) {
	t.Parallel()
	rec := ports.QbitSettingsRecord{
		InstanceID:        7,
		URL:               "http://qbit.local:8080",
		PasswordEncrypted: []byte{0xff, 0xee},
	}
	_, err := NewSettingsFromRecord(rec, "alpha", nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errCipherRequired))
}

func TestOutcomeReason_IsTerminal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		o    OutcomeReason
		want bool
	}{
		{OutcomeGrabbed, true},
		{OutcomeNothingBetter, true},
		{OutcomeFilterDropped, true},
		{OutcomeError, true},
		{OutcomeSkipCooldown, false},
		{OutcomeSkipBlacklist, false},
		{OutcomeSkipUnknown, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.o), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.o.IsTerminal())
		})
	}
}
