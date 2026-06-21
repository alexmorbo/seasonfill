package regrab

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestReason_IsValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		r    Reason
		want bool
	}{
		{ReasonConsecutiveNoBetter, true},
		{ReasonQbitUnreachablePersistent, true},
		{Reason(""), false},
		{Reason("manual"), false},
		{Reason("CONSECUTIVE_NO_BETTER"), false},
	}
	for _, tc := range cases {
		t.Run(string(tc.r), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.r.IsValid())
		})
	}
}

func TestNewBlacklistEntry_OK(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	got, err := NewBlacklistEntry("homelab", 122, 2, 3, ReasonConsecutiveNoBetter, now)
	require.NoError(t, err)
	assert.Equal(t, domain.InstanceName("homelab"), got.InstanceName)
	assert.Equal(t, domain.SonarrSeriesID(122), got.SeriesID)
	assert.Equal(t, 2, got.SeasonNumber)
	assert.Equal(t, 3, got.Consecutive)
	assert.Equal(t, ReasonConsecutiveNoBetter, got.Reason)
	assert.Equal(t, now, got.CreatedAt)
	assert.Nil(t, got.TTLUntil, "v1 always writes NULL TTLUntil (manual unblock)")
}

func TestNewBlacklistEntry_NormalisesToUTC(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	localNow := time.Date(2026, 6, 6, 15, 0, 0, 0, loc)
	got, err := NewBlacklistEntry("alpha", 1, 0, 1, ReasonQbitUnreachablePersistent, localNow)
	require.NoError(t, err)
	assert.Equal(t, time.UTC, got.CreatedAt.Location())
	assert.True(t, got.CreatedAt.Equal(localNow))
}

func TestNewBlacklistEntry_Validation(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	cases := []struct {
		name        string
		instance    domain.InstanceName
		series      domain.SonarrSeriesID
		season      int
		consecutive int
		reason      Reason
		errIs       error
	}{
		{"empty instance", "", 1, 0, 1, ReasonConsecutiveNoBetter, ErrInvalidInstance},
		{"zero series", "homelab", 0, 0, 1, ReasonConsecutiveNoBetter, ErrInvalidSeries},
		{"negative series id", "homelab", -5, 0, 1, ReasonConsecutiveNoBetter, ErrInvalidSeries},
		{"negative season", "homelab", 1, -1, 1, ReasonConsecutiveNoBetter, ErrInvalidSeason},
		{"unknown reason", "homelab", 1, 0, 1, Reason("bogus"), ErrInvalidReason},
		{"empty reason", "homelab", 1, 0, 1, Reason(""), ErrInvalidReason},
		{"zero consecutive", "homelab", 1, 0, 0, ReasonConsecutiveNoBetter, ErrInvalidConsecutive},
		{"negative consecutive", "homelab", 1, 0, -1, ReasonConsecutiveNoBetter, ErrInvalidConsecutive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewBlacklistEntry(tc.instance, tc.series, tc.season, tc.consecutive, tc.reason, now)
			require.Error(t, err)
			assert.True(t, errors.Is(err, tc.errIs),
				"err %v must wrap sentinel %v", err, tc.errIs)
		})
	}
}

func TestNewBlacklistEntry_SeasonZeroAllowed(t *testing.T) {
	t.Parallel()
	// Season 0 is "Specials" in Sonarr — must be representable.
	_, err := NewBlacklistEntry("homelab", 1, 0, 1, ReasonConsecutiveNoBetter, time.Now())
	require.NoError(t, err)
}
