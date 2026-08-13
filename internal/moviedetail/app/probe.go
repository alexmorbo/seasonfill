package app

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
)

// MovieSection identifies an enrichment section of a movie for the on-read
// hydration probe (Ф1.2). Mirror of the series freshener Section, adapted to the
// 5 movie section stamps landed by migration 000061 + the Ф1.1 writers.
type MovieSection string

const (
	MovieSectionText     MovieSection = "text"     // enrichment_text_synced_at
	MovieSectionCast     MovieSection = "cast"     // enrichment_cast_synced_at
	MovieSectionRecs     MovieSection = "recs"     // enrichment_recs_synced_at
	MovieSectionMedia    MovieSection = "media"    // enrichment_media_synced_at
	MovieSectionKeywords MovieSection = "keywords" // enrichment_keywords_synced_at
)

// MovieFixedSections is the canonical DENSE probe order.
var MovieFixedSections = []MovieSection{
	MovieSectionText,
	MovieSectionCast,
	MovieSectionRecs,
	MovieSectionMedia,
	MovieSectionKeywords,
}

// Flat, generous per-section TTLs. Movies change far less than series, so a
// single loose upper bound per section is sufficient — no status-aware curve.
// Any section past its TTL (or never stamped, or a stub movie) makes the movie
// eligible for one mark-stale nudge.
const (
	movieTextTTL     = 7 * 24 * time.Hour
	movieCastTTL     = 30 * 24 * time.Hour
	movieRecsTTL     = 30 * 24 * time.Hour
	movieMediaTTL    = 30 * 24 * time.Hour
	movieKeywordsTTL = 30 * 24 * time.Hour
)

// MovieSectionVerdict is one per-section freshness result. Pure value — the
// probe never writes and never does IO (all inputs are already-loaded canon
// fields). Reason ∈ {"never","expired","stub","fresh"}.
type MovieSectionVerdict struct {
	Section  MovieSection
	Stale    bool
	Reason   string
	SyncedAt *time.Time
}

// MovieProbe returns a DENSE verdict per MovieFixedSections for the given canon.
// A stub-hydration movie (Hydration != HydrationFull) → every section stale
// (reason "stub"), mirroring the series probe's stub branch. Otherwise each
// section is stale when its stamp is nil ("never") or older than its TTL
// ("expired"). Pure function: no IO, clock injected via now.
func MovieProbe(canon movie.Canon, now time.Time) []MovieSectionVerdict {
	if canon.Hydration != movie.HydrationFull {
		out := make([]MovieSectionVerdict, 0, len(MovieFixedSections))
		for _, s := range MovieFixedSections {
			out = append(out, MovieSectionVerdict{Section: s, Stale: true, Reason: "stub"})
		}
		return out
	}

	specs := []struct {
		section  MovieSection
		syncedAt *time.Time
		ttl      time.Duration
	}{
		{MovieSectionText, canon.EnrichmentTextSyncedAt, movieTextTTL},
		{MovieSectionCast, canon.EnrichmentCastSyncedAt, movieCastTTL},
		{MovieSectionRecs, canon.EnrichmentRecsSyncedAt, movieRecsTTL},
		{MovieSectionMedia, canon.EnrichmentMediaSyncedAt, movieMediaTTL},
		{MovieSectionKeywords, canon.EnrichmentKeywordsSyncedAt, movieKeywordsTTL},
	}
	out := make([]MovieSectionVerdict, 0, len(specs))
	for _, s := range specs {
		stale, reason := movieSectionStale(s.syncedAt, s.ttl, now)
		out = append(out, MovieSectionVerdict{
			Section: s.section, Stale: stale, Reason: reason, SyncedAt: s.syncedAt,
		})
	}
	return out
}

// AnyStale reports whether any section verdict is stale. The moviedetail wiring
// collapses the dense verdict to this single bool (movies have no per-section
// dispatcher — the only action is one whole-movie mark-stale).
func AnyStale(verdicts []MovieSectionVerdict) bool {
	for _, v := range verdicts {
		if v.Stale {
			return true
		}
	}
	return false
}

// movieSectionStale applies the flat TTL policy. nil stamp → never; age > ttl →
// expired; else fresh. Pure.
func movieSectionStale(syncedAt *time.Time, ttl time.Duration, now time.Time) (bool, string) {
	if syncedAt == nil {
		return true, "never"
	}
	if now.Sub(*syncedAt) > ttl {
		return true, "expired"
	}
	return false, "fresh"
}
