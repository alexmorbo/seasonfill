package tmdb

import "time"

// TVResponse is the raw JSON shape of GET /tv/{id} with
// append_to_response=aggregate_credits,videos,images,external_ids,content_ratings,keywords,recommendations.
//
// All embedded slices are nilable; callers (mappers) MUST treat
// missing arrays as empty. Date fields ship as YYYY-MM-DD strings;
// the mapper parses them via parseDate().
type TVResponse struct {
	ID                  int64          `json:"id"`
	Name                string         `json:"name"`
	OriginalName        string         `json:"original_name"`
	Overview            string         `json:"overview"`
	Tagline             string         `json:"tagline"`
	Status              string         `json:"status"`
	Type                string         `json:"type"`
	Homepage            string         `json:"homepage"`
	OriginalLanguage    string         `json:"original_language"`
	Languages           []string       `json:"languages"`
	OriginCountry       []string       `json:"origin_country"`
	FirstAirDate        string         `json:"first_air_date"`
	LastAirDate         string         `json:"last_air_date"`
	InProduction        bool           `json:"in_production"`
	NumberOfEpisodes    int            `json:"number_of_episodes"`
	NumberOfSeasons     int            `json:"number_of_seasons"`
	EpisodeRunTime      []int          `json:"episode_run_time"`
	Popularity          float64        `json:"popularity"`
	VoteAverage         float64        `json:"vote_average"`
	VoteCount           int            `json:"vote_count"`
	PosterPath          string         `json:"poster_path"`
	BackdropPath        string         `json:"backdrop_path"`
	NextEpisodeToAir    *TVEpisodeStub `json:"next_episode_to_air"`
	LastEpisodeToAir    *TVEpisodeStub `json:"last_episode_to_air"`
	Networks            []TVNetwork    `json:"networks"`
	ProductionCompanies []TVCompany    `json:"production_companies"`
	Seasons             []TVSeasonStub `json:"seasons"`
	Genres              []TVGenre      `json:"genres"`

	// append_to_response sub-resources.
	AggregateCredits *TVAggregateCredits `json:"aggregate_credits"`
	Videos           *TVVideos           `json:"videos"`
	Images           *TVImages           `json:"images"`
	ExternalIDs      *TVExternalIDs      `json:"external_ids"`
	ContentRatings   *TVContentRatings   `json:"content_ratings"`
	Keywords         *TVKeywords         `json:"keywords"`
	Recommendations  *TVRecommendations  `json:"recommendations"`
}

// TVEpisodeStub is the next/last episode embed. Used only for
// next_air_date / last_air_date sanity — the full episode payload
// comes from /tv/{id}/season/{n}.
type TVEpisodeStub struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	AirDate       string  `json:"air_date"`
	SeasonNumber  int     `json:"season_number"`
	EpisodeNumber int     `json:"episode_number"`
	Runtime       *int    `json:"runtime"`
	VoteAverage   float64 `json:"vote_average"`
	VoteCount     int     `json:"vote_count"`
	StillPath     string  `json:"still_path"`
}

// TVNetwork mirrors networks[*] in /tv/{id}. LogoPath maps to
// media_assets after pre-warm.
type TVNetwork struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	LogoPath      string `json:"logo_path"`
	OriginCountry string `json:"origin_country"`
}

// TVCompany mirrors production_companies[*].
type TVCompany struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	LogoPath      string `json:"logo_path"`
	OriginCountry string `json:"origin_country"`
}

// TVSeasonStub is the season summary embedded in /tv/{id} (not
// the full episode payload). EpisodeCount + Name + Overview are
// the only fields we copy into seasons-row writes; episodes come
// from /tv/{id}/season/{n}.
type TVSeasonStub struct {
	ID           int64  `json:"id"`
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	AirDate      string `json:"air_date"`
	EpisodeCount int    `json:"episode_count"`
	PosterPath   string `json:"poster_path"`
}

// TVGenre — TMDB ships id+name on /tv/{id}; we resolve the
// Genre row by tmdb_id (B-2b: taxonomy.Genre).
type TVGenre struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// TVAggregateCredits is the rich-shape credits embed: each cast
// member carries `roles[]` (per-role appearance count) and
// `total_episode_count`. Crew is the same shape but with `jobs[]`.
// We collapse the rich shape down to one SeriesCredit row per
// (person_id, tmdb_credit_id) — see MapTVToCredits.
type TVAggregateCredits struct {
	Cast []TVCastMember `json:"cast"`
	Crew []TVCrewMember `json:"crew"`
}

// TVCastMember mirrors aggregate_credits.cast[*]. The single
// "credit_id" / "character" pair on the legacy /credits endpoint
// is replaced here by roles[*] — but for the SeriesCredit row we
// pick the FIRST role's credit_id + character_name (the canonical
// long-running one). EpisodeCount comes from total_episode_count.
type TVCastMember struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	OriginalName       string   `json:"original_name"`
	Gender             *int     `json:"gender"`
	KnownForDepartment string   `json:"known_for_department"`
	Popularity         float64  `json:"popularity"`
	ProfilePath        string   `json:"profile_path"`
	Order              int      `json:"order"`
	TotalEpisodeCount  int      `json:"total_episode_count"`
	Roles              []TVRole `json:"roles"`
}

