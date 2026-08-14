package app

import (
	"context"
	"errors"
	"fmt"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieOverviewPage is the assembled localized-text slice returned by the movie
// overview sub-endpoint. Title falls back to canon; overview/tagline stay nil
// when no localized (or fallback) value exists. Degraded carries "missing_lang"
// when a fallback-language title was served (F-Ф2-04).
type MovieOverviewPage struct {
	TMDBID         domain.TMDBID
	Lang           string
	Title          string
	Overview       *string
	Tagline        *string
	ServedLanguage string
	Degraded       []string
}

// OverviewUseCase assembles the localized-text overview slice for a movie from
// local read ports — canon (for 404 + title fallback) + the Ф0.2 per-field
// movie_i18n ladder + the title-language signal. No live TMDB.
type OverviewUseCase struct {
	canon     CanonReader
	i18n      I18nReader
	titleLang TitleLangReader // nil-OK: served_language stays "" and no missing_lang
}

// NewOverviewUseCase constructs the overview usecase over its read ports. In the
// live wiring canon = *MovieRepository and i18n = titleLang =
// *MovieI18nReadRepository (one repo satisfies both the Get ladder and the
// TitleLanguage signal). titleLang nil-OK.
func NewOverviewUseCase(canon CanonReader, i18n I18nReader, titleLang TitleLangReader) *OverviewUseCase {
	return &OverviewUseCase{canon: canon, i18n: i18n, titleLang: titleLang}
}

// Get assembles the overview page for a tmdb id. ports.ErrNotFound bubbles when
// no canon row exists (→ 404). The localized title/overview/tagline are read via
// the Ф0.2 per-field ladder (I18nReader.Get); a missing localized row (or empty
// fields) falls back to the canon title with nil overview/tagline — never an
// error. served_language + missing_lang mirror the Ф2.1 cast signal.
func (uc *OverviewUseCase) Get(ctx context.Context, tmdbID domain.TMDBID, lang string) (*MovieOverviewPage, error) {
	canon, err := uc.canon.GetByTMDBID(ctx, tmdbID)
	if err != nil {
		return nil, err // ports.ErrNotFound bubbles
	}

	page := &MovieOverviewPage{TMDBID: tmdbID, Lang: lang, Title: canon.Title}

	row, ierr := uc.i18n.Get(ctx, canon.ID, lang)
	switch {
	case ierr == nil:
		if row.Title != nil && *row.Title != "" {
			page.Title = *row.Title
		}
		if row.Overview != nil && *row.Overview != "" {
			page.Overview = row.Overview
		}
		if row.Tagline != nil && *row.Tagline != "" {
			page.Tagline = row.Tagline
		}
	case errors.Is(ierr, ports.ErrNotFound):
		// No poster-bearing localized row in any language — keep canon title.
	default:
		return nil, fmt.Errorf("movie overview: i18n: %w", ierr)
	}

	served := ""
	if uc.titleLang != nil {
		if s, terr := uc.titleLang.TitleLanguage(ctx, canon.ID, lang); terr == nil {
			served = s // fail-open: a lookup error leaves served ""
		}
	}
	page.ServedLanguage = served
	page.Degraded = appendMissingLang(nil, served, lang)
	return page, nil
}
