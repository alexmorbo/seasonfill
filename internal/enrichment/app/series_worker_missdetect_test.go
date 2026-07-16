package enrichment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// recordingMissMetric counts IncMiss calls. Satisfies ChangesMissMetric.
type recordingMissMetric struct{ n int }

func (m *recordingMissMetric) IncMiss() { m.n++ }

// missTV is the baseline after-payload. Whitelist fields (Status / air dates /
// runtime / original title) are populated so MapTVToCanon yields a full canon.
// Popularity / VoteAverage / VoteCount are set so the M-02 exclusion can be proven.
func missTV() *tmdb.TVResponse {
	return &tmdb.TVResponse{
		ID:             42,
		OriginalName:   "Show",
		Status:         "Returning Series",
		FirstAirDate:   "2020-01-01",
		LastAirDate:    "2024-06-01",
		EpisodeRunTime: []int{45},
		Popularity:     10.0,
		VoteAverage:    8.0,
		VoteCount:      1000,
	}
}

// missBefore builds the before-canon from a tv payload so the whitelist matches
// exactly when the after-tv is unchanged, then stamps a prior sync at 2026-07-10.
func missBefore(tv *tmdb.TVResponse) series.Canon {
	c := tmdb.MapTVToCanon(tv)
	c.ID = 1
	synced := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	c.EnrichmentTMDBSyncedAt = &synced
	return c
}

// coveredCursor is a cursor whose window end is past date(synced)+24h (2026-07-11).
func coveredCursor() *fakeCursorStore {
	return &fakeCursorStore{cur: enrichdomain.ChangeCursor{
		LastWindowEnd: time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC),
	}}
}

func missWorker(m ChangesMissMetric, c ChangesCursorStore) *SeriesWorker {
	return &SeriesWorker{deps: SeriesWorkerDeps{ChangesMiss: m, ChangesCursor: c}}
}

// AC-1: prior sync + whitelist diff (Status) + tmdb_changed_at NULL + covered
// cursor → miss counted exactly once.
func TestMissDetector_PriorSyncWhitelistDiff_Counted(t *testing.T) {
	t.Parallel()
	before := missBefore(missTV())
	after := missTV()
	after.Status = "Ended" // whitelisted diff

	rec := &recordingMissMetric{}
	w := missWorker(rec, coveredCursor())
	w.recordChangesMissIfDetected(context.Background(), before, after, quietLogger())

	assert.Equal(t, 1, rec.n, "a covered, unflagged whitelist change on a prior-synced series is a miss")
}

// AC-2: first population (EnrichmentTMDBSyncedAt nil) → NOT counted.
func TestMissDetector_FirstPopulation_NotCounted(t *testing.T) {
	t.Parallel()
	before := missBefore(missTV())
	before.EnrichmentTMDBSyncedAt = nil // never synced
	after := missTV()
	after.Status = "Ended"

	rec := &recordingMissMetric{}
	w := missWorker(rec, coveredCursor())
	w.recordChangesMissIfDetected(context.Background(), before, after, quietLogger())

	assert.Equal(t, 0, rec.n, "on-demand first population is not a miss")
}

// AC-3: correctly flagged (tmdb_changed_at > synced_at) → NOT counted.
func TestMissDetector_CorrectlyFlagged_NotCounted(t *testing.T) {
	t.Parallel()
	before := missBefore(missTV())
	flagged := before.EnrichmentTMDBSyncedAt.Add(1 * time.Hour)
	before.TMDBChangedAt = &flagged
	after := missTV()
	after.Status = "Ended"

	rec := &recordingMissMetric{}
	w := missWorker(rec, coveredCursor())
	w.recordChangesMissIfDetected(context.Background(), before, after, quietLogger())

	assert.Equal(t, 0, rec.n, "firehose flagged ahead of sync ⇒ not a miss")
}

// AC-4: rating/popularity-only drift (M-02 excluded fields) with all whitelist
// fields equal → NOT counted.
func TestMissDetector_RatingOnlyDrift_NotCounted(t *testing.T) {
	t.Parallel()
	before := missBefore(missTV())
	after := missTV()
	after.Popularity = 99.0 // excluded
	after.VoteAverage = 1.0 // excluded
	after.VoteCount = 5     // excluded
	// Status / air dates / runtime / original title unchanged.

	rec := &recordingMissMetric{}
	w := missWorker(rec, coveredCursor())
	w.recordChangesMissIfDetected(context.Background(), before, after, quietLogger())

	assert.Equal(t, 0, rec.n, "M-02: aggregate rating/popularity drift must not count")
}

