package enrichment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
)

// recordingAnnouncer captures MaybeAnnounce delegations for the seam test.
type recordingAnnouncer struct {
	calls            int
	lastOld, lastNew *time.Time
}

func (r *recordingAnnouncer) MaybeAnnounce(_ context.Context, _ int64, _ string, oldNext, newNext *time.Time) {
	r.calls++
	r.lastOld, r.lastNew = oldNext, newNext
}

func TestMaybeAnnounceAirDate_HydratedDelegatesOnce(t *testing.T) {
	t.Parallel()
	rec := &recordingAnnouncer{}
	w := &SeriesWorker{deps: SeriesWorkerDeps{AirDateAnnouncer: rec}}

	synced := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	next := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	oldCanon := series.Canon{ID: 42, EnrichmentTMDBSyncedAt: &synced}
	merged := series.Canon{ID: 42, NextAirDate: &next}

	announced := false
	w.maybeAnnounceAirDate(context.Background(), oldCanon, merged, &announced, nil)
	assert.Equal(t, 1, rec.calls, "hydrated series must delegate once")
	assert.True(t, announced, "once-per-Handle flag must trip")
	assert.Equal(t, &next, rec.lastNew)

	// Once-per-Handle: a second call with *announced==true is a no-op.
	w.maybeAnnounceAirDate(context.Background(), oldCanon, merged, &announced, nil)
	assert.Equal(t, 1, rec.calls, "second call in the same Handle must not re-delegate")
}

func TestMaybeAnnounceAirDate_FirstHydration_NoDelegate(t *testing.T) {
	t.Parallel()
	rec := &recordingAnnouncer{}
	w := &SeriesWorker{deps: SeriesWorkerDeps{AirDateAnnouncer: rec}}

	next := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	oldCanon := series.Canon{ID: 42} // EnrichmentTMDBSyncedAt == nil → first hydration
	merged := series.Canon{ID: 42, NextAirDate: &next}

	announced := false
	w.maybeAnnounceAirDate(context.Background(), oldCanon, merged, &announced, nil)
	assert.Equal(t, 0, rec.calls, "first hydration must not flood")
	assert.False(t, announced)
}

func TestMaybeAnnounceAirDate_NilAnnouncer_Inert(t *testing.T) {
	t.Parallel()
	w := &SeriesWorker{deps: SeriesWorkerDeps{}} // AirDateAnnouncer nil-OK

	synced := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	next := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	oldCanon := series.Canon{ID: 42, EnrichmentTMDBSyncedAt: &synced}
	merged := series.Canon{ID: 42, NextAirDate: &next}

	announced := false
	assert.NotPanics(t, func() {
		w.maybeAnnounceAirDate(context.Background(), oldCanon, merged, &announced, nil)
	})
}
