// Package errors holds typed domain errors emitted by application
// use-cases, repositories, and workers. The companion HTTP middleware in
// interface/http/middleware/errors.go maps them to status codes.
//
// All errors expose Code() (a stable slug used in JSON responses and in
// metrics labels) and may implement Retriable() bool. Wrap with %w when
// adding context — IsRetriable / StatusCode / ErrorCode walk the chain
// via errors.As.
package errors

import "errors"

// Retriable is satisfied by any error that knows whether the caller
// should retry. Wrapped errors are unwrapped via errors.As.
type Retriable interface {
	Retriable() bool
}

// Coded is satisfied by any error that exposes a stable identifier slug
// for the response envelope ({"error": "<code>"}).
type Coded interface {
	Code() string
}

// IsRetriable reports whether err (or anything in its wrap chain) wants
// the caller to retry. Returns false for nil.
//
// The walk uses errors.As against each known typed error rather than the
// Retriable interface directly. This is deliberate: it forces every new
// typed error to be registered here, keeping the retry surface area
// explicit and grep-friendly. New typed errors must be added to this
// switch (and to StatusCode in http.go) when they ship.
func IsRetriable(err error) bool {
	if err == nil {
		return false
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
		return seriesNF.Retriable()
	case errors.As(err, &seriesLoad):
		return seriesLoad.Retriable()
	case errors.As(err, &sonarrU):
		return sonarrU.Retriable()
	case errors.As(err, &sonarrI):
		return sonarrI.Retriable()
	case errors.As(err, &tmdbRL):
		return tmdbRL.Retriable()
	case errors.As(err, &tmdbAuth):
		return tmdbAuth.Retriable()
	case errors.As(err, &tmdbNF):
		return tmdbNF.Retriable()
	case errors.As(err, &omdbQ):
		return omdbQ.Retriable()
	case errors.As(err, &omdbNF):
		return omdbNF.Retriable()
	case errors.As(err, &omdbAuth):
		return omdbAuth.Retriable()
	case errors.As(err, &scanF):
		return scanF.Retriable()
	case errors.As(err, &scanIP):
		return scanIP.Retriable()
	case errors.As(err, &seriesCacheNF):
		return seriesCacheNF.Retriable()
	case errors.As(err, &episodeNF):
		return episodeNF.Retriable()
	case errors.As(err, &seasonNF):
		return seasonNF.Retriable()
	case errors.As(err, &adminNF):
		return adminNF.Retriable()
	case errors.As(err, &instanceNF):
		return instanceNF.Retriable()
	case errors.As(err, &grabNF):
		return grabNF.Retriable()
	case errors.As(err, &runtimeNF):
		return runtimeNF.Retriable()
	case errors.As(err, &appSetNF):
		return appSetNF.Retriable()
	case errors.As(err, &qbitSetNF):
		return qbitSetNF.Retriable()
	case errors.As(err, &scanRunNF):
		return scanRunNF.Retriable()
	case errors.As(err, &decisionNF):
		return decisionNF.Retriable()
	case errors.As(err, &wbNF):
		return wbNF.Retriable()
	}
	return false
}

// ErrorCode returns the slug from the deepest typed error in err's wrap
// chain, or "internal_error" if err is non-nil but untyped. Returns "" for
// nil.
//
// As with IsRetriable, the switch is the source of truth for which typed
// errors participate in the response envelope.
func ErrorCode(err error) string {
	if err == nil {
		return ""
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
		return seriesNF.Code()
	case errors.As(err, &seriesLoad):
		return seriesLoad.Code()
	case errors.As(err, &sonarrU):
		return sonarrU.Code()
	case errors.As(err, &sonarrI):
		return sonarrI.Code()
	case errors.As(err, &tmdbRL):
		return tmdbRL.Code()
	case errors.As(err, &tmdbAuth):
		return tmdbAuth.Code()
	case errors.As(err, &tmdbNF):
		return tmdbNF.Code()
	case errors.As(err, &omdbQ):
		return omdbQ.Code()
	case errors.As(err, &omdbNF):
		return omdbNF.Code()
	case errors.As(err, &omdbAuth):
		return omdbAuth.Code()
	case errors.As(err, &scanF):
		return scanF.Code()
	case errors.As(err, &scanIP):
		return scanIP.Code()
	case errors.As(err, &seriesCacheNF):
		return seriesCacheNF.Code()
	case errors.As(err, &episodeNF):
		return episodeNF.Code()
	case errors.As(err, &seasonNF):
		return seasonNF.Code()
	case errors.As(err, &adminNF):
		return adminNF.Code()
	case errors.As(err, &instanceNF):
		return instanceNF.Code()
	case errors.As(err, &grabNF):
		return grabNF.Code()
	case errors.As(err, &runtimeNF):
		return runtimeNF.Code()
	case errors.As(err, &appSetNF):
		return appSetNF.Code()
	case errors.As(err, &qbitSetNF):
		return qbitSetNF.Code()
	case errors.As(err, &scanRunNF):
		return scanRunNF.Code()
	case errors.As(err, &decisionNF):
		return decisionNF.Code()
	case errors.As(err, &wbNF):
		return wbNF.Code()
	}
	return "internal_error"
}
