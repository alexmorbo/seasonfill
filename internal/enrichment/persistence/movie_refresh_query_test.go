package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedMovie inserts a stub movie with the given tmdb_id, then (optionally)
// stamps enrichment_tmdb_synced_at / tmdb_changed_at directly. A nil pointer
// leaves the column NULL. Returns the assigned movie id. tmdb_changed_at is set
// via UpdateColumns (the marker's grep-AC column) because Upsert never writes it.
func seedMovie(t *testing.T, db *gorm.DB, tmdbID int, syncedAt, changedAt *time.Time) domain.MovieID {
	t.Helper()
	repo := NewMovieRepository(db)
	tid := domain.TMDBID(tmdbID)
	id, err := repo.Upsert(context.Background(), movie.Canon{
		TMDBID:    &tid,
		Title:     fmt.Sprintf("m%d", tmdbID),
		Hydration: movie.HydrationStub,
	})
	require.NoError(t, err)
	updates := map[string]any{}
	if syncedAt != nil {
		updates["enrichment_tmdb_synced_at"] = syncedAt.UTC()
	}
	if changedAt != nil {
		updates["tmdb_changed_at"] = changedAt.UTC()
	}
	if len(updates) > 0 {
		require.NoError(t, db.Model(&database.MovieModel{}).Where("id = ?", id).UpdateColumns(updates).Error)
	}
	return id
}

// markMovieSections stamps the four per-section enrichment clocks
// (enrichment_{cast,keywords,recs,media}_synced_at) on an existing movie. A nil
// pointer leaves that column NULL. Lets a test place a movie in the "fully
// processed" state (all 4 non-NULL) or reproduce the pre-Ф1 hole (fresh tmdb
// clock, one-or-more section clock NULL).
func markMovieSections(t *testing.T, db *gorm.DB, id domain.MovieID, cast, keywords, recs, media *time.Time) {
	t.Helper()
	updates := map[string]any{}
	if cast != nil {
		updates["enrichment_cast_synced_at"] = cast.UTC()
	}
	if keywords != nil {
		updates["enrichment_keywords_synced_at"] = keywords.UTC()
	}
	if recs != nil {
		updates["enrichment_recs_synced_at"] = recs.UTC()
	}
	if media != nil {
		updates["enrichment_media_synced_at"] = media.UTC()
	}
	if len(updates) == 0 {
		return
	}
	require.NoError(t, db.Model(&database.MovieModel{}).Where("id = ?", id).UpdateColumns(updates).Error)
}

// seedMovieI18nRow seeds a movie_i18n row with explicit title/overview (nil → NULL). The
// heal picker (S-HEAL-FIX) keys recency off movies.enrichment_text_synced_at, NOT this
// row's enriched_at, so enriched_at is set to `now` here purely to satisfy NOT NULL-free
// columns; tests turn the anti-storm knob via stampAttempt below.
func seedMovieI18nRow(t *testing.T, db *gorm.DB, id domain.MovieID, lang string, title, overview *string) {
	t.Helper()
	n := time.Now().UTC()
	require.NoError(t, db.Create(&database.MovieI18nModel{
		MovieID: id, Language: lang, Title: title, Overview: overview,
		EnrichedAt: &n, UpdatedAt: n,
	}).Error)
}

// stampAttempt sets movies.enrichment_text_synced_at (the always-advancing heal attempt
// clock) to the given time, placing the movie inside or outside the 6h heal window.
func stampAttempt(t *testing.T, db *gorm.DB, id domain.MovieID, at time.Time) {
	t.Helper()
	require.NoError(t, db.Model(&database.MovieModel{}).Where("id = ?", id).
		UpdateColumns(map[string]any{"enrichment_text_synced_at": at.UTC()}).Error)
}

