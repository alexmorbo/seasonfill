package tmdb

// movie_types.go — raw JSON shapes for GET /movie/{id} (Ф6-R-4a L3-2). Movie
// analog of tv_types.go; do NOT touch tv_types.go. The detail response reuses
// TVImages (structurally identical poster/backdrop embed) but carries its OWN
// MovieTranslations: a movie translation's localized title is keyed "title",
// whereas a TV translation's is "name" — reusing TVTranslations silently dropped
// every localized movie title.

// MovieResponse is the raw JSON shape of GET /movie/{id} with
// append_to_response=external_ids,release_dates,images,translations. All
// embedded pointers are nilable; the mapper (MapMovieToCanon) treats missing
// sub-resources as empty. Date fields ship as YYYY-MM-DD strings parsed via
// parseDate(). Unlike /tv, /movie carries imdb_id at the TOP level (external_ids
// is a defensive fallback).
type MovieResponse struct {
	ID                  int64               `json:"id"`
	IMDBID              string              `json:"imdb_id"`
	Title               string              `json:"title"`
	OriginalTitle       string              `json:"original_title"`
	Overview            string              `json:"overview"`
	Tagline             string              `json:"tagline"`
	Status              string              `json:"status"`
	ReleaseDate         string              `json:"release_date"`
	Runtime             int                 `json:"runtime"`
	Budget              int64               `json:"budget"`
	Revenue             int64               `json:"revenue"`
	Homepage            string              `json:"homepage"`
	OriginalLanguage    string              `json:"original_language"`
	OriginCountry       []string            `json:"origin_country"`
	ProductionCountries []MovieProdCountry  `json:"production_countries"`
	Popularity          float64             `json:"popularity"`
	VoteAverage         float64             `json:"vote_average"`
	VoteCount           int                 `json:"vote_count"`
	PosterPath          string              `json:"poster_path"`
	BackdropPath        string              `json:"backdrop_path"`
	BelongsToCollection *MovieCollectionRef `json:"belongs_to_collection"`
	Adult               bool                `json:"adult"`
	// Ф1.1b — /movie ROOT taxonomy arrays (present by default, no append token).
	Genres              []TVGenre   `json:"genres"`               // reuse TVGenre {id,name}
	ProductionCompanies []TVCompany `json:"production_companies"` // reuse TVCompany {id,name,logo_path,origin_country}

	// append_to_response sub-resources.
	ExternalIDs  *MovieExternalIDs  `json:"external_ids"`
	ReleaseDates *MovieReleaseDates `json:"release_dates"`
	Images       *TVImages          `json:"images"`       // reused: posters/backdrops
	Translations *MovieTranslations `json:"translations"` // movie shape: data.title (NOT data.name like TV)
	// Credits — Ф1.1a movie cast writer. FLAT credits (append_to_response=credits):
	// cast[*] carries a 0-based `order` billing index (0 = lead). Crew is deferred
	// to Ф1.1b, so only Cast is decoded here.
	Credits *MovieCredits `json:"credits"`
	// Keywords — Ф1.1b. Movie shape is keywords.keywords[] (NOT TV's keywords.results[]);
	// requires append_to_response=keywords.
	Keywords *MovieKeywords `json:"keywords"`
	// Ф1.1c append_to_response=recommendations,videos.
	// Recommendations: movie shape uses title/original_title/release_date (NOT TV's
	// name/first_air_date), so it needs its own element type. Videos reuses TVVideos —
	// /movie videos.results[*] is structurally identical to /tv's.
	Recommendations *MovieRecommendations `json:"recommendations"`
	Videos          *TVVideos             `json:"videos"`
}

// MovieTranslations — append_to_response=translations sub-resource on GET
// /movie/{id}. STRUCTURALLY DISTINCT from TVTranslations on purpose: a movie
// translation's data.title is keyed "title", a TV translation's is "name". Do
// NOT collapse this back into TVTranslations — that mismatch left every localized
// movie title NULL (movie_i18n.title empty for all non-base languages).
type MovieTranslations struct {
	Translations []MovieTranslation `json:"translations"`
}

// MovieTranslation is one row of translations.translations[*]. ISO6391 is the
// bare 2-letter language code (matched against shortLang(userTag)); Data holds
// the localised text fields.
type MovieTranslation struct {
	ISO6391  string               `json:"iso_639_1"`
	ISO31661 string               `json:"iso_3166_1"`
	Data     MovieTranslationData `json:"data"`
}

