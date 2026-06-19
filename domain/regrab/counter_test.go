package regrab

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestNewNoBetterCounter_OK(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	got, err := NewNoBetterCounter(7, 122, 2, now)
	require.NoError(t, err)
	assert.Equal(t, uint(7), got.InstanceID)
	assert.Equal(t, domain.SonarrSeriesID(122), got.SeriesID)
	assert.Equal(t, 2, got.SeasonNumber)
	assert.Equal(t, 0, got.Consecutive)
	assert.Equal(t, now, got.CreatedAt)
	assert.Equal(t, now, got.UpdatedAt)
	assert.Equal(t, now, got.LastSeenAt)
}

func TestNewNoBetterCounter_NormalisesToUTC(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	localNow := time.Date(2026, 6, 6, 15, 0, 0, 0, loc)
	got, err := NewNoBetterCounter(1, 1, 0, localNow)
	require.NoError(t, err)
	assert.Equal(t, time.UTC, got.CreatedAt.Location())
	assert.Equal(t, time.UTC, got.UpdatedAt.Location())
	assert.Equal(t, time.UTC, got.LastSeenAt.Location())
}

func TestNewNoBetterCounter_Validation(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	cases := []struct {
		name     string
		instance uint
		series   domain.SonarrSeriesID
		season   int
		errIs    error
	}{
		{"zero instance", 0, 1, 0, ErrInvalidInstance},
		{"zero series", 1, 0, 0, ErrInvalidSeries},
		{"negative series", 1, -1, 0, ErrInvalidSeries},
		{"negative season", 1, 1, -1, ErrInvalidSeason},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewNoBetterCounter(tc.instance, tc.series, tc.season, now)
			require.Error(t, err)
			assert.True(t, errors.Is(err, tc.errIs))
		})
	}
}

func TestNoBetterCounter_Increment(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	c, err := NewNoBetterCounter(7, 122, 2, start)
	require.NoError(t, err)

	later := start.Add(time.Hour)
	got := c.Increment(later)
	assert.Equal(t, 1, got.Consecutive)
	assert.Equal(t, later, got.UpdatedAt)
	assert.Equal(t, later, got.LastSeenAt)
	// Receiver is unchanged — pure method.
	assert.Equal(t, 0, c.Consecutive)
}

func TestNoBetterCounter_Increment_NormalisesToUTC(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	c, err := NewNoBetterCounter(7, 122, 2, start)
	require.NoError(t, err)

	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	localLater := time.Date(2026, 6, 6, 15, 0, 0, 0, loc)
	got := c.Increment(localLater)
	assert.Equal(t, time.UTC, got.UpdatedAt.Location())
	assert.Equal(t, time.UTC, got.LastSeenAt.Location())
}

func TestNoBetterCounter_Reset(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	c, err := NewNoBetterCounter(7, 122, 2, start)
	require.NoError(t, err)
	bumped := c.Increment(start.Add(time.Hour))
	bumped = bumped.Increment(start.Add(2 * time.Hour))
	require.Equal(t, 2, bumped.Consecutive)

	reset := bumped.Reset(start.Add(3 * time.Hour))
	assert.Equal(t, 0, reset.Consecutive)
	assert.Equal(t, start.Add(3*time.Hour), reset.UpdatedAt)
	// LastSeenAt is preserved — debug telemetry.
	assert.Equal(t, start.Add(2*time.Hour), reset.LastSeenAt)
	// Receiver unchanged.
	assert.Equal(t, 2, bumped.Consecutive)
}

func TestNoBetterCounter_HasReachedThreshold(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	c, err := NewNoBetterCounter(7, 122, 2, now)
	require.NoError(t, err)

	cases := []struct {
		name      string
		consec    int
		threshold int
		want      bool
	}{
		{"below", 1, 3, false},
		{"at threshold", 3, 3, true},
		{"above", 4, 3, true},
		{"zero count, positive threshold", 0, 3, false},
		{"any count, zero threshold", 5, 0, false},
		{"negative threshold", 5, -1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			local := c
			local.Consecutive = tc.consec
			assert.Equal(t, tc.want, local.HasReachedThreshold(tc.threshold))
		})
	}
}