// TestMovieRepository_PickMovieRefreshCandidates covers the 2-tier picker:
// CHANGED before NORMAL, NULL-sync first within a tier, the 15m race guard
// excluding a just-stamped changed movie, anti-double-pick (a changed+stale
// movie appears exactly once, in tier 0), limit, and tmdb-less exclusion.
func TestMovieRepository_PickMovieRefreshCandidates(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
			old := now.Add(-40 * 24 * time.Hour) // older than Normal TTL (14d)
			recent := now.Add(-2 * time.Minute)  // inside the 15m race guard
			ttl := enrichment.DefaultRefreshTTL()

			// changedStale: tmdb_changed_at set, sync old → CHANGED tier, race-OK.
			changedStale := seedMovie(t, db, 100, new(old), new(now.Add(-1*time.Hour)))
			// changedNeverSynced: tmdb_changed_at set, sync NULL → CHANGED, sorts first.
			changedNeverSynced := seedMovie(t, db, 101, nil, new(now.Add(-1*time.Hour)))
			// changedButRaceGuarded: changed, but synced 2m ago → EXCLUDED (mid-Handle).
			rg := seedMovie(t, db, 102, new(recent), new(now.Add(-1*time.Hour)))
			// normalStale: no change flag, sync old → NORMAL tier.
			normalStale := seedMovie(t, db, 200, new(old), nil)
			// normalFresh: no change flag, synced just now → EXCLUDED (within TTL).
			nf := seedMovie(t, db, 201, new(now), nil)
			// Ф1.4: both are fully-processed (all 4 section stamps non-NULL) so the new
			// NULL-section OR does NOT pull them into NORMAL — they must stay excluded on
			// their tmdb-path grounds (race guard / TTL) exactly as before.
			markMovieSections(t, db, rg, new(recent), new(recent), new(recent), new(recent))
			markMovieSections(t, db, nf, new(now), new(now), new(now), new(now))
			// S3: both are text-attempted (enrichment_text_synced_at non-NULL) so the new
			// i18n-coverage OR does NOT re-pick them — they stay excluded on their
			// tmdb-path grounds (race guard / TTL), isolating this test to those grounds.
			require.NoError(t, repo.MarkTextSynced(ctx, rg, recent))
			require.NoError(t, repo.MarkTextSynced(ctx, nf, now))

			// tmdbless: a Radarr orphan (tmdb_id NULL) → NEVER picked.
			_, err := repo.Upsert(ctx, movie.Canon{Title: "orphan", Hydration: movie.HydrationStub})
			require.NoError(t, err)

			got, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 50)
			require.NoError(t, err)

			// Expected order: CHANGED (NULL-sync first, then older sync), then NORMAL.
			require.Len(t, got, 3, "want changedNeverSynced, changedStale, normalStale; got %+v", got)
			assert.Equal(t, changedNeverSynced, got[0].MovieID)
			assert.Equal(t, enrichment.RefreshTierChanged, got[0].Tier)
			assert.Equal(t, changedStale, got[1].MovieID)
			assert.Equal(t, enrichment.RefreshTierChanged, got[1].Tier)
			assert.Equal(t, normalStale, got[2].MovieID)
			assert.Equal(t, enrichment.RefreshTierNormal, got[2].Tier)

			// Anti-double-pick: changedStale is also TTL-stale but appears ONLY once.
			seen := map[domain.MovieID]int{}
			for _, c := range got {
				seen[c.MovieID]++
			}
			assert.Equal(t, 1, seen[changedStale], "changed+stale movie must appear exactly once")

			// LIMIT applies across the union.
			lim, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 2)
			require.NoError(t, err)
			require.Len(t, lim, 2)
			assert.Equal(t, changedNeverSynced, lim[0].MovieID)
			assert.Equal(t, changedStale, lim[1].MovieID)
		})
	}
}