// MovieTranslationData is the `data` object inside a MovieTranslation. The
// localized title arrives under "title" (the TV shape uses "name") — this key is
// the whole reason a dedicated movie type exists.
type MovieTranslationData struct {
	Title    string `json:"title"`
	Overview string `json:"overview"`
	Tagline  string `json:"tagline"`
	Homepage string `json:"homepage"`
}

// MovieRecommendations mirrors the /movie/{id} recommendations sub-resource (Ф1.1c). Each
// result is a movie summary; the mapper (MapMovieRecommendations) produces stub movie.Canon
// rows the recs writer UpsertStubs before writing the movie_recommendations join.
type MovieRecommendations struct {
	Results []MovieRecommendation `json:"results"`
}

// MovieRecommendation mirrors recommendations.results[*] on /movie. Only the handful of
// fields a stub movie.Canon needs are decoded. Note the movie field names (title,
// original_title, release_date) differ from TVRecommendation (name, first_air_date).
type MovieRecommendation struct {
	ID               int64   `json:"id"`
	Title            string  `json:"title"`
	OriginalTitle    string  `json:"original_title"`
	OriginalLanguage string  `json:"original_language"`
	Overview         string  `json:"overview"`
	ReleaseDate      string  `json:"release_date"`
	PosterPath       string  `json:"poster_path"`
	BackdropPath     string  `json:"backdrop_path"`
	VoteAverage      float64 `json:"vote_average"`
	VoteCount        int     `json:"vote_count"`
	Popularity       float64 `json:"popularity"`
}

// MovieKeywords mirrors the /movie/{id} keywords sub-resource. The movie payload nests the
// list under `keywords` (NOT `results` as /tv does), so it needs its own wrapper; the
// element reuses TVKeyword ({id,name}). Ф1.1b.
type MovieKeywords struct {
	Keywords []TVKeyword `json:"keywords"`
}

// MovieCredits mirrors the /movie/{id} credits sub-resource. Only Cast is consumed
// in Ф1.1a; crew is a Ф1.1b concern (add a Crew field then).
type MovieCredits struct {
	Cast []MovieCastMember `json:"cast"`
}

// MovieCastMember is one FLAT credits.cast[*] row. Order is the 0-based TMDB
// billing index (0 = top billing). CreditID is the stable per-credit id used as
// the person_credits natural key.
type MovieCastMember struct {
	ID                 int64   `json:"id"` // TMDB person id
	Name               string  `json:"name"`
	OriginalName       string  `json:"original_name"`
	Gender             *int    `json:"gender"`
	KnownForDepartment string  `json:"known_for_department"`
	Popularity         float64 `json:"popularity"`
	ProfilePath        string  `json:"profile_path"`
	Character          string  `json:"character"`
	CreditID           string  `json:"credit_id"`
	Order              int     `json:"order"`
}

// MovieCollectionRef mirrors belongs_to_collection. Only the id is copied into
// movies.collection_id (the collection's own row is a later Ф6 concern).
type MovieCollectionRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// MovieProdCountry mirrors production_countries[*]. Copied into
// movies.origin_countries when the top-level origin_country array is empty
// (/movie/{id} usually ships production_countries, not origin_country).
type MovieProdCountry struct {
	ISO31661 string `json:"iso_3166_1"`
	Name     string `json:"name"`
}

// MovieExternalIDs mirrors the external_ids embed. imdb_id may arrive in
// "tt1234567" or raw-numeric form; the mapper normalises via NormaliseIMDBID.
type MovieExternalIDs struct {
	IMDBID      string `json:"imdb_id"`
	WikidataID  string `json:"wikidata_id"`
	FacebookID  string `json:"facebook_id"`
	InstagramID string `json:"instagram_id"`
	TwitterID   string `json:"twitter_id"`
}

// MovieReleaseDates mirrors the release_dates embed. Each result groups a
// country's typed release rows (type 4 = digital, 5 = physical, 3 = theatrical).
type MovieReleaseDates struct {
	Results []MovieReleaseDateCountry `json:"results"`
}

// MovieReleaseDateCountry is one country's release-date group.
type MovieReleaseDateCountry struct {
	ISO31661     string                  `json:"iso_3166_1"`
	ReleaseDates []MovieReleaseDateEntry `json:"release_dates"`
}

// MovieReleaseDateEntry is one typed release row. Type enum: 1 Premiere,
// 2 Theatrical(limited), 3 Theatrical, 4 Digital, 5 Physical, 6 TV.
type MovieReleaseDateEntry struct {
	Type        int    `json:"type"`
	ReleaseDate string `json:"release_date"`
	Note        string `json:"note"`
}
