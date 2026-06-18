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
		tc := tc
		t.Run(string(tc.r), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.r.IsValid())
		})
	}
}

func TestNewBlacklistEntry_OK(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	got, err := NewBlacklistEntry(7, 122, 2, 3, ReasonConsecutiveNoBetter, now)
	require.NoError(t, err)
	assert.Equal(t, uint(7), got.InstanceID)
	assert.Equal(t, domain.SonarrSeriesID(122), got.SeriesID)
	assert.Equal(t, 2, got.SeasonNumber)
	assert.Equal(t, 3, got.Consecutive)
	assert.Equal(t, ReasonConsecutiveNoBetter, got.Reason)
	assert.Equal(t, now, got.CreatedAt)
	assert.Nil(t, got.ExpiresAt, "v1 always writes NULL ExpiresAt (manual unblock)")
}

func TestNewBlacklistEntry_NormalisesToUTC(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	localNow := time.Date(2026, 6, 6, 15, 0, 0, 0, loc)
	got, err := NewBlacklistEntry(1, 1, 0, 1, ReasonQbitUnreachablePersistent, localNow)
	require.NoError(t, err)
	assert.Equal(t, time.UTC, got.CreatedAt.Location())
	assert.True(t, got.CreatedAt.Equal(localNow))
}

func TestNewBlacklistEntry_Validation(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	cases := []struct {
		name        string
		instance    uint
		series      domain.SonarrSeriesID
		season      int
		consecutive int
		reason      Reason
		errIs       error
	}{
		{"zero instance", 0, 1, 0, 1, ReasonConsecutiveNoBetter, ErrInvalidInstance},
		{"negative series", 1, 0, 0, 1, ReasonConsecutiveNoBetter, ErrInvalidSeries},
		{"negative series id", 1, -5, 0, 1, ReasonConsecutiveNoBetter, ErrInvalidSeries},
		{"negative season", 1, 1, -1, 1, ReasonConsecutiveNoBetter, ErrInvalidSeason},
		{"unknown reason", 1, 1, 0, 1, Reason("bogus"), ErrInvalidReason},
		{"empty reason", 1, 1, 0, 1, Reason(""), ErrInvalidReason},
		{"zero consecutive", 1, 1, 0, 0, ReasonConsecutiveNoBetter, ErrInvalidConsecutive},
		{"negative consecutive", 1, 1, 0, -1, ReasonConsecutiveNoBetter, ErrInvalidConsecutive},
	}
	for _, tc := range cases {
		tc := tc
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
	_, err := NewBlacklistEntry(1, 1, 0, 1, ReasonConsecutiveNoBetter, time.Now())
	require.NoError(t, err)
}