// TestMovieRepository_PickMovieRefreshCandidates_NullSectionBackfill covers Ф1.4:
// a movie with a FRESH enrichment_tmdb_synced_at but a NULL section stamp
// (cast/keywords/recs/media) is re-picked into the NORMAL tier so the pre-Ф1
// section holes get filled; a fully-stamped movie (all 4 non-NULL) is NOT
// re-picked; and a movie whose sections are empty-but-STAMPED is not re-picked
// (the picker keys off the stamp column, not row counts → no churn).
func TestMovieRepository_PickMovieRefreshCandidates_NullSectionBackfill(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
			fresh := now.Add(-1 * time.Hour) // well within Normal TTL (14d), no change flag
			ttl := enrichment.DefaultRefreshTTL()

			// (a) fresh tmdb clock, cast section NULL (other 3 stamped) → PICKED (NORMAL).
			//     This is the movie-558449 hole: enriched before the Ф1 cast writer.
			nullCast := seedMovie(t, db, 300, new(fresh), nil)
			markMovieSections(t, db, nullCast, nil, new(fresh), new(fresh), new(fresh))

			// (b) fully-processed: all 4 section stamps non-NULL, tmdb fresh → NOT picked.
			fullStamped := seedMovie(t, db, 301, new(fresh), nil)
			markMovieSections(t, db, fullStamped, new(fresh), new(fresh), new(fresh), new(fresh))

			// (c) empty-section-but-STAMPED: all 4 stamped, zero child rows seeded
			//     (mirrors the worker's "checked, empty" stamp-only tx) → NOT re-picked.
			//     Proves the picker keys off the stamp column, not section row counts.
			emptyStamped := seedMovie(t, db, 302, new(fresh), nil)
			markMovieSections(t, db, emptyStamped, new(fresh), new(fresh), new(fresh), new(fresh))

			// S3: both excluded fixtures are text-attempted, so the new i18n-coverage OR
			// does not re-pick them — this test stays scoped to the NULL-section predicate.
			require.NoError(t, repo.MarkTextSynced(ctx, fullStamped, fresh))
			require.NoError(t, repo.MarkTextSynced(ctx, emptyStamped, fresh))

			got, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 50)
			require.NoError(t, err)

			ids := map[domain.MovieID]enrichment.RefreshTier{}
			for _, c := range got {
				ids[c.MovieID] = c.Tier
			}

			// (a) picked, in NORMAL tier.
			tier, ok := ids[nullCast]
			require.True(t, ok, "movie with NULL cast stamp must be re-picked; got %+v", got)
			assert.Equal(t, enrichment.RefreshTierNormal, tier)

			// (b) fully-stamped, tmdb-fresh → absent.
			_, ok = ids[fullStamped]
			assert.False(t, ok, "fully-stamped fresh movie must NOT be picked (no churn)")

			// (c) empty-but-stamped → absent (idempotent, no re-pick storm).
			_, ok = ids[emptyStamped]
			assert.False(t, ok, "empty-but-stamped movie must NOT be re-picked")

			// Negative-space guard: with only these 3 seeded, exactly one is pickable.
			require.Len(t, got, 1, "only the NULL-section movie is pickable; got %+v", got)
		})
	}
}

