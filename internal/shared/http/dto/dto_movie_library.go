package dto

import "time"

// MovieLibraryItem is one deduplicated movie in the library list
// (GET /api/v1/movies, Ф6-R-6b). Movie analog of SeriesCacheItem, kept lean.
// One item per movie (grouped by tmdb id) regardless of how many radarr
// instances hold it; instance memberships are aggregated. `poster` is the raw
// canon poster_asset — emitted identically to MovieDetailResponse.poster so the
// FE resolves it the same way.
type MovieLibraryItem struct {
	TMDBID      int        `json:"tmdb_id"      example:"438631"`
	Title       string     `json:"title"        example:"Dune"`
	Year        *int       `json:"year,omitempty" example:"2021"`
	Poster      *string    `json:"poster"`
	Status      *string    `json:"status,omitempty" example:"released"`
	ReleaseDate *time.Time `json:"release_date,omitempty"`
	TMDBRating  *float64   `json:"tmdb_rating,omitempty" example:"8.1"`
	IMDBRating  *float64   `json:"imdb_rating,omitempty" example:"8.0"`
	// Monitored / HasFile are OR-aggregated across every holding instance.
	Monitored bool `json:"monitored"`
	HasFile   bool `json:"has_file"`
	// Instances lists every radarr instance holding the movie (sorted).
	Instances []string `json:"instances"`
	// SizeOnDisk is the largest copy across instances (bytes).
	SizeOnDisk int64     `json:"size_on_disk_bytes" example:"5000000000"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// MovieLibraryList — body of GET /api/v1/movies. Mirrors SeriesCacheList's
// pagination envelope (total + has_more + next_cursor).
type MovieLibraryList struct {
	Items      []MovieLibraryItem `json:"items"`
	Total      int                `json:"total"      example:"42"`
	HasMore    bool               `json:"has_more"   example:"true"`
	NextCursor string             `json:"next_cursor,omitempty"`
}
