package movie

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Hydration mirrors series.Hydration semantics (stub|full) but is defined
// locally to avoid catalog cross-domain coupling — movies and series are
// distinct verticals that happen to share the same enrichment ladder.
type Hydration string

const (
	HydrationStub Hydration = "stub"
	HydrationFull Hydration = "full"
)

func (h Hydration) IsValid() bool { return h == HydrationStub || h == HydrationFull }

// Canon is the persistence-neutral movie canon row (Ф6-R-3, ADR-0018 §6b).
// One row per real movie; tmdb_id has a partial-unique index where not NULL
// so Radarr orphans without a TMDB match still fit.
type Canon struct {
	ID                     domain.MovieID
	TMDBID                 *domain.TMDBID
	IMDBID                 *domain.IMDBID
	Hydration              Hydration
	Title                  string
	OriginalTitle          *string
	Status                 *string
	ReleaseDate            *time.Time
	DigitalReleaseDate     *time.Time
	PhysicalReleaseDate    *time.Time
	Year                   *int
	RuntimeMinutes         *int
	Homepage               *string
	OriginalLanguage       *string
	OriginCountries        []string
	CollectionID           *int
	Popularity             *float64
	Budget                 *int64
	Revenue                *int64
	PosterAsset            *string
	BackdropAsset          *string
	TMDBRating             *float64
	TMDBVotes              *int
	IMDBRating             *float64
	IMDBVotes              *int
	OMDBRated              *string
	OMDBAwards             *string
	EnrichmentTMDBSyncedAt *time.Time
	EnrichmentOMDBSyncedAt *time.Time
	// TMDBChangedAt (Ф6-R-4a) — write-once /movie/changes clock. Written ONLY
	// by the movie changes-marker; never by Upsert (absent from
	// movieUpsertAssignments).
	TMDBChangedAt *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
