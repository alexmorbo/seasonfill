package series

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

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
	ID               domain.SeriesID
	TMDBID           *domain.TMDBID
	TVDBID           *domain.TVDBID
	IMDBID           *domain.IMDBID
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
	// EnrichmentTMDBSyncedAt is set by the TMDB series worker on a
	// successful /tv/{id} fetch (PRD §D-3). NULL = never TMDB-enriched
	// — replaces the legacy sync_log(tmdb_series, outcome='ok') TTL
	// gate the worker used to read before D-3.
	EnrichmentTMDBSyncedAt *time.Time
	// EnrichmentOMDBSyncedAt is set by the OMDb worker on a successful
	// /?i={imdb_id}&plot=short fetch. NULL = never OMDb-enriched —
	// replaces the legacy sync_log(omdb, outcome='ok') TTL gate.
	EnrichmentOMDBSyncedAt *time.Time
	// EnrichmentTextSyncedAt is set by Worker.RefreshSeriesText (A2) on
	// successful series_texts UPSERT. NULL = never section-refreshed —
	// either pre-migration (backfilled from EnrichmentTMDBSyncedAt) or
	// the row is a stub. PLAN §6.1.
	EnrichmentTextSyncedAt *time.Time
	// EnrichmentCastSyncedAt is set by Worker.RefreshCast (A2).
	EnrichmentCastSyncedAt *time.Time
	// EnrichmentRecsSyncedAt is set by Worker.RefreshRecommendations (A3b).
	EnrichmentRecsSyncedAt *time.Time
	// EnrichmentMediaSyncedAt is set by Worker.RefreshMediaAssets (A4).
	EnrichmentMediaSyncedAt *time.Time
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// CanonSeason is one row of `seasons`. SeriesID is a foreign reference
// to Canon.ID — application-side cascade as elsewhere in the schema.
type CanonSeason struct {
	ID           int64
	SeriesID     domain.SeriesID
	SeasonNumber int
	TMDBSeasonID *int
	Name         *string
	Overview     *string
	AirDate      *time.Time
	EpisodeCount *int
	PosterAsset  *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	// EpisodesSyncedAt is the E-1-A1 per-season freshness stamp. Set by
	// Worker.RefreshSeasonSlim (A3a) on successful episode list UPSERT.
	// NULL = never section-refreshed.
	EpisodesSyncedAt *time.Time
}

// CanonEpisode is one row of `episodes`. Carries canonical TMDB+Sonarr
// merged metadata; the per-instance file state lives separately in
// `EpisodeState` (§5.11 grain). EpisodeNumber is the Sonarr/TVDB
// display order; TMDBEpisodeNumber holds the TMDB number when it
// diverges (anime out of scope for v1 — `AbsoluteNumber` stays
// reserved).
type CanonEpisode struct {
	ID                int64
	SeriesID          domain.SeriesID
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
	InstanceName  domain.InstanceName
	EpisodeID     domain.EpisodeID
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
//
// EnrichedAt is the TMDB-worker freshness stamp (D-4 lazy-language
// enrichment plan, PRD §D-4). NULL = never enriched by TMDB; non-TMDB
// write paths (Sonarr stub) leave it nil and the Upsert COALESCEs to
// preserve any previously-set value.
type SeriesText struct {
	SeriesID   domain.SeriesID
	Language   string
	Title      *string
	Overview   *string
	Tagline    *string
	EnrichedAt *time.Time
	UpdatedAt  time.Time
}

// EpisodeText is one localised text row of `episode_texts`. Same
// (entity_id, language) PK shape as SeriesText; per §5.3 it carries
// only title + overview — episodes have no tagline.
//
// EnrichedAt — same semantics as SeriesText.EnrichedAt.
type EpisodeText struct {
	EpisodeID  domain.EpisodeID
	Language   string
	Title      *string
	Overview   *string
	EnrichedAt *time.Time
	UpdatedAt  time.Time
}

// SeasonText is one localised text row of `season_texts`. Composite key
// (series_id, season_number, language); language is a BCP-47 tag
// (v1: `ru-RU` / `en-US`). The §5.6 fallback (requested language, else
// en-US) is applied by the batch read; the final canon `seasons.name`
// tier is layered by the E-1 B3c SeasonsComposer, not here.
//
// EnrichedAt is the TMDB-worker freshness stamp (E-1 B3b). NULL = never
// enriched by TMDB; non-TMDB write paths leave it nil and the Upsert
// COALESCEs to preserve any previously-set value.
type SeasonText struct {
	SeriesID     domain.SeriesID
	SeasonNumber int
	Language     string
	Name         *string
	Overview     *string
	EnrichedAt   *time.Time
	UpdatedAt    time.Time
}

// SeriesMediaText is one per-language poster/backdrop row of
// series_media_texts. Same (series_id, language) PK shape as SeriesText.
// Variant A (Story 584): TMDB returns a different best poster/backdrop
// per language; this table stores the per-lang RAW TMDB image paths so
// each read path can mint its own size-specific media hash — exactly the
// way canon series.poster_asset is read today, but language-aware.
//
// PosterAsset / BackdropAsset are the raw TMDB paths ("/abc.jpg") and are
// the READ source of truth. PosterHash / BackdropHash are the eager
// default-size sha256 the RefreshSeriesText worker mints via
// MediaResolver.Resolve (poster=w342, backdrop=w1280) — their real value
// is the EnsurePending side effect that pre-warms the per-lang asset into
// the media pipeline; reads re-derive from the raw path and do not depend
// on these columns.
//
// EnrichedAt is the TMDB-worker freshness stamp (NULL = never enriched by
// TMDB); non-TMDB write paths leave every field nil and the Upsert
// COALESCEs to preserve any previously-set value.
type SeriesMediaText struct {
	SeriesID      domain.SeriesID
	Language      string
	PosterAsset   *string
	PosterHash    *string
	BackdropAsset *string
	BackdropHash  *string
	EnrichedAt    *time.Time
	UpdatedAt     time.Time
}

// SeasonMediaText is one per-language season poster/backdrop row of
// season_media_texts (S-C2). Same 3-column composite PK shape as SeasonText
// (series_id, season_number, language) and the same read/write contract as
// SeriesMediaText: PosterAsset is the raw TMDB path (READ source of truth),
// PosterHash the eager pre-warm sha256 the RefreshSeasonSlim worker mints via
// MediaResolver (side-effect: EnsurePending). BackdropAsset/BackdropHash mirror
// series_media_texts for symmetry but stay nil — TMDB season images carry
// posters only. EnrichedAt is the TMDB-worker freshness stamp; non-TMDB paths
// leave every field nil and the Upsert COALESCEs to preserve prior values.
type SeasonMediaText struct {
	SeriesID      domain.SeriesID
	SeasonNumber  int
	Language      string
	PosterAsset   *string
	PosterHash    *string
	BackdropAsset *string
	BackdropHash  *string
	EnrichedAt    *time.Time
	UpdatedAt     time.Time
}

// SeasonEpisodeAggregate is the per-season rollup the E-1 B3c SeasonsComposer
// reads to fill air_date_end + episode_count without an N+1 walk. There is no
// seasons.air_date_end column (seasons carries a single AirDate), so LastAirDate
// is computed as MAX(episodes.air_date) for the season via a GROUP BY aggregate
// (EpisodesRepository.AggregateBySeries). FirstAirDate / LastAirDate are nil when
// the season has no episode rows with a non-NULL air_date (cold season shell).
type SeasonEpisodeAggregate struct {
	SeasonNumber int
	EpisodeCount int
	FirstAirDate *time.Time
	LastAirDate  *time.Time
}