// TestMovieRepository_PickMovieRefreshCandidates_RollingI18nGap covers S-HEAL: the
// NORMAL-arm rolling i18n-gap branch re-picks a WARM-INCOMPLETE movie (localized
// ru-RU row empty) ONLY when its enriched_at is past the recheck window, keyed on
// movie_i18n.enriched_at (NOT the one-shot enrichment_text_synced_at). All movies
// here are tmdb-fresh with all 4 section stamps non-NULL, so ONLY the i18n branch
// can pull them into NORMAL — isolating the predicate under test. Also asserts the
// Heal flag is set exactly for the gap picks.
func TestMovieRepository_PickMovieRefreshCandidates_RollingI18nGap(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
			fresh := now.Add(-1 * time.Hour)         // within Normal TTL, no change flag
			oldAttempt := now.Add(-8 * time.Hour)    // attempt clock older than the 6h window
			recentAttempt := now.Add(-1 * time.Hour) // attempt clock inside the 6h window
			ttl := enrichment.DefaultRefreshTTL()
			s := func(v string) *string { return &v }
			allFresh := func(id domain.MovieID) {
				markMovieSections(t, db, id, new(fresh), new(fresh), new(fresh), new(fresh))
			}

			// (a) SELF-HEAL: warm-incomplete — ru-RU row with empty TITLE, overview
			//     present, attempt clock STALE (>6h) → re-picked. THE ~9,944 case.
			healEmptyTitle := seedMovie(t, db, 700, new(fresh), nil)
			allFresh(healEmptyTitle)
			seedMovieI18nRow(t, db, healEmptyTitle, "ru-RU", nil, s("русское описание"))
			stampAttempt(t, db, healEmptyTitle, oldAttempt)

			// (b) ANTI-STORM (fresh row): SAME shape but attempt clock RECENT (<6h) → NOT
			//     re-picked (no per-tick storm; MarkTextSynced just advanced the clock).
			antiStormFresh := seedMovie(t, db, 701, new(fresh), nil)
			allFresh(antiStormFresh)
			seedMovieI18nRow(t, db, antiStormFresh, "ru-RU", nil, s("описание"))
			stampAttempt(t, db, antiStormFresh, recentAttempt)

			// (c) GATED OUT (no row): genuinely-untranslated — NO ru-RU row at all →
			//     NOT re-picked (row-exists gate; TMDB has no ru, re-fetch is wasted).
			untranslated := seedMovie(t, db, 702, new(fresh), nil)
			allFresh(untranslated)

			// (d) COVERED: ru-RU title AND overview present (non-empty), attempt clock
			//     stale → NOT re-picked (no empty field, nothing to heal).
			covered := seedMovie(t, db, 703, new(fresh), nil)
			allFresh(covered)
			seedMovieI18nRow(t, db, covered, "ru-RU", s("Название"), s("описание"))
			stampAttempt(t, db, covered, oldAttempt)

			// (e) SELF-HEAL empty OVERVIEW: title present, overview '' (empty string),
			//     attempt clock stale → re-picked (empty '' treated as missing).
			healEmptyOverview := seedMovie(t, db, 704, new(fresh), nil)
			allFresh(healEmptyOverview)
			seedMovieI18nRow(t, db, healEmptyOverview, "ru-RU", s("Название"), s(""))
			stampAttempt(t, db, healEmptyOverview, oldAttempt)

			// (f) NULL attempt clock: empty title, enrichment_text_synced_at NULL → re-picked
			//     (NULL clock is always due).
			healNullClock := seedMovie(t, db, 705, new(fresh), nil)
			allFresh(healNullClock)
			seedMovieI18nRow(t, db, healNullClock, "ru-RU", nil, s("описание"))
			// (no stampAttempt → enrichment_text_synced_at stays NULL)

			got, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 50)
			require.NoError(t, err)

			picked := map[domain.MovieID]MovieRefreshCandidate{}
			for _, c := range got {
				picked[c.MovieID] = c
			}

			// Pickables: (a), (e), (f) — each with Heal=true, NORMAL tier.
			for _, id := range []domain.MovieID{healEmptyTitle, healEmptyOverview, healNullClock} {
				c, ok := picked[id]
				require.True(t, ok, "movie %d (stale empty i18n) must be re-picked; got %+v", id, got)
				assert.Equal(t, enrichment.RefreshTierNormal, c.Tier)
				assert.True(t, c.Heal, "movie %d must carry Heal=true (gap pick)", id)
			}

			// Non-pickables: (b) fresh-clock anti-storm, (c) no row, (d) covered.
			for _, id := range []domain.MovieID{antiStormFresh, untranslated, covered} {
				_, ok := picked[id]
				assert.False(t, ok, "movie %d must NOT be re-picked; got %+v", id, got)
			}

			require.Len(t, got, 3, "exactly the 3 stale-empty movies are pickable; got %+v", got)
		})
	}
}

