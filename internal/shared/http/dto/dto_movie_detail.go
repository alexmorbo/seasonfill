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
	Degraded   []string               `json:"degraded"`
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
