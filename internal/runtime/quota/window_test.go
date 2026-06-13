package quota

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaily_UTC_TruncatesToMidnight(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 6, 14, 18, 30, 0, 0, time.UTC)
	got := Daily(t0, time.UTC)
	want := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, want, got)
}

func TestDaily_NilLoc_DefaultsToUTC(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 6, 14, 18, 30, 0, 0, time.UTC)
	got := Daily(t0, nil)
	want := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, want, got)
}

func TestDaily_NonUTCLoc_TruncatesInLocalThenToUTC(t *testing.T) {
	t.Parallel()
	msk, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	// 2026-06-14 18:30 UTC = 2026-06-14 21:30 MSK
	// → day start in MSK = 2026-06-14 00:00 MSK
	// → in UTC = 2026-06-13 21:00 UTC
	t0 := time.Date(2026, 6, 14, 18, 30, 0, 0, time.UTC)
	got := Daily(t0, msk)
	want := time.Date(2026, 6, 13, 21, 0, 0, 0, time.UTC)
	assert.Equal(t, want, got)
}

func TestDaily_SameDayCollides(t *testing.T) {
	t.Parallel()
	a := time.Date(2026, 6, 14, 1, 0, 0, 0, time.UTC)
	b := time.Date(2026, 6, 14, 23, 59, 0, 0, time.UTC)
	assert.Equal(t, Daily(a, time.UTC), Daily(b, time.UTC), "two same-day calls collide on the window key")
}

func TestMonthly_UTC_TruncatesToFirstOfMonth(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 6, 14, 18, 30, 0, 0, time.UTC)
	got := Monthly(t0, time.UTC)
	want := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, want, got)
}

func TestMonthly_NilLoc_DefaultsToUTC(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 6, 14, 18, 30, 0, 0, time.UTC)
	got := Monthly(t0, nil)
	want := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, want, got)
}
