package dto

import "time"

// MovieDetailResponse is the wire aggregate for GET /api/v1/movies/:tmdb_id
// (Ф6-R-6a). Sourced from local repos; degraded lists absent slices.
type MovieDetailResponse struct {
	TMDBID     int                    `json:"tmdb_id"`
	IMDBID     *string                `json:"imdb_id"`
	Title      string                 `json:"title"`
	Overview   *string                `json:"overview"`
	Tagline    *string                `json:"tagline"`
	Year       *int                   `json:"year"`
	Status     *string                `json:"status"`
	Runtime    *int                   `json:"runtime_minutes"`
	Poster     *string                `json:"poster"`
	Backdrop   *string                `json:"backdrop"`
	Released   *time.Time             `json:"release_date"`
	Digital    *time.Time             `json:"digital_release_date"`
	Physical   *time.Time             `json:"physical_release_date"`
	TMDBRating *float64               `json:"tmdb_rating"`
	IMDBRating *float64               `json:"imdb_rating"`
	Collection *MovieDetailCollection `json:"collection"`
	Library    []MovieDetailLibrary   `json:"library"`
	// Genres are localized taxonomy chips mirroring the series hero (Ф2.5a).
	// Each chip carries its own resolved Language (en-US when the requested lang
	// had no row). Omitted when the movie has no genres attached yet.
	Genres []TaxonomyChip `json:"genres,omitempty"`
	// Keywords are localized taxonomy chips (Ф2.5a). v1 keywords are en-only, so
	// Language is en-US for any requested lang. Omitted when none attached.
	Keywords []TaxonomyChip `json:"keywords,omitempty"`
	Degraded []string       `json:"degraded"`
}

// MovieDetailCollection is the franchise-collection header on a movie detail.
type MovieDetailCollection struct {
	TMDBCollectionID int     `json:"tmdb_collection_id"`
	Name             string  `json:"name"`
	Poster           *string `json:"poster"`
	RadarrMonitored  bool    `json:"radarr_monitored"`
}

// MovieDetailLibrary is one per-instance Radarr library-membership row.
type MovieDetailLibrary struct {
	InstanceName  string  `json:"instance_name"`
	RadarrMovieID int     `json:"radarr_movie_id"`
	Monitored     bool    `json:"monitored"`
	HasFile       bool    `json:"has_file"`
	Availability  *string `json:"availability"`
	SizeOnDisk    int64   `json:"size_on_disk_bytes"`
}