// TVRole is one row of cast[*].roles[*]. CreditID is the
// aggregate_credits-level id (stable across refreshes).
type TVRole struct {
	CreditID     string `json:"credit_id"`
	Character    string `json:"character"`
	EpisodeCount int    `json:"episode_count"`
}

// TVCrewMember mirrors aggregate_credits.crew[*]. Same rich shape
// as cast but with jobs[] instead of roles[].
type TVCrewMember struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	OriginalName       string  `json:"original_name"`
	Gender             *int    `json:"gender"`
	KnownForDepartment string  `json:"known_for_department"`
	Popularity         float64 `json:"popularity"`
	ProfilePath        string  `json:"profile_path"`
	Department         string  `json:"department"`
	TotalEpisodeCount  int     `json:"total_episode_count"`
	Jobs               []TVJob `json:"jobs"`
}

// TVJob is one row of crew[*].jobs[*]. CreditID + Job uniquely
// identify the contribution.
type TVJob struct {
	CreditID     string `json:"credit_id"`
	Job          string `json:"job"`
	EpisodeCount int    `json:"episode_count"`
}

// TVVideos — trailer / teaser embeds. Mapper picks the best one
// (Trailer + official + YouTube + most recent published_at).
type TVVideos struct {
	Results []TVVideo `json:"results"`
}

// TVVideo mirrors videos.results[*].
type TVVideo struct {
	ID          string `json:"id"`
	ISO6391     string `json:"iso_639_1"`
	ISO31661    string `json:"iso_3166_1"`
	Name        string `json:"name"`
	Key         string `json:"key"`
	Site        string `json:"site"`
	Size        int    `json:"size"`
	Type        string `json:"type"`
	Official    bool   `json:"official"`
	PublishedAt string `json:"published_at"`
}

// TVImages — poster/backdrop/logo embeds. Only poster_path /
// backdrop_path on the root row are used; the typed slices stay
// here for future media pre-warm work (F-1).
type TVImages struct {
	Backdrops []TVImage `json:"backdrops"`
	Posters   []TVImage `json:"posters"`
	Logos     []TVImage `json:"logos"`
}

// TVImage mirrors images.{backdrops,posters,logos}[*].
type TVImage struct {
	FilePath    string  `json:"file_path"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	ISO6391     *string `json:"iso_639_1"`
	VoteAverage float64 `json:"vote_average"`
	VoteCount   int     `json:"vote_count"`
}

// TVExternalIDs — external_ids embed. imdb_id may arrive in
// either "tt0944947" or raw-numeric form; the mapper normalises
// via NormaliseIMDBID (PRD §13 risk 6).
type TVExternalIDs struct {
	IMDBID      string `json:"imdb_id"`
	TVDBID      *int64 `json:"tvdb_id"`
	FacebookID  string `json:"facebook_id"`
	InstagramID string `json:"instagram_id"`
	TwitterID   string `json:"twitter_id"`
	WikidataID  string `json:"wikidata_id"`
}

// TVContentRatings — content_ratings embed. v1 reads only US +
// the operator's locale; mapper returns the full list and lets
// the worker pick.
type TVContentRatings struct {
	Results []TVContentRating `json:"results"`
}

// TVContentRating mirrors content_ratings.results[*].
type TVContentRating struct {
	ISO31661 string `json:"iso_3166_1"`
	Rating   string `json:"rating"`
}

// TVKeywords — keywords embed. TMDB ships `results` on movies
// and `results` on TV consistently in current API; some legacy
// payloads used `keywords` instead. We accept both via the
// `Results` field — fixtures all use `results`.
type TVKeywords struct {
	Results []TVKeyword `json:"results"`
}

// TVKeyword mirrors keywords.results[*].
type TVKeyword struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// TVRecommendations — recommendations embed. Each result is a TV
// summary; mapper produces stub Canon rows (see MapTVToRecommendations).
type TVRecommendations struct {
	Results []TVRecommendation `json:"results"`
}

// TVRecommendation mirrors recommendations.results[*]. We only
// pick the handful of fields a stub Canon row needs.
type TVRecommendation struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	OriginalName  string   `json:"original_name"`
	Overview      string   `json:"overview"`
	FirstAirDate  string   `json:"first_air_date"`
	PosterPath    string   `json:"poster_path"`
	BackdropPath  string   `json:"backdrop_path"`
	VoteAverage   float64  `json:"vote_average"`
	VoteCount     int      `json:"vote_count"`
	OriginCountry []string `json:"origin_country"`
	GenreIDs      []int64  `json:"genre_ids"`
}

// MappedVideo is the mapper output shape for one video — wrapped
// in a struct so the worker can write videos table rows without
// reaching back into the raw TMDB types. PublishedAt is *time.Time
// (parsed via mappers.go::parseRFC3339); nil on unparseable input.
type MappedVideo struct {
	TMDBID      string
	Language    string
	Country     string
	Name        string
	Key         string
	Site        string
	Type        string
	Official    bool
	PublishedAt *time.Time
	Size        int
}

// MappedContentRating is the mapper output for one content_ratings
// row. The persisted row carries (series_id, country, rating); the
// worker writes via ContentRatingsRepository (B-3).
type MappedContentRating struct {
	Country string
	Rating  string
}

// MappedExternalID is the mapper output for one row of the
// polymorphic external_ids table (B-3). EntityType is the
// caller's responsibility — mappers emit `Provider` + `ProviderID`
// only.
type MappedExternalID struct {
	Provider   string
	ProviderID string
}
