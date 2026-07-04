package freshener

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	catalogseries "github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	dataports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// Probe answers "which sections of this series are stale for this
// lang?". F-R2-2 signature: DENSE 5 fixed + SPARSE per element of
// seasonNumbers. Pure read: never writes, never enqueues.
type Probe interface {
	IsStale(
		ctx context.Context,
		seriesID domain.SeriesID,
		lang values.LanguageTag,
		seasonNumbers []int,
	) ([]SectionVerdict, error)
}

// SeriesReader — narrow port reading canon (incl. all enrichment_*_synced_at).
type SeriesReader interface {
	Get(ctx context.Context, id domain.SeriesID) (catalogseries.Canon, error)
}

// SeriesTextsReader — narrow port reading the localised row with language fallback.
type SeriesTextsReader interface {
	GetWithFallback(ctx context.Context, seriesID domain.SeriesID, language string) (catalogseries.SeriesText, error)
}

// EpisodeTextsCoverageReader — narrow port answering "what fraction of
// episodes have a row in episode_texts for this lang?". Story 548
// catches partial coverage (Season 8 ru-RU enriched while 1-7 stay en).
type EpisodeTextsCoverageReader interface {
	CoverageBySeriesSeason(ctx context.Context, seriesID domain.SeriesID, seasonNumber int, language string) (covered, total int, err error)
}

// SeriesTextsCoverageReader — narrow port answering "what fraction of
// this series's recommendations have a row in series_texts for this
// lang?". Story 566 catches the en-US-stamp/ru-RU-empty gap: A3b fires
// TMDB /recommendations?language=en-US, N×UPSERT lands en-US rows,
// enrichment_recs_synced_at stamped → subsequent ru-RU visits see Fresh
// verdict and never trigger A3b(lang=ru-RU). Coverage check turns that
// into missing_recs_lang and lets EnsureFreshScope re-fire A3b until
// the gap closes.
type SeriesTextsCoverageReader interface {
	// RecommendationsCoverage returns (covered, total). covered =
	// distinct recommended_series_id which have a row in series_texts
	// with language == lang. total = distinct recommended_series_id
	// in series_recommendations for parentID. Returns (0, 0, nil) when
	// total == 0.
	RecommendationsCoverage(ctx context.Context, seriesID domain.SeriesID, language string) (covered, total int, err error)
}

// SeasonSyncedAtReader — narrow port reading
// seasons.episodes_synced_at for one (seriesID, seasonNumber). Returns
// (nil, dataports.ErrNotFound) if the season row is absent (caller
// treats as never synced).
type SeasonSyncedAtReader interface {
	GetEpisodesSyncedAt(ctx context.Context, seriesID domain.SeriesID, seasonNumber int) (*time.Time, error)
}

// DBProbeConfig — dep surface for the production Probe.
type DBProbeConfig struct {
	Series               SeriesReader
	SeriesTexts          SeriesTextsReader
	EpisodeTextsCoverage EpisodeTextsCoverageReader // optional — nil disables Story 548 check
	Seasons              SeasonSyncedAtReader

	// EpisodeCoverageMinPct — covered*100 < total*pct → stale.
	// Default 80 (Story 548 baseline).
	EpisodeCoverageMinPct int

	// SeriesTextsCoverage — optional. Story 566: probe checks that the
	// recommendations for the parent series have series_texts rows in the
	// requested lang, and marks SectionRecommendations Stale if coverage
	// falls below RecsCoverageMinPct. Nil disables the check (defensive
	// для non-prod wiring).
	SeriesTextsCoverage SeriesTextsCoverageReader

	// RecsCoverageMinPct — covered*100 < total*pct → stale. Default 80
	// (Story 548 baseline; symmetric across coverage checks).
	RecsCoverageMinPct int

	// Now is the clock injection. nil → time.Now.
	Now func() time.Time

	// Logger — fail-open events are logged at WarnLevel for grep.
	Logger *slog.Logger
}

// DBProbe is the production implementation backed by the catalog repositories.
type DBProbe struct {
	cfg DBProbeConfig
}

// Compile-time check.
var _ Probe = (*DBProbe)(nil)

