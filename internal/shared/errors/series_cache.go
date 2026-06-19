package errors

import (
	"fmt"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesCacheNotFoundError signals that the per-instance series_cache row
// keyed on (instance_name, sonarr_series_id) does not exist. Distinct from
// SeriesNotFoundError, which signals the canon `series` row is missing —
// they're different surfaces even when the user-visible 404 is the same.
// Maps to HTTP 404.
type SeriesCacheNotFoundError struct {
	InstanceName   domain.InstanceName
	SonarrSeriesID domain.SonarrSeriesID
}

func (e *SeriesCacheNotFoundError) Error() string {
	return fmt.Sprintf("series_cache row %s/%d not found", e.InstanceName, e.SonarrSeriesID)
}

func (e *SeriesCacheNotFoundError) Code() string { return "series_cache_not_found" }

func (e *SeriesCacheNotFoundError) Retriable() bool { return false }
