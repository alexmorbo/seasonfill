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
		seriesNF      *SeriesNotFoundError
		seriesLoad    *SeriesCanonicalLoadError
		sonarrU       *SonarrUnreachableError
		sonarrI       *SonarrInstanceInvalidError
		tmdbRL        *TMDBRateLimitedError
		tmdbAuth      *TMDBAuthError
		tmdbNF        *TMDBNotFoundError
		omdbQ         *OMDbQuotaExhaustedError
		omdbNF        *OMDbNotFoundError
		omdbAuth      *OMDbAuthError
		scanF         *ScanFailedError
		scanIP        *ScanInProgressError
		seriesCacheNF *SeriesCacheNotFoundError
		episodeNF     *EpisodeNotFoundError
		seasonNF      *SeasonNotFoundError
		adminNF       *AdminUserNotFoundError
		instanceNF    *InstanceNotFoundError
		grabNF        *GrabNotFoundError
		runtimeNF     *RuntimeConfigNotFoundError
		appSetNF      *AppSettingsNotFoundError
		qbitSetNF     *QbitSettingsNotFoundError
		scanRunNF     *ScanRunNotFoundError
		decisionNF    *DecisionNotFoundError
		wbNF          *WatchdogBlacklistNotFoundError
	)
	switch {
	case errors.As(err, &seriesNF):
		return http.StatusNotFound
	case errors.As(err, &tmdbNF):
		return http.StatusNotFound
	case errors.As(err, &omdbNF):
		return http.StatusNotFound
	case errors.As(err, &seriesCacheNF):
		return http.StatusNotFound
	case errors.As(err, &episodeNF):
		return http.StatusNotFound
	case errors.As(err, &seasonNF):
		return http.StatusNotFound
	case errors.As(err, &adminNF):
		return http.StatusNotFound
	case errors.As(err, &instanceNF):
		return http.StatusNotFound
	case errors.As(err, &grabNF):
		return http.StatusNotFound
	case errors.As(err, &runtimeNF):
		return http.StatusNotFound
	case errors.As(err, &appSetNF):
		return http.StatusNotFound
	case errors.As(err, &qbitSetNF):
		return http.StatusNotFound
	case errors.As(err, &scanRunNF):
		return http.StatusNotFound
	case errors.As(err, &decisionNF):
		return http.StatusNotFound
	case errors.As(err, &wbNF):
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
