package adapters

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	mdengdomain "github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
	mvapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// movieTitleGapRecheck re-declares moviedetail/app's private constant (freshener.go).
// S-HEAL-FIX: the on-view heal recency gate now keys on the always-advancing
// movies.enrichment_text_synced_at attempt clock (see HasLocalizedTextGap), and the
// window is 6h — kept in sync by contract with the background picker
// (movie_refresh_query.go movieI18nHealAttemptWindow) and moviedetail/app/freshener.go.
const movieTitleGapRecheck = 6 * time.Hour

// movieCanonReader reads a movie canon by internal id. *enrichpersistence.MovieRepository.Get satisfies it.
type movieCanonReader interface {
	Get(ctx context.Context, id domain.MovieID) (movie.Canon, error)
}

// movieTextGapReader answers the U-1b localized-text gap (freshener isStale second arm).
// *enrichpersistence.MovieI18nReadRepository.HasLocalizedTextGap satisfies it.
type movieTextGapReader interface {
	HasLocalizedTextGap(ctx context.Context, movieID domain.MovieID, lang string, recheckBefore time.Time) (bool, error)
}

// movieForceRefresher drives a whole-movie HandleForced. *MovieForceRefresherHolder satisfies it.
type movieForceRefresher interface {
	HandleForced(ctx context.Context, movieID int64) error
}

// movieTextPlugin implements the engine SectionPlugin for (movie, text). It rides
// the STALENESS arm (movie text freshness is boolean: MovieProbe section stamps ∨
// U-1b localized-text gap) and NO-OPs Coverage. Refresh delegates to the SAME
// whole-movie HandleForced the old MovieFreshener drove — so REPLACE, not double-fetch.
type movieTextPlugin struct {
	canon    movieCanonReader
	gap      movieTextGapReader // nil-OK → U-1b arm skipped (section-stamp only)
	refresh  movieForceRefresher
	baseLang string
}

// NewMovieTextPlugin constructs the plugin. baseLang is locale.Default(); the U-1b
// gap arm only fires for a non-base, non-empty lang (mirror of freshener.isStale).
func NewMovieTextPlugin(canon movieCanonReader, gap movieTextGapReader, refresh movieForceRefresher, baseLang string) mdengSectionPlugin {
	return &movieTextPlugin{canon: canon, gap: gap, refresh: refresh, baseLang: baseLang}
}

// Section is the canonical text section.
func (p *movieTextPlugin) Section() mdengdomain.Section { return mdengdomain.SectionText }

// Coverage NO-OP: movie text is boolean-shaped (Staleness arm), so total==0 tells
// the engine to defer to Staleness.
func (p *movieTextPlugin) Coverage(context.Context, mdengdomain.MediaID, string) (int, int, error) {
	return 0, 0, nil
}

// Staleness reproduces MovieFreshener.isStale (freshener.go:207-231) by delegating
// to the EXPORTED MovieProbe/AnyStale + HasLocalizedTextGap — no SQL copied. Read
// errors return (verdict, err); the engine's assess() fails CLOSED on a Staleness
// error (treats as not-stale), so a flaky read never forces a sync HandleForced.
func (p *movieTextPlugin) Staleness(ctx context.Context, id mdengdomain.MediaID, lang string, now time.Time) (mdengdomain.SectionVerdict, error) {
	canon, err := p.canon.Get(ctx, domain.MovieID(id.InternalID()))
	if err != nil {
		return mdengdomain.SectionVerdict{Section: mdengdomain.SectionText}, err
	}
	if mvapp.AnyStale(mvapp.MovieProbe(canon, now)) {
		return mdengdomain.SectionVerdict{Section: mdengdomain.SectionText, Stale: true, Reason: "section"}, nil
	}
	if p.gap == nil || lang == "" || lang == p.baseLang {
		return mdengdomain.SectionVerdict{Section: mdengdomain.SectionText, Stale: false, Reason: "fresh"}, nil
	}
	gap, gerr := p.gap.HasLocalizedTextGap(ctx, canon.ID, lang, now.Add(-movieTitleGapRecheck))
	if gerr != nil {
		return mdengdomain.SectionVerdict{Section: mdengdomain.SectionText}, gerr
	}
	reason := "fresh"
	if gap {
		reason = "gap"
	}
	return mdengdomain.SectionVerdict{Section: mdengdomain.SectionText, Stale: gap, Reason: reason}, nil
}

// Refresh drives the whole-movie HandleForced (the SAME COALESCE-safe pass the old
// MovieFreshener called). Idempotent; the engine coalesces per movie+lang.
func (p *movieTextPlugin) Refresh(ctx context.Context, id mdengdomain.MediaID, _ string) error {
	return p.refresh.HandleForced(ctx, id.InternalID())
}
