package dataports

import "context"

// RadarrLookupResult is the application-layer projection of one row in Radarr's
// GET /api/v3/movie/lookup response (Ф6-R-3, ADR-0018 §6b). Mirrors
// SonarrLookupResult; ImageURL is best-effort (remotePoster preferred over the
// relative images[].url an un-added movie carries).
type RadarrLookupResult struct {
	Title     string
	TitleSlug string
	Year      int
	TMDBID    int
	IMDBID    string
	Overview  string
	ImageURL  string
	Images    []LookupImage
}

// RadarrMovie is one row of Radarr's GET /api/v3/movie library — the shape the
// movie_states projection (radarr-sync, R-4 consumer) reads.
type RadarrMovie struct {
	RadarrMovieID       int
	Title               string
	TitleSlug           string
	Year                int
	TMDBID              int
	IMDBID              string
	Monitored           bool
	HasFile             bool
	MinimumAvailability string
	SizeOnDiskBytes     int64
}

// AddMoviePayload mirrors POST /api/v3/movie. MinimumAvailability defaults to
// "released" at the client when empty (ADR-0018 Q3).
type AddMoviePayload struct {
	TMDBID              int
	Title               string
	TitleSlug           string
	Year                int
	QualityProfileID    int
	RootFolderPath      string
	Monitored           bool
	MinimumAvailability string
	SearchOnAdd         bool
	Tags                []int
	Images              []LookupImage
}

type AddMovieResult struct {
	RadarrMovieID int
}

//go:generate moq -out radarr_mock.go . RadarrClient

type RadarrClient interface {
	SystemStatus(ctx context.Context) (SystemStatus, error)
	GetQualityProfile(ctx context.Context, id int) (QualityProfile, error)
	ListQualityProfiles(ctx context.Context) ([]QualityProfile, error)
	ListRootFolders(ctx context.Context) ([]RootFolder, error)
	CreateTag(ctx context.Context, label string) (Tag, error)
	Name() string
	// LookupMovie calls GET /api/v3/movie/lookup?term={term}. The add-flow
	// passes term="tmdb:{id}" for a deterministic single-row match. Returns the
	// empty slice (no error) on Radarr "no matches"; the caller surfaces 404.
	LookupMovie(ctx context.Context, term string) ([]RadarrLookupResult, error)
	// ListMovies calls GET /api/v3/movie — the full library, for the
	// movie_states projection (R-4 consumer; R-3 lands the method).
	ListMovies(ctx context.Context) ([]RadarrMovie, error)
	// AddMovie posts to POST /api/v3/movie. Idempotent at the Radarr layer —
	// a duplicate tmdbId returns a 400 the use case maps to already-added.
	AddMovie(ctx context.Context, p AddMoviePayload) (AddMovieResult, error)
}