// NewDBProbe wires the production Probe. Series + SeriesTexts +
// Seasons are required; EpisodeTextsCoverage optional; defaults applied.
func NewDBProbe(cfg DBProbeConfig) (*DBProbe, error) {
	if cfg.Series == nil || cfg.SeriesTexts == nil || cfg.Seasons == nil {
		return nil, errors.New("freshener: Series + SeriesTexts + Seasons readers required")
	}
	if cfg.EpisodeCoverageMinPct <= 0 || cfg.EpisodeCoverageMinPct > 100 {
		cfg.EpisodeCoverageMinPct = 80
	}
	if cfg.RecsCoverageMinPct <= 0 || cfg.RecsCoverageMinPct > 100 {
		cfg.RecsCoverageMinPct = 80
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	return &DBProbe{cfg: cfg}, nil
}

// IsStale — see Probe doc. DENSE 5 fixed verdicts + SPARSE 1 per element
// of seasonNumbers. Order: FixedSections in declaration order, then
// season verdicts in input order.
//
// Fail-open per Radarr lesson: any IO error → all known verdicts return
// Stale=true Reason="probe_error". Never propagates the error to the
// caller as a refusal-to-decide (the only error case is ctx cancel,
// where we surface it so the composer can abandon properly).
func (p *DBProbe) IsStale(
	ctx context.Context,
	seriesID domain.SeriesID,
	lang values.LanguageTag,
	seasonNumbers []int,
) ([]SectionVerdict, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("freshener: %w", err)
	}

	now := p.cfg.Now()
	verdicts := make([]SectionVerdict, 0, len(FixedSections)+len(seasonNumbers))

	canon, canonErr := p.cfg.Series.Get(ctx, seriesID)
	switch {
	case canonErr != nil && errors.Is(canonErr, dataports.ErrNotFound):
		// Defensive: caller usually validates existence first; treat as
		// "stub" so EnsureFreshScope dispatches a full refresh.
		return p.allStaleVerdicts(seriesID, lang, seasonNumbers, "stub", nil, 0), nil
	case canonErr != nil:
		p.cfg.Logger.WarnContext(ctx, "freshener.probe.canon_read_error",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("error", canonErr.Error()),
		)
		return p.allStaleVerdicts(seriesID, lang, seasonNumbers, "probe_error", nil, 0), nil
	}

	if canon.Hydration != catalogseries.HydrationFull {
		return p.allStaleVerdicts(seriesID, lang, seasonNumbers, "stub", nil, 0), nil
	}

	// One section at a time. Each branch reads its own synced_at field,
	// applies TTL policy, then layers in lang-coverage checks where
	// applicable. Predictable ordering matches FixedSections.

	// SectionSkeleton: canon-level signal (EnrichmentTMDBSyncedAt — the
	// Stage 1+2 stamp). Pre-A2 this is the only column that gets bumped.
	verdicts = append(verdicts, ttlSectionVerdict(
		SectionSkeleton, canon.EnrichmentTMDBSyncedAt, canon.Status,
		SectionTTLs[SectionSkeleton], now,
	))

	// SectionOverview: enrichment_text_synced_at + missing_lang check.
	// missing_lang short-circuits the TTL verdict — if we don't HAVE the
	// row in this lang, no amount of recency on text_synced_at saves it.
	overview := ttlSectionVerdict(
		SectionOverview, canon.EnrichmentTextSyncedAt, canon.Status,
		SectionTTLs[SectionOverview], now,
	)
	if missing := p.checkMissingLang(ctx, seriesID, lang); missing != "" {
		overview.Stale = true
		overview.Reason = missing
	}
	verdicts = append(verdicts, overview)

	// SectionCast: enrichment_cast_synced_at + missing_lang fallback.
	// Character names are localized per-language in person_credits_texts
	// (S-G); the missing_lang probe still drives cast re-fetch per language.
	cast := ttlSectionVerdict(
		SectionCast, canon.EnrichmentCastSyncedAt, canon.Status,
		SectionTTLs[SectionCast], now,
	)
	if missing := p.checkMissingLang(ctx, seriesID, lang); missing != "" {
		cast.Stale = true
		cast.Reason = missing
	}
	verdicts = append(verdicts, cast)

	// SectionRecommendations: enrichment_recs_synced_at + per-lang
	// coverage check. Story 566: A1's original "no per-rec lang check"
	// stance turned out wrong — recs get stamped in en-US on first visit,
	// then subsequent ru-RU visits see Fresh timestamp and never trigger
	// A3b to populate ru-RU series_texts rows. Coverage check catches that
	// gap and lets A5 EnsureFreshScope re-fire A3b until closed.
	// W15-3: removed the en-US exclusion here — RecommendationsCoverage is
	// parameterized on language, and en-US rec-sets first warmed under a
	// ru-RU visit are legitimately uncovered in en-US, so en-US views must
	// also enter the coverage path to re-fire A3b for their own language.
	recs := ttlSectionVerdict(
		SectionRecommendations, canon.EnrichmentRecsSyncedAt, canon.Status,
		SectionTTLs[SectionRecommendations], now,
	)
	if !recs.Stale && p.cfg.SeriesTextsCoverage != nil && !lang.IsZero() {
		covered, total, cerr := p.cfg.SeriesTextsCoverage.RecommendationsCoverage(ctx, seriesID, lang.Value())
		switch {
		case cerr != nil:
			// Fail-open: prefer to over-fire A3b (cheap, de-duped) than
			// strand user with en-US carousel. Radarr lesson: never let an
			// IO error look like Fresh.
			p.cfg.Logger.WarnContext(ctx, "freshener.probe.recs_coverage_error",
				slog.Int64("series_id", int64(seriesID)),
				slog.String("lang", lang.Value()),
				slog.String("error", cerr.Error()),
			)
			recs.Stale = true
			recs.Reason = "probe_error"
		case total > 0 && covered*100 < total*p.cfg.RecsCoverageMinPct:
			recs.Stale = true
			recs.Reason = "missing_recs_lang"
		}
		// total == 0 → intentional no-op: series has no recs to cover.
	}
	verdicts = append(verdicts, recs)

	// SectionMedia: enrichment_media_synced_at; lang-agnostic.
	verdicts = append(verdicts, ttlSectionVerdict(
		SectionMedia, canon.EnrichmentMediaSyncedAt, canon.Status,
		SectionTTLs[SectionMedia], now,
	))

	// SPARSE season verdicts.
	for _, n := range seasonNumbers {
		seasonSyncedAt, sErr := p.cfg.Seasons.GetEpisodesSyncedAt(ctx, seriesID, n)
		section := SeasonSection(n)
		switch {
		case sErr != nil && errors.Is(sErr, dataports.ErrNotFound):
			verdicts = append(verdicts, SectionVerdict{
				Section: section, Stale: true, Reason: "never",
			})
			continue
		case sErr != nil:
			p.cfg.Logger.WarnContext(ctx, "freshener.probe.season_read_error",
				slog.Int64("series_id", int64(seriesID)),
				slog.Int("season_number", n),
				slog.String("error", sErr.Error()),
			)
			verdicts = append(verdicts, SectionVerdict{
				Section: section, Stale: true, Reason: "probe_error",
			})
			continue
		}
		v := ttlSectionVerdict(section, seasonSyncedAt, canon.Status, SeasonTTL, now)

		// Story 548 — partial episode_texts coverage detection.
		// W15-4: no en-US exclusion here either (same false invariant as
		// checkMissingLang). en-US episode_texts are scan-seeded per
		// episode (sonarr_sync), so coverage is ~100% and this rarely
		// fires; when it does it's a genuine gap that re-localizes under
		// Season TTL + singleflight (tmdb-less → no_tmdb_id_skip no-op).
		// W16-7: coverage is now scoped to this season `n` (was series-wide)
		// — RefreshSeasonSlim writes one season, so a series-wide fraction
		// kept re-flagging a fully-localized season on every open.
		if p.cfg.EpisodeTextsCoverage != nil && !lang.IsZero() {
			if covered, total, cerr := p.cfg.EpisodeTextsCoverage.CoverageBySeriesSeason(ctx, seriesID, n, lang.Value()); cerr == nil &&
				total > 0 && covered*100 < total*p.cfg.EpisodeCoverageMinPct {
				v.Stale = true
				v.Reason = "missing_episodes_lang"
			}
		}
		verdicts = append(verdicts, v)
	}

	return verdicts, nil
}

