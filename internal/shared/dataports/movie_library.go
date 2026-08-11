package dataports

import (
	"context"
	"time"
)

// MovieLibraryState is the has_file/monitored-derived state filter for the
// movie library list (Ф6-R-6b). Movie analog of SeriesCacheState — but since
// EVERY listed movie is in the library (has an active movie_states row), the
// meaningful split is downloaded vs still-missing rather than imported vs all.
type MovieLibraryState string

const (
	// MovieLibraryStateAll — no state filter (default).
	MovieLibraryStateAll MovieLibraryState = "all"
	// MovieLibraryStateDownloaded — at least one instance has the file.
	MovieLibraryStateDownloaded MovieLibraryState = "downloaded"
	// MovieLibraryStateMissing — monitored on some instance but no instance
	// has the file yet.
	MovieLibraryStateMissing MovieLibraryState = "missing"
)

// IsValid reports whether the state is a recognized enum value.
func (s MovieLibraryState) IsValid() bool {
	switch s {
	case MovieLibraryStateAll, MovieLibraryStateDownloaded, MovieLibraryStateMissing:
		return true
	}
	return false
}

// MovieLibrarySort is the ordering key for the movie library list. Mirrors the
// series list's sort surface (updated_desc default + title_asc), plus a
// movie-appropriate release_desc.
type MovieLibrarySort string

const (
	// MovieLibrarySortUpdatedDesc — most-recently-synced first (default).
	MovieLibrarySortUpdatedDesc MovieLibrarySort = "updated_desc"
	// MovieLibrarySortTitleAsc — alphabetical by canon title.
	MovieLibrarySortTitleAsc MovieLibrarySort = "title_asc"
	// MovieLibrarySortReleaseDesc — newest theatrical release first; NULL
	// release dates sort last.
	MovieLibrarySortReleaseDesc MovieLibrarySort = "release_desc"
)

// IsValid reports whether the sort is a recognized enum value.
func (s MovieLibrarySort) IsValid() bool {
	switch s {
	case MovieLibrarySortUpdatedDesc, MovieLibrarySortTitleAsc, MovieLibrarySortReleaseDesc:
		return true
	}
	return false
}

// MovieLibraryFilter is the query filter for the movie library list.
type MovieLibraryFilter struct {
	State  MovieLibraryState
	Search string // case-insensitive canon-title substring; "" = no filter
}

// MovieLibraryRow is one deduplicated movie in the library — grouped by
// movies.id (one row per movie regardless of how many radarr instances hold
// it). Instance memberships are aggregated: Monitored/HasFile are OR across
// instances, SizeOnDisk is the largest copy, Instances lists every holding
// instance name (sorted). Poster is the RAW canon poster_asset — emitted the
// SAME way MovieDetailResponse.Poster is, so the FE's MediaImage/mediaUrl
// handling is consistent across the two endpoints.
type MovieLibraryRow struct {
	TMDBID      int
	Title       string
	Year        *int
	PosterAsset *string
	Status      *string
	ReleaseDate *time.Time
	TMDBRating  *float64
	IMDBRating  *float64
	Monitored   bool
	HasFile     bool
	Instances   []string
	SizeOnDisk  int64
	UpdatedAt   time.Time
}

// MovieLibraryRepository is the read port backing GET /api/v1/movies. List
// returns the matching page (offset/limit) plus the total count of DISTINCT
// movies matching the filter (for has_more / pagination).
type MovieLibraryRepository interface {
	List(ctx context.Context, filter MovieLibraryFilter, sort MovieLibrarySort, limit, offset int) ([]MovieLibraryRow, int, error)
}