// TestMovieRepository_PickMovieRefreshCandidates_GapOrdering covers F-04: within the
// NORMAL tier a GAP movie with a RECENT tmdb synced_at sorts AHEAD of a COMPLETE
// movie with an OLD tmdb synced_at, because is_gap DESC precedes the synced_at sort.
func TestMovieRepository_PickMovieRefreshCandidates_GapOrdering(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
			recentTmdb := now.Add(-1 * time.Hour)    // within TTL
			oldTmdb := now.Add(-40 * 24 * time.Hour) // older than Normal TTL (14d)
			oldAttempt := now.Add(-8 * time.Hour)    // attempt clock past the 6h heal window
			ttl := enrichment.DefaultRefreshTTL()
			s := func(v string) *string { return &v }

			// gapRecent: tmdb FRESH, all sections stamped, ru row empty-title + stale
			//   attempt clock → picked ONLY via the i18n gap (is_gap=1).
			gapRecent := seedMovie(t, db, 800, new(recentTmdb), nil)
			markMovieSections(t, db, gapRecent, new(recentTmdb), new(recentTmdb), new(recentTmdb), new(recentTmdb))
			seedMovieI18nRow(t, db, gapRecent, "ru-RU", nil, s("описание"))
			stampAttempt(t, db, gapRecent, oldAttempt)

			// completeOld: tmdb STALE (picked via tmdb-stale OR), full ru coverage →
			//   is_gap=0. Older synced_at than gapRecent.
			completeOld := seedMovie(t, db, 801, new(oldTmdb), nil)
			markMovieSections(t, db, completeOld, new(oldTmdb), new(oldTmdb), new(oldTmdb), new(oldTmdb))
			seedMovieI18nRow(t, db, completeOld, "ru-RU", s("Название"), s("описание"))
			stampAttempt(t, db, completeOld, oldAttempt)

			got, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 50)
			require.NoError(t, err)
			require.Len(t, got, 2, "both movies pickable; got %+v", got)

			// is_gap DESC wins over the older synced_at: the gap movie leads.
			assert.Equal(t, gapRecent, got[0].MovieID, "gap movie must sort first (is_gap DESC)")
			assert.True(t, got[0].Heal)
			assert.Equal(t, completeOld, got[1].MovieID)
			assert.False(t, got[1].Heal)
		})
	}
}