// checkMissingLang returns "missing_lang" if the series_texts row for
// the requested lang is absent or only available as a fallback. Empty
// lang OR matched row → "" (caller keeps TTL verdict).
func (p *DBProbe) checkMissingLang(ctx context.Context, seriesID domain.SeriesID, lang values.LanguageTag) string {
	if lang.IsZero() {
		return ""
	}
	v := lang.Value()
	// W15-4: no en-US exclusion. The invariant "S-E1 guarantees en-US"
	// is violated live (~45% series_texts coverage), so an en-US view
	// whose base-lang row is missing (or only a fallback) must mark
	// overview/cast stale to re-localize. GetWithFallback + language
	// compare is correct for any lang; a tmdb-less series dispatches
	// no_tmdb_id_skip (no-op), a tmdb series re-fetches under TTL.
	row, terr := p.cfg.SeriesTexts.GetWithFallback(ctx, seriesID, v)
	if terr != nil && errors.Is(terr, dataports.ErrNotFound) {
		return "missing_lang"
	}
	if terr == nil && !strings.EqualFold(row.Language, v) {
		return "missing_lang"
	}
	return ""
}

// ttlSectionVerdict bundles TTL policy + section identity into a verdict.
// Always returns SectionVerdict (never error) so callers can build the
// dense array without branching.
func ttlSectionVerdict(
	section Section,
	syncedAt *time.Time,
	status *string,
	policy TTLPolicy,
	now time.Time,
) SectionVerdict {
	stale, reason := ttlVerdict(syncedAt, status, policy, now)
	v := SectionVerdict{
		Section:  section,
		Stale:    stale,
		Reason:   reason,
		SyncedAt: syncedAt,
	}
	if syncedAt != nil {
		v.Age = now.Sub(*syncedAt)
	}
	return v
}

// allStaleVerdicts is the fail-open helper. Builds DENSE 5 + SPARSE
// verdicts all marked stale with the same reason. Used on canon-read
// failures (probe_error / stub).
func (p *DBProbe) allStaleVerdicts(
	_ domain.SeriesID,
	_ values.LanguageTag,
	seasonNumbers []int,
	reason string,
	syncedAt *time.Time,
	age time.Duration,
) []SectionVerdict {
	verdicts := make([]SectionVerdict, 0, len(FixedSections)+len(seasonNumbers))
	for _, s := range FixedSections {
		verdicts = append(verdicts, SectionVerdict{
			Section: s, Stale: true, Reason: reason, SyncedAt: syncedAt, Age: age,
		})
	}
	for _, n := range seasonNumbers {
		verdicts = append(verdicts, SectionVerdict{
			Section: SeasonSection(n), Stale: true, Reason: reason, SyncedAt: syncedAt, Age: age,
		})
	}
	return verdicts
}
