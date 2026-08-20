package movie

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// StateEntry is the domain shape of one movie_states row (Ф6-R-4b) — the
// per-instance Radarr library-membership projection. Mirror of
// series.CacheEntry for the movie vertical. movie_id is the resolved
// movies.id (FK); it is 0 on the value returned by scan.BuildRadarrMovieCache
// and stamped by scan.PersistRadarrMovieCache after the canon Upsert.
// DeletedAt is *time.Time because rows are soft-deleted (MovieDelete webhook)
// to keep any FK references valid — mirror of series_cache.
type StateEntry struct {
	InstanceName    domain.InstanceName
	RadarrMovieID   int
	MovieID         domain.MovieID
	TitleSlug       string
	Monitored       bool
	HasFile         bool
	Availability    *string
	Quality         *string
	Resolution      *int
	VideoCodec      *string
	AudioCodec      *string
	SizeOnDiskBytes int64
	AddedToRadarr   bool
	UpdatedAt       time.Time
	DeletedAt       *time.Time
}

// IsActive reports whether the entry is non-soft-deleted.
func (e StateEntry) IsActive() bool { return e.DeletedAt == nil }
