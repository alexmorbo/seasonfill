package series

import "time"

// Hydration tracks the gidration depth of a Canon (or future Person)
// row. `HydrationStub` rows are placeholder shells (title + key ids
// only, created when a Recommendation references a series that has no
// row yet, or when a Sonarr orphan needs a canon target); enrichment
// workers (Pillar C / D / E) lift them to `HydrationFull`. Empty value
// is treated as `HydrationStub` by callers — defensive default for
// rows inserted by older code paths that pre-date this enum.
type Hydration string

const (
	HydrationStub Hydration = "stub"
	HydrationFull Hydration = "full"
)

// IsValid reports whether h is one of the two known levels. Empty
// strings are explicitly NOT valid — callers MUST normalise to
// HydrationStub before persisting.
func (h Hydration) IsValid() bool {
	return h == HydrationStub || h == HydrationFull
}

// Canon is the canonical, instance-independent local entity. One row
// per real-world series (natural key: tmdb_id when present, otherwise
// Sonarr-orphan with NULL tmdb_id). Stays distinct from
// `series.Series`, which is the Sonarr in-memory projection used by
// the legacy scan/evaluate path; the two coexist until the Pillar-B
// rebuild migrates those callers over.
//
// Every *string / *int field maps to a nullable column. Pointers (not
// zero values) make `nil = SQL NULL, zero = explicit 0/""` unambiguous
// on the merge-policy boundary (§5.4) — a TMDB worker writing
// `Popularity=ptr(0.0)` MUST be distinguishable from a worker that
// left popularity unset.
type Canon struct {
	ID               int64
	TMDBID           *int
	TVDBID           *int
	IMDBID           *string
	Hydration        Hydration
	Title            string
	OriginalTitle    *string
	Status           *string
	FirstAirDate     *time.Time
	LastAirDate      *time.Time
	NextAirDate      *time.Time
	Year             *int
	RuntimeMinutes   *int
	Homepage         *string
	OriginalLanguage *string
	OriginCountry    *string
	OriginCountries  []string
	Popularity       *float64
	InProduction     bool
	// Network REMOVED in E-1 (000033). Use SeriesNetworksRepository
	// to read/write network membership.
	PosterAsset   *string
	BackdropAsset *string
	TMDBRating    *float64
	TMDBVotes     *int
	IMDBRating    *float64
	IMDBVotes     *int
	OMDBRated     *string
	OMDBAwards    *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// CanonSeason is one row of `seasons`. SeriesID is a foreign reference
// to Canon.ID — application-side cascade as elsewhere in the schema.
type CanonSeason struct {
	ID           int64
	SeriesID     int64
	SeasonNumber int
	TMDBSeasonID *int
	Name         *string
	Overview     *string
	AirDate      *time.Time
	EpisodeCount *int
	PosterAsset  *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CanonEpisode is one row of `episodes`. Carries canonical TMDB+Sonarr
// merged metadata; the per-instance file state lives separately in
// `EpisodeState` (§5.11 grain). EpisodeNumber is the Sonarr/TVDB
// display order; TMDBEpisodeNumber holds the TMDB number when it
// diverges (anime out of scope for v1 — `AbsoluteNumber` stays
// reserved).
type CanonEpisode struct {
	ID                int64
	SeriesID          int64
	SeasonID          *int64
	SeasonNumber      int
	EpisodeNumber     int
	TMDBEpisodeNumber *int
	TMDBEpisodeID     *int
	SonarrEpisodeID   *int
	AbsoluteNumber    *int
	AirDate           *time.Time
	RuntimeMinutes    *int
	FinaleType        *string
	StillAsset        *string
	TMDBRating        *float64
	TMDBVotes         *int
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// EpisodeState is the per-instance file state for one canonical
// episode. Composite key (InstanceName, EpisodeID); rows are written
// by the Sonarr sync path only (§5.4).
type EpisodeState struct {
	InstanceName  string
	EpisodeID     int64
	Monitored     bool
	HasFile       bool
	EpisodeFileID *int
	Quality       *string
	SizeBytes     *int64
	VideoCodec    *string
	AudioCodec    *string
	AudioChannels *string
	ReleaseGroup  *string
	UpdatedAt     time.Time
}

// SeriesText is one localised text row of `series_texts`. Language is
// a BCP-47 tag (v1: `ru-RU` / `en-US`). Helpers read the row with
// (series_id, language) PK; the §5.6 fallback helper returns the
// requested language when present, else en-US, else first available.
type SeriesText struct {
	SeriesID  int64
	Language  string
	Title     *string
	Overview  *string
	Tagline   *string
	UpdatedAt time.Time
}

// EpisodeText is one localised text row of `episode_texts`. Same
// (entity_id, language) PK shape as SeriesText; per §5.3 it carries
// only title + overview — episodes have no tagline.
type EpisodeText struct {
	EpisodeID int64
	Language  string
	Title     *string
	Overview  *string
	UpdatedAt time.Time
}