// AC-5a: unpolled gap — cursor.LastWindowEnd < date(synced)+24h → NOT counted.
func TestMissDetector_UnpolledGap_NotCounted(t *testing.T) {
	t.Parallel()
	before := missBefore(missTV())
	after := missTV()
	after.Status = "Ended"
	// threshold = 2026-07-11; window end just short of it.
	store := &fakeCursorStore{cur: enrichdomain.ChangeCursor{
		LastWindowEnd: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC),
	}}

	rec := &recordingMissMetric{}
	w := missWorker(rec, store)
	w.recordChangesMissIfDetected(context.Background(), before, after, quietLogger())

	assert.Equal(t, 0, rec.n, "M-03: firehose did not cover the interval ⇒ not a miss")
}

// AC-5b: empty cursor (ErrNotFound + zero LastWindowEnd) → NOT counted
// (doubles as the dark-launch-inert proof: poller OFF ⇒ cursor never advances).
func TestMissDetector_EmptyCursor_DarkLaunchInert(t *testing.T) {
	t.Parallel()
	before := missBefore(missTV())
	after := missTV()
	after.Status = "Ended"

	// (i) ErrNotFound
	rec := &recordingMissMetric{}
	w := missWorker(rec, &fakeCursorStore{getErr: ports.ErrNotFound})
	w.recordChangesMissIfDetected(context.Background(), before, after, quietLogger())
	assert.Equal(t, 0, rec.n, "ErrNotFound cursor ⇒ inert")

	// (ii) zero-value cursor (LastWindowEnd IsZero)
	rec2 := &recordingMissMetric{}
	w2 := missWorker(rec2, &fakeCursorStore{})
	w2.recordChangesMissIfDetected(context.Background(), before, after, quietLogger())
	assert.Equal(t, 0, rec2.n, "zero cursor (dark-launch, poller OFF) ⇒ inert")
}

// AC-6: diff present only in a NON-whitelisted canon field (Homepage) while all
// whitelist fields are equal → NOT counted.
func TestMissDetector_NonWhitelistFieldOnly_NotCounted(t *testing.T) {
	t.Parallel()
	before := missBefore(missTV())
	after := missTV()
	after.Homepage = "https://example.com" // maps to canon.Homepage — not whitelisted

	rec := &recordingMissMetric{}
	w := missWorker(rec, coveredCursor())
	w.recordChangesMissIfDetected(context.Background(), before, after, quietLogger())

	assert.Equal(t, 0, rec.n, "non-whitelisted canon diff must not count")
}

// AC-7: nil metric OR nil cursor dep → helper no-ops without panic.
func TestMissDetector_NilDeps_NoPanic(t *testing.T) {
	t.Parallel()
	before := missBefore(missTV())
	after := missTV()
	after.Status = "Ended"

	// nil metric
	w1 := &SeriesWorker{deps: SeriesWorkerDeps{ChangesCursor: coveredCursor()}}
	assert.NotPanics(t, func() {
		w1.recordChangesMissIfDetected(context.Background(), before, after, quietLogger())
	})

	// nil cursor
	rec := &recordingMissMetric{}
	w2 := &SeriesWorker{deps: SeriesWorkerDeps{ChangesMiss: rec}}
	assert.NotPanics(t, func() {
		w2.recordChangesMissIfDetected(context.Background(), before, after, quietLogger())
	})
	assert.Equal(t, 0, rec.n)
}

// N1: per-field whitelist positive coverage — a diff in EACH whitelisted canon
// field independently (all else equal, prior sync, tmdb_changed_at NULL, covered
// cursor) must count exactly one miss. Guards against a silent drop of any single
// field from changesWhitelistCanonDiff. Each mutation is driven through the after
// tv payload the helper maps, mirroring the AC end-to-end tests.
func TestMissDetector_EachWhitelistField_Counted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(tv *tmdb.TVResponse)
	}{
		{"Status", func(tv *tmdb.TVResponse) { tv.Status = "Ended" }},
		{"FirstAirDate", func(tv *tmdb.TVResponse) { tv.FirstAirDate = "2019-02-02" }},
		{"LastAirDate", func(tv *tmdb.TVResponse) { tv.LastAirDate = "2025-07-07" }},
		{"NextAirDate", func(tv *tmdb.TVResponse) {
			tv.NextEpisodeToAir = &tmdb.TVEpisodeStub{AirDate: "2026-01-01"}
		}},
		{"RuntimeMinutes", func(tv *tmdb.TVResponse) { tv.EpisodeRunTime = []int{90} }},
		{"OriginalTitle", func(tv *tmdb.TVResponse) { tv.OriginalName = "Different" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := missBefore(missTV())
			after := missTV()
			tc.mutate(after)

			rec := &recordingMissMetric{}
			w := missWorker(rec, coveredCursor())
			w.recordChangesMissIfDetected(context.Background(), before, after, quietLogger())

			assert.Equal(t, 1, rec.n, "a covered, unflagged diff in %s must count exactly one miss", tc.name)
		})
	}
}

