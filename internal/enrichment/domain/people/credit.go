package people

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesCreditKind is the discriminator on series_people.kind.
// Cast covers the regular cast roles (character credits), Crew
// covers per-series creative roles (creators, EPs, etc). TMDB's
// aggregate_credits payload emits both sides under one merged list;
// the C-2 worker splits by the TMDB object key (`cast` vs `crew`)
// on upsert.
type SeriesCreditKind string

const (
	SeriesCreditCast SeriesCreditKind = "cast"
	SeriesCreditCrew SeriesCreditKind = "crew"
)

// IsValid reports whether k is one of the two known kinds.
func (k SeriesCreditKind) IsValid() bool {
	return k == SeriesCreditCast || k == SeriesCreditCrew
}

// EpisodeCreditKind is the discriminator on the episode-level credit
// row. The TMDB episode-level shape distinguishes guest stars (one-off
// appearances) from per-episode crew (episode director, episode
// writer); regular cast members do NOT appear at the episode level —
// they appear once at the series level (person_credits media_type='tv'
// with an episode_count instead).
//
// D-7 (468b): persisted as person_credits rows with
// media_type='tv_episode'; the legacy `episode_people` table was
// dropped in D-3 (story 464c).
type EpisodeCreditKind string

const (
	EpisodeCreditGuestStar EpisodeCreditKind = "guest_star"
	EpisodeCreditCrew      EpisodeCreditKind = "crew"
)

// IsValid reports whether k is one of the two known kinds.
func (k EpisodeCreditKind) IsValid() bool {
	return k == EpisodeCreditGuestStar || k == EpisodeCreditCrew
}

// SeriesCredit is one row of series_people. Natural key
// (series_id, tmdb_credit_id) — idempotent re-ingest of
// aggregate_credits never duplicates. CreditOrder is the TMDB
// billing order; EpisodeCount is filled from
// aggregate_credits[*].total_episode_count for the H-1 cast page
// Main / Recurring / Guest derivation (design-handoff Q3).
type SeriesCredit struct {
	ID            int64
	SeriesID      domain.SeriesID
	PersonID      int64
	Kind          SeriesCreditKind
	TMDBCreditID  string
	CharacterName *string
	Department    *string
	Job           *string
	CreditOrder   *int
	EpisodeCount  *int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// EpisodeCredit is one canonical row of episode-level credits.
//
// D-7 (468b): persistence moved to person_credits with
// media_type='tv_episode' + tmdb_media_id=<episode tmdb_id>. The
// legacy `episode_people` table (PK on episode_id) was dropped in D-3
// (story 464c). The domain type is kept because tmdb.MapSeasonToCredits
// still emits it as its return shape; the worker projects to
// people.PersonCredit before BatchUpsert.
//
// Episode-level credits stay logically separate from series-level
// credits because (a) the read path for the episode card needs
// per-episode-only rows (guest stars) without filtering, and (b) the
// write paths differ — series credits come from aggregate_credits in
// one shot, episode credits come from per-season
// `GET /tv/{id}/season/{n}` enrichment.
type EpisodeCredit struct {
	ID            int64
	EpisodeID     domain.EpisodeID
	PersonID      int64
	Kind          EpisodeCreditKind
	TMDBCreditID  string
	CharacterName *string
	Department    *string
	Job           *string
	CreditOrder   *int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// PersonCredit is the cross-reference filmography row (PRD §5.3 row
// "person_credits", schema shipped by story 206). Natural key
// (person_id, tmdb_credit_id) — idempotent re-ingest of TMDB's
// /person/{id}/tv_credits + /movie_credits. The row references TMDB
// media ids directly (TMDBMediaID + MediaType ∈ {tv, movie}); no
// `series` stub is created for non-library TV titles or for movies.
// The Kind reuses SeriesCreditKind ("cast" / "crew") because the
// TMDB credit shape is unified across media types.
//
// Domain type sits alongside the database model (PersonCreditModel)
// — the mapper layer emits this canonical shape; the C-3 worker /
// repository handles the model conversion. *string / *int fields
// follow the nil-vs-zero merge-policy convention used elsewhere in
// the people domain.
type PersonCredit struct {
	ID            int64
	PersonID      int64
	MediaType     string
	TMDBMediaID   int64
	TMDBCreditID  string
	Kind          SeriesCreditKind
	Title         string
	OriginalTitle *string
	CharacterName *string
	Department    *string
	Job           *string
	EpisodeCount  *int
	ReleaseDate   *time.Time
	PosterAsset   *string
	TMDBRating    *float64
	TMDBVotes     *int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// PersonCreditText is one per-language cast character-name row
// (person_credits_texts, S-G). Keyed by the person_credits surrogate id
// + language. CharacterName is nil-able; a nil/empty value must NOT
// overwrite an existing stored value (COALESCE-protected upsert).
type PersonCreditText struct {
	PersonCreditID int64
	Language       string
	CharacterName  *string
}

// PersonText is one per-language person DISPLAY name row (people_texts,
// Story 1083). Keyed by the people surrogate id + language. Name is nil-able;
// a nil/empty value must NOT overwrite an existing stored value
// (COALESCE-protected upsert), matching PersonCreditText semantics.
type PersonText struct {
	PersonID int64
	Language string
	Name     *string
}
