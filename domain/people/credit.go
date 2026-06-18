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

// EpisodeCreditKind is the discriminator on episode_people.kind.
// The TMDB episode-level shape distinguishes guest stars (one-off
// appearances) from per-episode crew (episode director, episode
// writer); regular cast members do NOT appear in episode_people —
// they live in series_people with an episode_count instead.
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

// EpisodeCredit is one row of episode_people. Natural key
// (episode_id, tmdb_credit_id) — idempotent. Episode-level credits
// are kept separate from series-level credits because (a) the read
// path for the episode card needs per-episode-only rows (guest
// stars) without filtering, and (b) the write paths differ — series
// credits come from aggregate_credits in one shot, episode credits
// come from per-season `GET /tv/{id}/season/{n}` enrichment.
type EpisodeCredit struct {
	ID            int64
	EpisodeID     int64
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
