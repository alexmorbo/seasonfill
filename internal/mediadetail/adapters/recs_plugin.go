package adapters

import (
	"context"
	"time"

	mdengdomain "github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
)

// recsGapRecheck bounds how often a media item whose localized recommendation-title
// coverage sits BELOW the bar is re-refreshed: at most once per window, keyed on
// enrichment_recs_synced_at. GATE-ZERO F-05 proved TMDB localizes movie rec titles,
// but coverage is PERMANENTLY partial (movie 787 = 0/20 ru at first touch, global
// ~5.6%) because most recommended movies have never been per-lang drained — so an
// open must never storm RefreshRecommendations every time. Mirror of castNameGapRecheck.
const recsGapRecheck = 7 * 24 * time.Hour

// recsCoverageMinPct is the localized-rec-title coverage floor (percent). Mirrors
// castCoverageMinPct: a rail is "covered enough" at >=80% so a handful of
// untranslated rec titles never keeps a media item perpetually stale.
const recsCoverageMinPct = 80

// RecsPort is the per-(MediaType) seam the shared recsPlugin drives, keyed on the
// internal surrogate id (int64) so ONE plugin serves both verticals with zero
// `if type` branching (ADR-0022 D3): the movie adapter converts to domain.MovieID,
// the series adapter to domain.SeriesID.
type RecsPort interface {
	// Coverage reports (covered, total) localized rec-title coverage for
	// (internalID, lang): total = distinct recommended items, covered = those whose
	// per-language title side-table row is non-empty. (0,0) when no recommendations.
	Coverage(ctx context.Context, internalID int64, lang string) (covered, total int, err error)
	// RecsSyncedAt returns enrichment_recs_synced_at for internalID (nil when the
	// recs section was never written). Keys the recheck window.
	RecsSyncedAt(ctx context.Context, internalID int64) (*time.Time, error)
	// Refresh drives the localized rec-title write (RefreshRecommendations) for
	// (internalID, lang). Idempotent + COALESCE-safe by contract.
	Refresh(ctx context.Context, internalID int64, lang string) error
}

// recsPlugin implements the engine SectionPlugin for SectionRecs, for EITHER media
// type. Rec-title freshness is fractional (per-lang title side-table coverage), but
// the engine's Coverage arm is a hard 100% bar with NO anti-storm — a rail with ANY
// untranslated rec title would storm RefreshRecommendations on EVERY open (F-01). So
// this plugin rides the STALENESS arm and NO-OPs Coverage: it folds "coverage below
// the 80% bar AND enrichment_recs_synced_at NULL/older-than-recsGapRecheck" into the
// boolean verdict. Symmetric with castPlugin's Staleness-arm design.
type recsPlugin struct {
	port     RecsPort
	baseLang string
	minPct   int
	recheck  time.Duration
}

// NewRecsPlugin constructs the shared recs plugin over port. baseLang is
// locale.Default(); the per-lang coverage arm is skipped for "" / baseLang because
// the recs rail resolves base-lang titles from the canon row (no side-table row
// required).
func NewRecsPlugin(port RecsPort, baseLang string) mdengSectionPlugin {
	return &recsPlugin{port: port, baseLang: baseLang, minPct: recsCoverageMinPct, recheck: recsGapRecheck}
}

// Section is the canonical recs section.
func (p *recsPlugin) Section() mdengdomain.Section { return mdengdomain.SectionRecs }

// Coverage NO-OP: recs rides the Staleness arm (anti-storm), so total==0 tells the
// engine to defer to Staleness.
func (p *recsPlugin) Coverage(context.Context, mdengdomain.MediaID, string) (int, int, error) {
	return 0, 0, nil
}

// Staleness returns Stale=true ONLY when localized rec-title coverage is below the
// bar AND the recs clock is NULL or older than the recheck window. Read errors
// return (verdict, err); the engine's assess() fails CLOSED on a Staleness error
// (treats as not-stale), so a flaky read never forces a synchronous refresh.
func (p *recsPlugin) Staleness(ctx context.Context, id mdengdomain.MediaID, lang string, now time.Time) (mdengdomain.SectionVerdict, error) {
	v := mdengdomain.SectionVerdict{Section: mdengdomain.SectionRecs}
	// Base-lang / no-lang: rec titles resolve from the canon row; nothing per-language
	// to cover (mirror castPlugin's base-lang short-circuit).
	if lang == "" || lang == p.baseLang {
		v.Reason = "base_lang"
		return v, nil
	}
	covered, total, err := p.port.Coverage(ctx, id.InternalID(), lang)
	if err != nil {
		return v, err
	}
	if total == 0 {
		v.Reason = "no_recs"
		return v, nil
	}
	if covered*100 >= total*p.minPct {
		v.Reason = "covered"
		return v, nil
	}
	// Below the bar → gate on the recheck window (anti-storm).
	syncedAt, err := p.port.RecsSyncedAt(ctx, id.InternalID())
	if err != nil {
		return v, err
	}
	if syncedAt == nil {
		v.Stale, v.Reason = true, "never_synced"
		return v, nil
	}
	if now.Sub(*syncedAt) >= p.recheck {
		v.Stale, v.Reason = true, "gap_recheck"
		return v, nil
	}
	v.Reason = "within_recheck_window"
	return v, nil
}

// Refresh drives the port's localized rec-title write for id+lang.
func (p *recsPlugin) Refresh(ctx context.Context, id mdengdomain.MediaID, lang string) error {
	return p.port.Refresh(ctx, id.InternalID(), lang)
}
