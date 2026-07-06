package enrichment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// countOMDbEnqueues returns how many EntityOMDb jobs at the given priority the
// dispatcher recorded.
func countOMDbEnqueues(d *recordingDispatcher, p Priority) int {
	n := 0
	for _, c := range d.calls {
		if c.Kind == EntityOMDb && c.Priority == p {
			n++
		}
	}
	return n
}

// seedCanonNoIMDB installs a canon row with tmdb_id set but NO imdb_id, so a
// TMDB enrichment (minimalTV carries external_ids imdb "tt0001") drives the
// null→value transition. InProduction=true classifies as KindOMDbInProduction.
// OriginalTitle is intentionally omitted — irrelevant to classifyOMDbKind /
// the W18-8 guards.
func seedCanonNoIMDB(f *workerFixture, id domain.SeriesID) {
	tmdbID := domain.TMDBID(42)
	f.series.rows[id] = series.Canon{
		ID:           id,
		TMDBID:       &tmdbID,
		InProduction: true,
	}
}

// Test 1: series gains imdb_id for the first time → enqueue OMDb Cold exactly
// once (and once only, across a two-language Handle).
func TestW18_8_IMDBGain_EnqueuesColdOnce(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t, minimalTV(), map[int]*tmdb.SeasonResponse{1: minimalSeason()})
	f.worker.deps.Languages = []string{"en-US", "ru-RU"}
	d := &recordingDispatcher{}
	f.worker.deps.Dispatcher = d
	f.worker.deps.OMDbBudget = &fakeOMDbBudget{coldAvailable: true, remaining: 899}
	seedCanonNoIMDB(f, 1)

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	assert.Equal(t, 1, countOMDbEnqueues(d, PriorityCold),
		"imdb_id null→value must enqueue exactly one OMDb Cold job across all languages")
	// The enqueue targets the canon series id.
	found := false
	for _, c := range d.calls {
		if c.Kind == EntityOMDb {
			assert.Equal(t, int64(1), c.ID)
			found = true
		}
	}
	require.True(t, found, "an EntityOMDb enqueue must be recorded")
}

// Test 2: re-enrichment of a series that ALREADY has the same imdb_id → NO
// enqueue (no null→value transition).
func TestW18_8_IMDBUnchanged_NoEnqueue(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t, minimalTV(), map[int]*tmdb.SeasonResponse{1: minimalSeason()})
	d := &recordingDispatcher{}
	f.worker.deps.Dispatcher = d
	f.worker.deps.OMDbBudget = &fakeOMDbBudget{coldAvailable: true, remaining: 899}

	// Old canon already carries imdb_id (matches minimalTV's tt0001).
	tmdbID := domain.TMDBID(42)
	f.series.rows[1] = series.Canon{
		ID: 1, TMDBID: &tmdbID, IMDBID: imdbPtr("tt0001"),
		InProduction: true,
	}

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	assert.Equal(t, 0, countOMDbEnqueues(d, PriorityCold),
		"unchanged imdb_id must NOT enqueue an OMDb Cold job")
}

// Test 3: imdb_id gained BUT OMDb data already fresh by the W18-5 TTL → skip.
func TestW18_8_OMDbFresh_SkipsEnqueue(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t, minimalTV(), map[int]*tmdb.SeasonResponse{1: minimalSeason()})
	d := &recordingDispatcher{}
	f.worker.deps.Dispatcher = d
	f.worker.deps.OMDbBudget = &fakeOMDbBudget{coldAvailable: true, remaining: 899}

	// No imdb_id (transition fires) but OMDb synced "now" → InProduction TTL
	// (2d) not expired → fresh → skip. Clock = 2026-06-13 12:00 UTC.
	tmdbID := domain.TMDBID(42)
	syncedNow := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	f.series.rows[1] = series.Canon{
		ID: 1, TMDBID: &tmdbID, InProduction: true,
		EnrichmentOMDBSyncedAt: &syncedNow,
	}

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	assert.Equal(t, 0, countOMDbEnqueues(d, PriorityCold),
		"fresh OMDb data (within TTL) must skip the enqueue")
}

// Test 4: imdb_id gained BUT the series is terminal-negative (attempts>5) → skip.
func TestW18_8_TerminalNegative_SkipsEnqueue(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t, minimalTV(), map[int]*tmdb.SeasonResponse{1: minimalSeason()})
	d := &recordingDispatcher{}
	f.worker.deps.Dispatcher = d
	f.worker.deps.OMDbBudget = &fakeOMDbBudget{coldAvailable: true, remaining: 899}
	seedCanonNoIMDB(f, 1)

	// Seed a durable OMDb negative-cache row past the give-up threshold.
	f.enrichmentErrors.preexist = &enrichment.EnrichmentError{
		EntityType: enrichment.EntityTypeSeries,
		EntityID:   1,
		Source:     enrichment.SourceOMDb,
		Attempts:   6, // > omdbColdEnqueueTerminalAttempts (5)
		LastError:  "Movie not found!",
	}

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	assert.Equal(t, 0, countOMDbEnqueues(d, PriorityCold),
		"terminal-negative (attempts>5) must skip the enqueue")
}

// Test 5: imdb_id gained BUT Cold budget is at the Hot floor → skip, no crash.
func TestW18_8_ColdBudgetExhausted_SkipsEnqueue(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t, minimalTV(), map[int]*tmdb.SeasonResponse{1: minimalSeason()})
	d := &recordingDispatcher{}
	f.worker.deps.Dispatcher = d
	// coldAvailable=false ⇒ ReserveCold would deny at the floor.
	f.worker.deps.OMDbBudget = &fakeOMDbBudget{coldAvailable: false, remaining: 200}
	seedCanonNoIMDB(f, 1)

	require.NoError(t, f.worker.Handle(context.Background(), 1))

	assert.Equal(t, 0, countOMDbEnqueues(d, PriorityCold),
		"Cold budget at the Hot floor must skip the enqueue (no crash)")
}
