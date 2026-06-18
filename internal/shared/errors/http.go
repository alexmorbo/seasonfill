package errors

import (
	"errors"
	"net/http"
)

// StatusCode walks err's chain via errors.As and returns the HTTP status
// for the deepest typed match. Defaults to 500 for non-nil untyped errors;
// returns 0 for nil.
//
// Mapping order is locked: 4xx mappings come before 5xx so the most
// specific match wins. New typed errors must be added to this switch when
// they ship.
func StatusCode(err error) int {
	if err == nil {
		return 0
	}
	var (
		seriesNF   *SeriesNotFoundError
		seriesLoad *SeriesCanonicalLoadError
		sonarrU    *SonarrUnreachableError
		sonarrI    *SonarrInstanceInvalidError
		tmdbRL     *TMDBRateLimitedError
		tmdbAuth   *TMDBAuthError
		tmdbNF     *TMDBNotFoundError
		omdbQ      *OMDbQuotaExhaustedError
		omdbNF     *OMDbNotFoundError
		omdbAuth   *OMDbAuthError
		scanF      *ScanFailedError
		scanIP     *ScanInProgressError
	)
	switch {
	case errors.As(err, &seriesNF):
		return http.StatusNotFound
	case errors.As(err, &tmdbNF):
		return http.StatusNotFound
	case errors.As(err, &omdbNF):
		return http.StatusNotFound
	case errors.As(err, &scanIP):
		return http.StatusConflict
	case errors.As(err, &sonarrI):
		return http.StatusBadRequest
	case errors.As(err, &tmdbRL):
		return http.StatusTooManyRequests
	case errors.As(err, &tmdbAuth):
		return http.StatusBadGateway
	case errors.As(err, &omdbAuth):
		return http.StatusBadGateway
	case errors.As(err, &sonarrU):
		return http.StatusBadGateway
	case errors.As(err, &omdbQ):
		return http.StatusServiceUnavailable
	case errors.As(err, &scanF):
		return http.StatusInternalServerError
	case errors.As(err, &seriesLoad):
		return http.StatusInternalServerError
	}
	return http.StatusInternalServerError
}
