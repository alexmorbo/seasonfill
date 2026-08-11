package sonarr

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/arrcore"
)

// StatusError is the canonical non-2xx arr error. Ф6-R-2 moved the concrete
// type to arrcore; this alias keeps sonarr.StatusError identical to
// arrcore.StatusError so every external errors.As(&sonarr.StatusError) /
// errors.Is chain and every &sonarr.StatusError{...} literal is unchanged.
type StatusError = arrcore.StatusError

// IsTransient / Is4xx / IsAuth / IsReleaseGone / IsReleaseAlreadyAdded /
// Classifier stay here UNCHANGED (external callers: drainer, grab wiring,
// regrab). They operate on *arrcore.StatusError via the alias.

// IsTransient reports whether a Sonarr error is retry-eligible. Covers:
//   - HTTP 5xx
//   - HTTP 408 (Request Timeout) and 429 (Too Many Requests) (H-3)
//   - network/DNS/connect/refused
//   - timeouts (including context.DeadlineExceeded surfaced via url.Error)
//
// 408 and 429 are checked BEFORE the generic 4xx branch so Prowlarr/Sonarr
// throttling does not trigger the 72h guid cooldown path.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		if se.Status == 408 || se.Status == 429 {
			return true
		}
		return se.Status >= 500 && se.Status <= 599
	}
	var nerr net.Error
	if errors.As(err, &nerr) {
		if nerr.Timeout() {
			return true
		}
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		if uerr.Timeout() {
			return true
		}
		// fall through — let net.Error / specific checks below decide
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	return false
}

// Is4xx reports whether the error carries a 4xx HTTP status. Note: this stays
// true for 401/403 — use IsAuth for the auth-specific predicate.
func Is4xx(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status >= 400 && se.Status < 500
	}
	return false
}

// IsAuth reports whether the error is a 401/403 from Sonarr.
func IsAuth(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status == 401 || se.Status == 403
	}
	return false
}

// IsReleaseGone reports whether the error carries a 404 or 410 HTTP
// status — Sonarr's signal that POST /api/v3/release could not find
// the requested release on the indexer (forum topic deleted / topic
// id retired). 404 is the common case; 410 covers indexers that
// explicitly tombstone removed releases. Other 4xx codes (400, 401,
// 403) stay false — the caller distinguishes "release gone" from
// "auth broken / bad payload" because the right reaction differs:
// release-gone falls through to the evaluator search path, auth/bad
// payload surfaces as a real error.
func IsReleaseGone(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status == 404 || se.Status == 410
	}
	return false
}

// IsReleaseAlreadyAdded reports whether the error is a Sonarr 500
// wrapping qBittorrent's 409 Conflict on POST /api/v2/torrents/add —
// the "hash already present in qBit" condition. Sonarr surfaces this
// as HTTP 500 with body containing the literal substring
// `[409:Conflict]` together with `qBittorrent` and the qBit endpoint
// path. Watchdog story 117 treats the situation as success-equivalent:
// the replay's intent (have the file in qBit) was already realised
// before the POST fired, so we record an OutcomeGrab decision row
// rather than an error.
//
// The match is intentionally tight (literal `[409:Conflict]` AND
// `qBittorrent` AND `/api/v2/torrents/add` substrings, all
// case-insensitive) so unrelated 500 bodies don't get misclassified.
// Returns false on nil, non-StatusError, or any body that doesn't
// match all three markers.
func IsReleaseAlreadyAdded(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	if se.Status != http.StatusInternalServerError {
		return false
	}
	body := strings.ToLower(se.Body)
	if !strings.Contains(body, "[409:conflict]") {
		return false
	}
	if !strings.Contains(body, "qbittorrent") {
		return false
	}
	if !strings.Contains(body, "/api/v2/torrents/add") {
		return false
	}
	return true
}

// Classifier is a struct-shaped adapter implementing application/grab.classifier.
type Classifier struct{}

func (Classifier) IsTransient(err error) bool { return IsTransient(err) }
func (Classifier) Is4xx(err error) bool       { return Is4xx(err) }
func (Classifier) IsAuth(err error) bool      { return IsAuth(err) }
