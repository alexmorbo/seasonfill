package tmdb

import "github.com/alexmorbo/seasonfill/internal/shared/domain"

// PersonResponse mirrors GET /person/{id} with
// append_to_response=tv_credits,movie_credits,external_ids.
type PersonResponse struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	OriginalName       string   `json:"original_name"`
	AlsoKnownAs        []string `json:"also_known_as"`
	Biography          string   `json:"biography"`
	Birthday           string   `json:"birthday"`
	Deathday           string   `json:"deathday"`
	Gender             *int     `json:"gender"`
	PlaceOfBirth       string   `json:"place_of_birth"`
	KnownForDepartment string   `json:"known_for_department"`
	Popularity         float64  `json:"popularity"`
	ProfilePath        string   `json:"profile_path"`
	IMDBID             string   `json:"imdb_id"`
	Homepage           string   `json:"homepage"`

	TVCredits    *PersonTVCredits    `json:"tv_credits"`
	MovieCredits *PersonMovieCredits `json:"movie_credits"`
	ExternalIDs  *PersonExternalIDs  `json:"external_ids"`
	Translations *PersonTranslations `json:"translations"`
}

// PersonTVCredits mirrors tv_credits sub-resource.
type PersonTVCredits struct {
	Cast []PersonTVCredit `json:"cast"`
	Crew []PersonTVCredit `json:"crew"`
}

// PersonMovieCredits mirrors movie_credits sub-resource.
type PersonMovieCredits struct {
	Cast []PersonMovieCredit `json:"cast"`
	Crew []PersonMovieCredit `json:"crew"`
}

// PersonTVCredit is one row of tv_credits.{cast,crew}[*]. Fields
// differ between cast (Character) and crew (Department + Job),
// but the JSON shape is unified — empty strings on the inapplicable
// side.
type PersonTVCredit struct {
	ID           int64   `json:"id"`
	CreditID     string  `json:"credit_id"`
	Name         string  `json:"name"`
	OriginalName string  `json:"original_name"`
	Character    string  `json:"character"`
	Department   string  `json:"department"`
	Job          string  `json:"job"`
	EpisodeCount int     `json:"episode_count"`
	FirstAirDate string  `json:"first_air_date"`
	PosterPath   string  `json:"poster_path"`
	VoteAverage  float64 `json:"vote_average"`
	VoteCount    int     `json:"vote_count"`
}

// PersonMovieCredit is one row of movie_credits.{cast,crew}[*].
type PersonMovieCredit struct {
	ID            int64   `json:"id"`
	CreditID      string  `json:"credit_id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Character     string  `json:"character"`
	Department    string  `json:"department"`
	Job           string  `json:"job"`
	ReleaseDate   string  `json:"release_date"`
	PosterPath    string  `json:"poster_path"`
	VoteAverage   float64 `json:"vote_average"`
	VoteCount     int     `json:"vote_count"`
}

// PersonExternalIDs mirrors external_ids sub-resource.
type PersonExternalIDs struct {
	IMDBID      string         `json:"imdb_id"`
	FacebookID  string         `json:"facebook_id"`
	InstagramID string         `json:"instagram_id"`
	TwitterID   string         `json:"twitter_id"`
	WikidataID  string         `json:"wikidata_id"`
	TVDBID      *domain.TVDBID `json:"tvdb_id"`
}

// PersonTranslations — append_to_response=translations sub-resource on
// /person/{id}. Each entry carries one language's localised biography.
// S-H reads this to populate person_biographies for every supported
// language from a single GetPerson round-trip (mirrors TVTranslations).
type PersonTranslations struct {
	Translations []PersonTranslation `json:"translations"`
}

// PersonTranslation is one row of translations.translations[*]. ISO6391 is
// the bare 2-letter language code (matched against shortLang(userTag)); Data
// holds the localised biography.
type PersonTranslation struct {
	ISO6391  string                `json:"iso_639_1"`
	ISO31661 string                `json:"iso_3166_1"`
	Data     PersonTranslationData `json:"data"`
}

// PersonTranslationData is the `data` object inside a PersonTranslation. The
// person-translations sub-resource ships a localised biography (and name); S-H
// uses Biography. A named type (not an anonymous struct) mirrors
// TVTranslationData and keeps test fixtures constructible.
type PersonTranslationData struct {
	Biography string `json:"biography"`
	Name      string `json:"name"`
}
