package dataports

import "context"

// movie_collections.go — Ф6-R-5 TMDB-franchise collection ports. DISTINCT from
// collections.go (Ф7 insight "curated collections"). The names here all carry a
// MovieCollection* / RadarrCollection prefix to avoid any symbol clash.

// MovieCollectionPart is one member movie of a TMDB collection projected with its
// per-instance library membership (Ф6-R-5). TMDBID is 0 when the canon row has no
// tmdb_id (a Radarr orphan that happens to share a collection_id — cannot be
// add-to-Radarr'd, so the add-all-missing usecase skips it). InLibrary is true
// when an ACTIVE movie_states row exists for the queried instance. Title is the
// localized title when a movie_i18n row exists for the requested language, else
// the canon title (U-2). Poster is the RAW canon movies.poster_asset path
// (nil-able); the REST handler resolves it to a media hash (resolver lives in the
// handler, not the repo — mirrors moviedetail).
type MovieCollectionPart struct {
	MovieID   int64
	TMDBID    int
	Title     string
	Year      *int
	InLibrary bool
	Poster    *string
}

// MovieCollectionsReader is the usecase-layer read port over the collection-parts
// projection. Production impl:
// *enrichpersistence.MovieCollectionsRepository.ListPartsWithMembership.
type MovieCollectionsReader interface {
	// ListPartsWithMembership returns every canon movie whose collection_id equals
	// tmdbCollectionID, LEFT-JOINed to that instance's ACTIVE movie_states rows
	// (deleted_at IS NULL) so InLibrary reflects the given instance, and
	// LEFT-JOINed to movie_i18n on lang so Title is localized when present (canon
	// title fallback). Poster carries the raw movies.poster_asset path. Ordered by
	// movies.id ASC (deterministic, dialect-portable). Empty → (nil, nil).
	ListPartsWithMembership(ctx context.Context, tmdbCollectionID int, instanceName, lang string) ([]MovieCollectionPart, error)
}

// RadarrCollection is the neutral value shape of a Radarr v3 collection resource
// (GET/PUT /api/v3/collection). Deliberately NOT part of the RadarrClient
// interface (no moq regen); the collection methods live on the concrete
// *radarr.Client and are consumed via the narrow RadarrCollectionClient port in
// the moviecollection usecase package.
type RadarrCollection struct {
	ID                  int
	Title               string
	TMDBID              int
	Monitored           bool
	SearchOnAdd         bool
	QualityProfileID    int
	MinimumAvailability string
	RootFolderPath      string
}