// TestMovieRepository_PickMovieRefreshCandidates_HealAttemptClock is the S-HEAL-FIX core:
// the ru-RU text-heal arm re-selects a title-empty/overview-present row keyed on the
// ALWAYS-ADVANCING movies.enrichment_text_synced_at attempt clock (window 6h), IGNORING
// movie_i18n.enriched_at recency. Four named sub-assertions: fixable-drains, floor-no-storm,
// title-fills-exits, series-untouched. Every movie here is tmdb-fresh with all 4 section
// stamps non-NULL, so ONLY the i18n heal arm can pull it into NORMAL — isolating the
// predicate under test.
func TestMovieRepository_PickMovieRefreshCandidates_HealAttemptClock(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			s := func(v string) *string { return &v }

			t.Run("fixable-drains", func(t *testing.T) {
				db := backend.NewDB(t)
				repo := NewMovieRepository(db)
				ctx := context.Background()
				now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
				fresh := now.Add(-1 * time.Hour)
				ttl := enrichment.DefaultRefreshTTL()

				// title-empty + overview-present, all sections fresh, attempt clock OLD
				// (>6h) → SELECTED with Heal=true, EVEN THOUGH the i18n row's enriched_at
				// is 'now' (recent). Proves enriched_at recency no longer suppresses.
				id := seedMovie(t, db, 900, new(fresh), nil)
				markMovieSections(t, db, id, new(fresh), new(fresh), new(fresh), new(fresh))
				seedMovieI18nRow(t, db, id, "ru-RU", nil, s("русское описание"))
				stampAttempt(t, db, id, now.Add(-8*time.Hour))

				got, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 50)
				require.NoError(t, err)
				require.Len(t, got, 1, "the fixable row must be selected; got %+v", got)
				assert.Equal(t, id, got[0].MovieID)
				assert.True(t, got[0].Heal, "selected via the heal arm")
				assert.Equal(t, enrichment.RefreshTierNormal, got[0].Tier)

				// Writer lands the title (simulate a successful attempt): title non-empty
				// + attempt clock advanced to now → row exits the heal set.
				require.NoError(t, db.Model(&database.MovieI18nModel{}).
					Where("movie_id = ? AND language = ?", id, "ru-RU").
					UpdateColumns(map[string]any{"title": "Название"}).Error)
				stampAttempt(t, db, id, now)

				got2, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 50)
				require.NoError(t, err)
				require.Len(t, got2, 0, "healed row must not be re-selected; got %+v", got2)
			})

			t.Run("floor-no-storm", func(t *testing.T) {
				db := backend.NewDB(t)
				repo := NewMovieRepository(db)
				ctx := context.Background()
				now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
				fresh := now.Add(-1 * time.Hour)
				ttl := enrichment.DefaultRefreshTTL()

				// A genuine floor row: title-empty + overview-EMPTY (TMDB has no ru at
				// all beyond a stub). Attempt clock stamped to `now` (just attempted).
				id := seedMovie(t, db, 901, new(fresh), nil)
				markMovieSections(t, db, id, new(fresh), new(fresh), new(fresh), new(fresh))
				seedMovieI18nRow(t, db, id, "ru-RU", nil, nil)
				stampAttempt(t, db, id, now)

				// Immediate second tick (same `now`): attempt clock is inside 6h → the
				// floor row is NOT re-selected. This is the anti-storm guarantee: the
				// floor does not consume the batch every tick.
				got, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 50)
				require.NoError(t, err)
				require.Len(t, got, 0, "floor row must be suppressed within the window; got %+v", got)

				// >6h later → re-attempted exactly once more (capped, not every tick).
				later := now.Add(7 * time.Hour)
				got2, err := repo.PickMovieRefreshCandidates(ctx, later, ttl, 50)
				require.NoError(t, err)
				require.Len(t, got2, 1, "floor row re-attempted once the window elapses; got %+v", got2)
				assert.Equal(t, id, got2[0].MovieID)
				assert.True(t, got2[0].Heal)
			})

			t.Run("title-fills-exits", func(t *testing.T) {
				db := backend.NewDB(t)
				repo := NewMovieRepository(db)
				ctx := context.Background()
				now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
				fresh := now.Add(-1 * time.Hour)
				ttl := enrichment.DefaultRefreshTTL()

				// Title AND overview present, attempt clock OLD → still NOT selected,
				// proving the predicate excludes non-empty rows regardless of clock age
				// (no storm on healthy rows).
				id := seedMovie(t, db, 902, new(fresh), nil)
				markMovieSections(t, db, id, new(fresh), new(fresh), new(fresh), new(fresh))
				seedMovieI18nRow(t, db, id, "ru-RU", s("Название"), s("описание"))
				stampAttempt(t, db, id, now.Add(-30*24*time.Hour))

				got, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 50)
				require.NoError(t, err)
				require.Len(t, got, 0, "healthy row must never be selected; got %+v", got)
			})

			t.Run("series-untouched", func(t *testing.T) {
				db := backend.NewDB(t)
				mrepo := NewMovieRepository(db)
				srepo := NewSeriesRepository(db)
				ctx := context.Background()
				now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
				fresh := now.Add(-1 * time.Hour)
				ttl := enrichment.DefaultRefreshTTL()

				// A movie with a heal gap (selectable) ...
				mid := seedMovie(t, db, 903, new(fresh), nil)
				markMovieSections(t, db, mid, new(fresh), new(fresh), new(fresh), new(fresh))
				seedMovieI18nRow(t, db, mid, "ru-RU", nil, s("описание"))
				stampAttempt(t, db, mid, now.Add(-8*time.Hour))

				// ... plus a series with the analogous shape. The MOVIE picker reads only
				// the movies/movie_i18n tables, so it must NEVER surface a series id — a
				// structural no-series-regression guard. (The series picker + its own
				// series.enrichment_text_synced_at predicate are untouched by this story;
				// the full series_refresh_query_test.go suite must remain green.)
				sid, err := srepo.Upsert(ctx, sampleCanon("heal-isolation"))
				require.NoError(t, err)
				require.NotZero(t, sid, "series stub must be seeded (coexists with the movie gap)")

				// The movie picker reads only movies/movie_i18n, so despite a series row
				// carrying the analogous shape it returns EXACTLY the one movie. (movies
				// and series live in separate id spaces, so a numeric id compare is not a
				// valid cross-table guard; the len==1 + Equal(mid) below IS the guard that
				// no series row leaked into the movie picker's output.)
				got, err := mrepo.PickMovieRefreshCandidates(ctx, now, ttl, 50)
				require.NoError(t, err)
				require.Len(t, got, 1, "only the movie is pickable by the movie picker; got %+v", got)
				assert.Equal(t, mid, got[0].MovieID, "movie picker must surface only the movie row")
			})
		})
	}
}
