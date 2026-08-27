package tmdb

// collection_types.go — raw JSON shapes for GET /collection/{id} (Ф6-R-5). A
// collection is a franchise grouping (belongs_to_collection on /movie/{id}); the
// detail endpoint returns the collection art plus a `parts` array of member-movie
// summaries. All fields are the raw wire shape — dates ship as YYYY-MM-DD strings
// parsed by the mapper (parseDate). Do NOT add business logic here.

// CollectionResponse is GET /collection/{id}. Parts are member-movie summaries
// (NOT full /movie/{id} detail) — enough to COALESCE stub-upsert each part into
// the movies canon so the collection_id linkage is populated.
type CollectionResponse struct {
	ID           int64            `json:"id"`
	Name         string           `json:"name"`
	Overview     string           `json:"overview"`
	PosterPath   string           `json:"poster_path"`
	BackdropPath string           `json:"backdrop_path"`
	Parts        []CollectionPart `json:"parts"`
	// Translations — append_to_response=translations sub-resource (F-08 S2).
	// REUSES MovieTranslations: a collection translation's localized NAME is
	// keyed data.title (same as a movie, NOT data.name like TV), so the movie
	// type maps 1:1 (Title → collection name, Overview → collection overview).
	// Nilable — callers MUST treat a nil pointer as "no translations".
	Translations *MovieTranslations `json:"translations"`
	// Images — append_to_response=images sub-resource (F-08 S4). REUSES TVImages
	// (the movie/collection detail reuses the TV poster/backdrop embed, mirror of
	// MovieResponse.Images at movie_types.go:46). posters[*].iso_639_1 lets the
	// localized writer pick a per-language poster (pickPosterForLangStrict). Nilable —
	// callers MUST treat nil as "no images".
	Images *TVImages `json:"images"`
}

// CollectionPart is one member-movie summary inside a collection's `parts`. Only
// the fields the stub-upsert consumes are typed; the rest are ignored.
type CollectionPart struct {
	ID               int64   `json:"id"`
	Title            string  `json:"title"`
	OriginalTitle    string  `json:"original_title"`
	OriginalLanguage string  `json:"original_language"`
	Overview         string  `json:"overview"`
	ReleaseDate      string  `json:"release_date"`
	PosterPath       string  `json:"poster_path"`
	BackdropPath     string  `json:"backdrop_path"`
	Popularity       float64 `json:"popularity"`
	VoteAverage      float64 `json:"vote_average"`
	VoteCount        int     `json:"vote_count"`
}
