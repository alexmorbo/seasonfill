// Package taxonomy carries the canonical taxonomy-domain value types
// (PRD v4 §5.3 + §8.1). Network and ProductionCompany are simple
// dictionaries with entity-level names (brand names are not
// meaningfully translated); Genre and Keyword carry localised names
// in a sibling i18n table, read via the §5.6 fallback helper
// (i18n_texts.go::pickLanguageFallback, introduced in story 203).
//
// All four types are deliberately POD shapes — no behaviour, no
// invariants beyond "id != 0 once persisted". Business rules
// (Sonarr-genre fallback resolution, merge policy) live in the
// application layer; the domain types stay value-shaped.
package taxonomy

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Network is one row of `networks` (PRD §5.3 row "networks"). Name
// stays on the entity — brand names like "Netflix" / "BBC One" are
// not meaningfully translated. LogoAsset is a media_assets.hash
// reference; OriginCountry is an ISO 3166-1 alpha-2 string from TMDB.
type Network struct {
	ID            int64
	TMDBID        *domain.TMDBID
	Name          string
	LogoAsset     *string
	OriginCountry *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ProductionCompany is one row of `production_companies` (PRD §5.3
// row "production_companies"). Same shape as Network — brand names
// stay on the entity.
type ProductionCompany struct {
	ID            int64
	TMDBID        *domain.TMDBID
	Name          string
	LogoAsset     *string
	OriginCountry *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Genre is the canonical, instance-independent genre row (PRD §5.3
// row "genres"). The localised name lives in `genres_i18n`; Name +
// Language are projection fields populated by GenresRepository.Get
// via the shared §5.6 fallback helper. Writes go directly through
// GenresI18nRepository.Upsert; the two projection fields exist on
// Genre purely so the composer / read path can return a single
// ready-to-serialise object.
type Genre struct {
	ID     int64
	TMDBID *domain.TMDBID
	// Name is the resolved localised name returned by
	// GenresRepository.Get — read-only on Genre. May be empty if no
	// row exists in genres_i18n for any language (rare — TMDB always
	// emits at least an en-US name on series enrichment).
	Name string
	// Language is the language of the resolved Name row; empty when
	// Name is empty. Composer surfaces this so UI can render an
	// "EN"-tag when a non-requested language served the §5.6 fallback.
	Language  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Keyword is the canonical, instance-independent keyword row (PRD
// §5.3 row "keywords"). Same projection shape as Genre. v1 only has
// en-US rows in `keywords_i18n` because TMDB does not localise
// keywords — `Get(id, "ru-RU")` falls back to en-US via the §5.6
// helper. The unified i18n form is forward-compat: a future RU /
// de / fr keyword source lands as new rows, not a migration.
type Keyword struct {
	ID        int64
	TMDBID    *domain.TMDBID
	Name      string
	Language  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GenreI18n is one localised name row of `genres_i18n` (PRD §5.3 row
// "genres_i18n"). Same (entity_id, language) PK shape as SeriesText
// / PersonBiography; read via the shared §5.6 fallback helper.
type GenreI18n struct {
	GenreID   int64
	Language  string
	Name      string
	UpdatedAt time.Time
}

// KeywordI18n is one localised name row of `keywords_i18n`. v1 only
// has en-US rows; the form is unified with GenreI18n / SeriesText so
// adding RU is a write-path concern, not a schema change.
type KeywordI18n struct {
	KeywordID int64
	Language  string
	Name      string
	UpdatedAt time.Time
}
