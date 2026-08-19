package adapters

import (
	"context"
	"time"

	mdengdomain "github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
)

// castNameGapRecheck bounds how often a media item whose localized cast-name
// coverage sits BELOW the bar is re-refreshed: at most once per window, keyed on
// enrichment_cast_synced_at. Mirrors movieTitleGapRecheck — the anti-storm guard
// for a cast whose names TMDB simply does not localize (GATE-ZERO F-04 proved some
// cast members have no ru translation, e.g. Cameron Seely on The Grinch), so an
// open never storms RefreshCast every time.
const castNameGapRecheck = 7 * 24 * time.Hour

// castCoverageMinPct is the localized-cast-name coverage floor (percent). Mirrors
// the series freshener's PeopleCoverageMinPct default (probe.go:177): a section is
// "covered enough" at >=80% so a handful of untranslatable names never keeps a
// media item perpetually stale.
const castCoverageMinPct = 80

// CastPort is the per-(MediaType) seam the shared castPlugin drives, keyed on the
// internal surrogate id (int64) so ONE plugin serves both verticals with zero
// `if type` branching (ADR-0022 D3): the movie adapter converts to domain.MovieID,
// the series adapter to domain.SeriesID.
type CastPort interface {
	// Coverage reports (covered, total) localized cast-name coverage for
	// (internalID, lang): total = distinct credited persons, covered = those with a
	// people_texts row (language == lang AND name IS NOT NULL). (0,0) when no cast.
	Coverage(ctx context.Context, internalID int64, lang string) (covered, total int, err error)
	// CastSyncedAt returns enrichment_cast_synced_at for internalID (nil when the
	// cast section was never written). Keys the recheck window.
	CastSyncedAt(ctx context.Context, internalID int64) (*time.Time, error)
	// Refresh drives the localized cast-name write (RefreshCast) for
	// (internalID, lang). Idempotent + COALESCE-safe by contract.
	Refresh(ctx context.Context, internalID int64, lang string) error
}

// castPlugin implements the engine SectionPlugin for SectionCast, for EITHER media
// type. Cast-name freshness is fractional (people_texts coverage), but the engine's
// Coverage arm is a hard 100% bar with NO anti-storm — a cast with ANY untranslatable
// name would storm RefreshCast on EVERY open (F-01/F-02). So this plugin rides the
// STALENESS arm and NO-OPs Coverage: it folds "coverage below the 80% bar AND
// enrichment_cast_synced_at NULL/older-than-castNameGapRecheck" into the boolean
// verdict. Symmetric with movieTextPlugin's Staleness-arm design (movie_text_plugin.go:54-84).
type castPlugin struct {
	port     CastPort
	baseLang string
	minPct   int
	recheck  time.Duration
}

// NewCastPlugin constructs the shared cast plugin over port. baseLang is
// locale.Default(); the per-lang coverage arm is skipped for "" / baseLang because
// the cast reader resolves base-lang names via people.original_name
// (ListByIDsWithNameFallback COALESCE ladder) with no people_texts row required.
func NewCastPlugin(port CastPort, baseLang string) mdengSectionPlugin {
	return &castPlugin{port: port, baseLang: baseLang, minPct: castCoverageMinPct, recheck: castNameGapRecheck}
}

// Section is the canonical cast section.
func (p *castPlugin) Section() mdengdomain.Section { return mdengdomain.SectionCast }

// Coverage NO-OP: cast rides the Staleness arm (anti-storm), so total==0 tells the
// engine to defer to Staleness.
func (p *castPlugin) Coverage(context.Context, mdengdomain.MediaID, string) (int, int, error) {
	return 0, 0, nil
}

// Staleness returns Stale=true ONLY when localized cast-name coverage is below the
// bar AND the cast clock is NULL or older than the recheck window. Read errors
// return (verdict, err); the engine's assess() fails CLOSED on a Staleness error
// (treats as not-stale), so a flaky read never forces a synchronous RefreshCast.
func (p *castPlugin) Staleness(ctx context.Context, id mdengdomain.MediaID, lang string, now time.Time) (mdengdomain.SectionVerdict, error) {
	v := mdengdomain.SectionVerdict{Section: mdengdomain.SectionCast}
	// Base-lang / no-lang: base names resolve via original_name fallback; nothing
	// per-language to cover (mirror movieTextPlugin's base-lang short-circuit).
	if lang == "" || lang == p.baseLang {
		v.Reason = "base_lang"
		return v, nil
	}
	covered, total, err := p.port.Coverage(ctx, id.InternalID(), lang)
	if err != nil {
		return v, err
	}
	if total == 0 {
		v.Reason = "no_cast"
		return v, nil
	}
	if covered*100 >= total*p.minPct {
		v.Reason = "covered"
		return v, nil
	}
	// Below the bar → gate on the recheck window (anti-storm).
	syncedAt, err := p.port.CastSyncedAt(ctx, id.InternalID())
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

// Refresh drives the port's localized cast-name write for id+lang.
func (p *castPlugin) Refresh(ctx context.Context, id mdengdomain.MediaID, lang string) error {
	return p.port.Refresh(ctx, id.InternalID(), lang)
}
