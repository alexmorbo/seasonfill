package dto

// dto_movie_collections.go — Ф6-R-6a wire shapes for the three TMDB-franchise
// collection routes (GET detail, POST add-all-missing, PUT monitor).

// MovieCollectionDetail is GET /api/v1/collections/:tmdb_collection_id.
type MovieCollectionDetail struct {
	TMDBCollectionID int                      `json:"tmdb_collection_id"`
	Name             string                   `json:"name"`
	Overview         *string                  `json:"overview"`
	Poster           *string                  `json:"poster"`
	RadarrMonitored  bool                     `json:"radarr_monitored"`
	Instance         string                   `json:"instance"`
	Parts            []MovieCollectionPartDTO `json:"parts"`
}

type MovieCollectionPartDTO struct {
	MovieID   int64   `json:"movie_id"`
	TMDBID    int     `json:"tmdb_id"`
	Title     string  `json:"title"`
	Year      *int    `json:"year"`
	InLibrary bool    `json:"in_library"`
	Poster    *string `json:"poster"`
}

// MovieCollectionAddAllRequest is the POST /collections/:id/add-all-missing body.
type MovieCollectionAddAllRequest struct {
	InstanceName        string `json:"instance_name"`
	QualityProfileID    int    `json:"quality_profile_id"`
	RootFolderPath      string `json:"root_folder_path"`
	Monitored           bool   `json:"monitored"`
	MinimumAvailability string `json:"minimum_availability,omitempty"`
	SearchOnAdd         bool   `json:"search_on_add,omitempty"`
}

// MovieCollectionAddAllResponse is the POST /collections/:id/add-all-missing result.
type MovieCollectionAddAllResponse struct {
	Requested      int                         `json:"requested"`
	Added          int                         `json:"added"`
	AlreadyPresent int                         `json:"already_present"`
	Failed         int                         `json:"failed"`
	Parts          []MovieCollectionAddPartDTO `json:"parts"`
}

type MovieCollectionAddPartDTO struct {
	TMDBID        int    `json:"tmdb_id"`
	Title         string `json:"title"`
	RadarrMovieID int    `json:"radarr_movie_id,omitempty"`
	AlreadyAdded  bool   `json:"already_added,omitempty"`
	Skipped       bool   `json:"skipped,omitempty"`
	Error         string `json:"error,omitempty"`
}

// MovieCollectionMonitorRequest is the PUT /collections/:id/monitor body.
type MovieCollectionMonitorRequest struct {
	InstanceName string `json:"instance_name"`
}
