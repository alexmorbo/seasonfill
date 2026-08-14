package app

import (
	"context"
	"strings"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// castKind is the person_credits.kind literal for cast rows (crew rows carry
// "crew"). Movie /person/{id}/movie_credits maps cast entries to this value —
// see person_credits_movie_order_test.go.
const castKind = "cast"

// CastRowsReader lists person_credits rows for a media by tmdb id with the
// character_name localized (requested → en-US → base). Impl:
// *enrichpersistence.PersonCreditsRepository.
type CastRowsReader interface {
	ListByMediaWithTextFallback(ctx context.Context, mediaType string, tmdbMediaID int, lang string) ([]enrichpersistence.PersonCredit, error)
}

// PeopleNameReader resolves people rows with the display name localized
// (requested → en-US → original_name). Impl: *enrichpersistence.PeopleRepository.
type PeopleNameReader interface {
	ListByIDsWithNameFallback(ctx context.Context, ids []int64, lang string) ([]people.Person, error)
}

// TitleLangReader resolves the BCP-47 language the localized movie title
// resolves to (requested → en-US → any), or "" when the movie has no titled
// localized row. Impl: *enrichpersistence.MovieI18nReadRepository. nil-OK.
type TitleLangReader interface {
	TitleLanguage(ctx context.Context, movieID domain.MovieID, lang string) (string, error)
}

// MovieCastPage is the assembled cast aggregate (pre-media-resolution; the REST
// layer resolves profile paths + maps to the wire DTO).
type MovieCastPage struct {
	TMDBID         domain.TMDBID
	Lang           string
	ServedLanguage string
	Cast           []MovieCastEntry
	Degraded       []string
}

// MovieCastEntry is one cast row: the resolved person + this movie's credit
// fields.
type MovieCastEntry struct {
	Person        people.Person
	CharacterName *string
	CreditOrder   *int
}

// CastUseCase assembles the movie cast list from local read ports. All data is
// local (person_credits + people + movie_i18n) — no live TMDB.
type CastUseCase struct {
	canon     CanonReader
	castRows  CastRowsReader
	people    PeopleNameReader
	titleLang TitleLangReader // nil-OK
}

// NewCastUseCase constructs the cast usecase. titleLang nil-OK (served_language
// then "" and no missing_lang marker).
func NewCastUseCase(canon CanonReader, castRows CastRowsReader, ppl PeopleNameReader, titleLang TitleLangReader) *CastUseCase {
	return &CastUseCase{canon: canon, castRows: castRows, people: ppl, titleLang: titleLang}
}

// Get assembles the cast page for a tmdb id. ports.ErrNotFound bubbles when no
// canon row exists (→ 404). Ordering is NOT applied here — the REST layer sorts
// the resolved DTO (default credit_order ASC).
func (uc *CastUseCase) Get(ctx context.Context, tmdbID domain.TMDBID, lang string) (*MovieCastPage, error) {
	canon, err := uc.canon.GetByTMDBID(ctx, tmdbID)
	if err != nil {
		return nil, err // ports.ErrNotFound bubbles
	}

	rows, err := uc.castRows.ListByMediaWithTextFallback(ctx, "movie", int(tmdbID), lang)
	if err != nil {
		return nil, err
	}

	// Collect cast rows + deduped person ids (rows arrive person_id ASC).
	castRows := make([]enrichpersistence.PersonCredit, 0, len(rows))
	personIDs := make([]int64, 0, len(rows))
	seen := make(map[int64]bool, len(rows))
	for _, r := range rows {
		if r.Kind != castKind {
			continue
		}
		castRows = append(castRows, r)
		if !seen[r.PersonID] {
			seen[r.PersonID] = true
			personIDs = append(personIDs, r.PersonID)
		}
	}

	personByID := make(map[int64]people.Person, len(personIDs))
	if len(personIDs) > 0 {
		persons, perr := uc.people.ListByIDsWithNameFallback(ctx, personIDs, lang)
		if perr != nil {
			return nil, perr
		}
		for _, p := range persons {
			personByID[p.ID] = p
		}
	}

	entries := make([]MovieCastEntry, 0, len(castRows))
	for _, r := range castRows {
		p, ok := personByID[r.PersonID]
		if !ok {
			// Person stub not yet materialised — carry the id so the row is
			// still addressable; name resolves on the next enrichment pass.
			p = people.Person{ID: r.PersonID}
		}
		entries = append(entries, MovieCastEntry{
			Person:        p,
			CharacterName: r.CharacterName,
			CreditOrder:   r.CreditOrder,
		})
	}

	served := ""
	if uc.titleLang != nil {
		if s, terr := uc.titleLang.TitleLanguage(ctx, canon.ID, lang); terr == nil {
			served = s // fail-open: a lookup error leaves served ""
		}
	}

	return &MovieCastPage{
		TMDBID:         tmdbID,
		Lang:           lang,
		ServedLanguage: served,
		Cast:           entries,
		Degraded:       appendMissingLang(nil, served, lang),
	}, nil
}

// appendMissingLang appends "missing_lang" when a real fallback-language title
// was served. Duplicated from seriesdetail.AppendMissingLang to keep moviedetail
// free of a cross-vertical import (established codebase convention).
func appendMissingLang(degraded []string, served, requested string) []string {
	if served == "" || served == resolveLang(requested) {
		return degraded
	}
	return append(degraded, "missing_lang")
}

// resolveLang normalises a requested tag: empty/absurd → en-US. Mirrors
// seriesdetail.resolveLang.
func resolveLang(lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" || len(lang) > 35 {
		return "en-US"
	}
	return lang
}