// N2: M-03 exact-boundary — cursor.LastWindowEnd == date(synced)+24h (the exact
// >= boundary, not strictly greater) COUNTS as covered → miss. Locks the >=
// (Before-negation) semantics against an accidental switch to strict >.
func TestMissDetector_CoverageExactBoundary_Counted(t *testing.T) {
	t.Parallel()
	before := missBefore(missTV())
	after := missTV()
	after.Status = "Ended"

	// synced = 2026-07-10 12:00 UTC ⇒ threshold = utc-midnight(synced)+24h =
	// 2026-07-11 00:00 UTC. Window end EXACTLY on the threshold must be covered.
	synced := *before.EnrichmentTMDBSyncedAt
	boundary := time.Date(synced.Year(), synced.Month(), synced.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	store := &fakeCursorStore{cur: enrichdomain.ChangeCursor{LastWindowEnd: boundary}}

	rec := &recordingMissMetric{}
	w := missWorker(rec, store)
	w.recordChangesMissIfDetected(context.Background(), before, after, quietLogger())

	assert.Equal(t, 1, rec.n, "LastWindowEnd exactly on date(synced)+24h is covered (>=) ⇒ miss")
}

// AC-8: once-per-Handle — a 2-language HandleForced fires the miss-detector
// exactly once (the whitelist fields are language-independent). Exercises the
// missChecked guard threaded through refreshOneLanguage end-to-end.
func TestMissDetector_OncePerHandle(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t, minimalTV(), map[int]*tmdb.SeasonResponse{1: minimalSeason()})
	f.worker.deps.Languages = []string{"en-US", "ru-RU"} // two passes → two GetTV
	rec := &recordingMissMetric{}
	f.worker.deps.ChangesMiss = rec
	// covered relative to a prior sync at 2026-06-01 (fixture clock = 2026-06-13).
	f.worker.deps.ChangesCursor = &fakeCursorStore{cur: enrichdomain.ChangeCursor{
		LastWindowEnd: time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC),
	}}

	tmdbID := domain.TMDBID(42)
	synced := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	// Seed canon: prior sync, tmdb_changed_at NULL, Status "Ended" — differs from
	// minimalTV's "Returning Series" ⇒ whitelist diff on the refresh.
	f.series.rows[1] = series.Canon{
		ID:                     1,
		TMDBID:                 &tmdbID,
		Status:                 new("Ended"),
		OriginalTitle:          new("Show"),
		EnrichmentTMDBSyncedAt: &synced,
	}

	// HandleForced bypasses the freshness gate so both language passes run.
	require.NoError(t, f.worker.HandleForced(context.Background(), 1))

	assert.Equal(t, 1, rec.n, "miss-detector must fire exactly once across a 2-language Handle")
	require.Equal(t, 2, f.tmdb.getTVHit, "sanity: both language passes fetched (2 GetTV)")
}

// W2-FIX L4: an EMPTY TMDB field (after canon nil) must NOT count as a diff — the
// COALESCE-guarded write persists nothing for empty values, so treating a
// non-nil→nil transition as a change is a FALSE miss. before.Status "Ended",
// after payload Status "" (→ nil canon), all other whitelist fields equal.
func TestMissDetector_EmptyAfterField_NotCounted(t *testing.T) {
	t.Parallel()
	before := missBefore(missTV())
	before.Status = new("Ended")
	after := missTV()
	after.Status = "" // MapTVToCanon → Status nil

	rec := &recordingMissMetric{}
	w := missWorker(rec, coveredCursor())
	w.recordChangesMissIfDetected(context.Background(), before, after, quietLogger())

	assert.Equal(t, 0, rec.n, "empty TMDB field (nil after) must not count as a miss (COALESCE-consistency)")
}

// W2-FIX L4 pure-fn: changesWhitelistCanonDiff is FALSE for an empty after-value
// and TRUE for a real value change — locks the "after non-nil AND differs" rule.
func TestChangesWhitelistCanonDiff_EmptyAfterVsRealChange(t *testing.T) {
	t.Parallel()
	// (1) non-nil before, nil after (empty TMDB field) → NOT a change.
	assert.False(t,
		changesWhitelistCanonDiff(series.Canon{Status: new("Ended")}, series.Canon{}),
		"nil after-value (empty TMDB field) must not count as a change")

	// (2) real Status change → still a change.
	assert.True(t,
		changesWhitelistCanonDiff(
			series.Canon{Status: new("Returning Series")},
			series.Canon{Status: new("Ended")}),
		"a real Status change must still be detected")
}
