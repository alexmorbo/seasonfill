package repositories

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// frozenNow — anchor for counter tests; deep inside a UTC hour so
// truncations don't bite at the boundary.
var frozenNow = time.Date(2026, 6, 7, 15, 30, 0, 0, time.UTC)

func TestCounterRepository_BucketCounters_7d_MixedStatuses(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewCounterRepository(db)
	ctx := context.Background()

	// today, yesterday, 3 days ago — three distinct buckets.
	today := startOfUTCDay(frozenNow)
	yesterday := today.Add(-24 * time.Hour)
	threeDaysAgo := today.Add(-3 * 24 * time.Hour)

	// today: 2 grabbed, 1 imported, 1 import_failed → grabs=4, imports=1, fails=1
	seedGrab(t, db, "alpha", 1, 1, grab.StatusGrabbed, today.Add(1*time.Hour))
	seedGrab(t, db, "alpha", 2, 1, grab.StatusGrabbed, today.Add(2*time.Hour))
	seedGrab(t, db, "alpha", 3, 1, grab.StatusImported, today.Add(3*time.Hour))
	seedGrab(t, db, "alpha", 4, 1, grab.StatusImportFailed, today.Add(4*time.Hour))
	// yesterday: 1 grab_failed → grabs=1, imports=0, fails=1
	seedGrab(t, db, "alpha", 5, 1, grab.StatusGrabFailed, yesterday.Add(10*time.Hour))
	// 3 days ago: 1 imported → grabs=1, imports=1, fails=0
	seedGrab(t, db, "alpha", 6, 1, grab.StatusImported, threeDaysAgo.Add(12*time.Hour))

	got, err := repo.BucketCounters(ctx, "alpha", ports.CounterWindow7d, frozenNow)
	require.NoError(t, err)
	require.Len(t, got, 7, "7-day window must always return 7 buckets")

	// Buckets are ordered oldest→newest. Index 6 = today, 5 = yesterday,
	// 4 = 2 days ago (empty), 3 = 3 days ago.
	assert.Equal(t, 4, got[6].Grabs)
	assert.Equal(t, 1, got[6].Imports)
	assert.Equal(t, 1, got[6].Fails)

	assert.Equal(t, 1, got[5].Grabs)
	assert.Equal(t, 0, got[5].Imports)
	assert.Equal(t, 1, got[5].Fails)

	assert.Equal(t, 0, got[4].Grabs, "empty bucket must be zero-filled")
	assert.Equal(t, 1, got[3].Grabs)
	assert.Equal(t, 1, got[3].Imports)
	assert.Equal(t, 0, got[3].Fails)
}

func TestCounterRepository_BucketCounters_24h_HourRollup(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewCounterRepository(db)
	ctx := context.Background()

	// Three grabs inside the same hour MUST roll up into one bucket.
	hour := frozenNow.Truncate(time.Hour).Add(-2 * time.Hour)
	seedGrab(t, db, "alpha", 1, 1, grab.StatusGrabbed, hour.Add(5*time.Minute))
	seedGrab(t, db, "alpha", 2, 1, grab.StatusGrabbed, hour.Add(20*time.Minute))
	seedGrab(t, db, "alpha", 3, 1, grab.StatusImported, hour.Add(45*time.Minute))

	got, err := repo.BucketCounters(ctx, "alpha", ports.CounterWindow24h, frozenNow)
	require.NoError(t, err)
	require.Len(t, got, 24, "24h window must always return 24 hourly buckets")

	// Locate the bucket that wraps `hour`.
	var matched ports.CounterBucket
	for _, b := range got {
		if b.BucketStart.Equal(hour) {
			matched = b
			break
		}
	}
	assert.Equal(t, 3, matched.Grabs)
	assert.Equal(t, 1, matched.Imports)
	assert.Equal(t, 0, matched.Fails)
}

func TestCounterRepository_BucketCounters_InstanceIsolation(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewCounterRepository(db)
	ctx := context.Background()

	today := startOfUTCDay(frozenNow)
	seedGrab(t, db, "alpha", 1, 1, grab.StatusGrabbed, today.Add(1*time.Hour))
	seedGrab(t, db, "beta", 2, 1, grab.StatusGrabbed, today.Add(2*time.Hour))

	got, err := repo.BucketCounters(ctx, "alpha", ports.CounterWindow7d, frozenNow)
	require.NoError(t, err)
	require.Len(t, got, 7)
	assert.Equal(t, 1, got[6].Grabs, "alpha must not see beta rows")
}

func TestCounterRepository_BucketCounters_InvalidWindow(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewCounterRepository(db)
	ctx := context.Background()

	_, err := repo.BucketCounters(ctx, "alpha", ports.CounterWindow("8h"), frozenNow)
	require.Error(t, err)
}

func TestCounterRepository_AvgGrabsLast7Days(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewCounterRepository(db)
	ctx := context.Background()

	today := startOfUTCDay(frozenNow)

	// One grab on each of i=1..7 days ago contributes to the average.
	// i=8..14 fall outside the window and must be excluded.
	for i := 1; i <= 14; i++ {
		when := today.Add(-time.Duration(i)*24*time.Hour + 6*time.Hour)
		seedGrab(t, db, "alpha", domain.SonarrSeriesID(i), 1, grab.StatusGrabbed, when)
	}
	// Today's grab MUST be excluded — window is [today-7, today).
	seedGrab(t, db, "alpha", 99, 1, grab.StatusGrabbed, today.Add(2*time.Hour))

	avg, err := repo.AvgGrabsLast7Days(ctx, "alpha", frozenNow)
	require.NoError(t, err)
	// 7 in-window grabs / 7 days = 1.0.
	assert.InDelta(t, 1.0, avg, 0.0001)
}

func TestCounterRepository_ExplainUsesIndex_SQLite(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	var plan []struct {
		ID     int
		Parent int
		Notu   int
		Detail string
	}
	err := db.Raw(`EXPLAIN QUERY PLAN
		SELECT COUNT(*) FROM grab_records
		WHERE instance_name = ? AND created_at >= ? AND created_at < ?`,
		"alpha", frozenNow.Add(-24*time.Hour), frozenNow,
	).Scan(&plan).Error
	require.NoError(t, err)
	found := false
	for _, p := range plan {
		if strings.Contains(p.Detail, "idx_grab_records_instance_created") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected idx_grab_records_instance_created in plan: %+v", plan)
}
