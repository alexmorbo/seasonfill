// Package tmdb — auth.go owns the v3/v4 key-format detection and the
// request-mutation helper that picks the right TMDB auth strategy.
//
// TMDB exposes TWO auth methods on the same `/3/...` endpoints:
//
//   - v3 API Key — 32-char hex string. Auth via `?api_key=<key>` query
//     param. Sending it as `Authorization: Bearer <key>` returns 401
//     "Invalid API key" (status_code=7).
//   - v4 Read Access Token — long JWT (eyJ…). Auth via
//     `Authorization: Bearer <token>` header. Sending it as a query
//     param works too but is not the documented form.
//
// The operator pastes whichever one TMDB's UI offered on the day they
// signed up — both forms are valid TMDB credentials, but the wire
// protocol differs. DetectAuthFormat classifies the token; ApplyAuth
// mutates an outgoing *http.Request to use the right method.
//
// Story 471 (B-18) — added after the Phase 2 cutover when v3 hex keys
// were silently failing enrichment with 401s.
package tmdb

import (
	"net/http"
	"regexp"
	"strings"
)

// AuthFormat classifies a TMDB credential. The zero value
// (AuthFormatUnknown) intentionally defaults ApplyAuth to the v4
// Bearer-header path — that preserves the historical (pre-471)
// behaviour for short test tokens used in unit tests and for any
// not-yet-classified shape a future TMDB rev might introduce.
type AuthFormat int

const (
	// AuthFormatUnknown is the zero value. ApplyAuth treats it as v4
	// Bearer for backward compatibility — the New() constructor logs
	// `tmdb.auth.unknown_format` WARN once when this is the resolved
	// format so the operator sees the hint at boot.
	AuthFormatUnknown AuthFormat = iota
	// AuthFormatV3 — 32-char hex API key. Auth method: ?api_key=<key>.
	AuthFormatV3
	// AuthFormatV4 — long JWT Read Access Token. Auth method:
	// Authorization: Bearer <token>.
	AuthFormatV4
)

// String renders the format as a stable enum tag — used in log
// attributes (`auth_format=v3`). The values are part of the public
// log shape: do not rename without a memory entry + operator note.
func (f AuthFormat) String() string {
	switch f {
	case AuthFormatV3:
		return "v3"
	case AuthFormatV4:
		return "v4"
	default:
		return "unknown"
	}
}

// v3HexPattern matches a TMDB v3 API key — exactly 32 hex characters,
// case-insensitive. TMDB issues lowercase by default but the UI's
// copy-paste does not normalize, so a stray uppercase paste still
// classifies correctly.
var v3HexPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// DetectAuthFormat classifies a TMDB credential. Pure — no I/O, no
// allocations beyond what the regex / strings.Count costs. Empty
// string returns AuthFormatUnknown (the New() constructor already
// errors out on an empty token before this is called in production).
//
// Rules:
//
//   - 32 hex chars → AuthFormatV3.
//   - Starts with `eyJ` AND contains exactly two `.` separators
//     (i.e. three dot-segments — the JWT shape per RFC 7519 §3) →
//     AuthFormatV4.
//   - Anything else → AuthFormatUnknown.
//
// The v4 check intentionally does NOT decode the base64 payload — a
// malformed JWT is still routed to the Bearer-header path so TMDB
// itself surfaces a 401, which the test_runner classifies as
// auth_failed (a visible operator-facing error). The detector's job
// is auth-method selection, not credential validation.
func DetectAuthFormat(token string) AuthFormat {
	if token == "" {
		return AuthFormatUnknown
	}
	if v3HexPattern.MatchString(token) {
		return AuthFormatV3
	}
	if strings.HasPrefix(token, "eyJ") && strings.Count(token, ".") == 2 {
		return AuthFormatV4
	}
	return AuthFormatUnknown
}

// ApplyAuth mutates an outgoing *http.Request to carry the TMDB
// credential in the correct shape for the given format. Idempotent
// when called on a fresh request; calling it twice on the same
// request will overwrite both the Authorization header and the
// api_key query param (no duplication).
//
// v3: parses req.URL.Query(), sets `api_key`, writes back. Existing
// query params (language, append_to_response, …) are preserved —
// url.Values' Set is key-scoped. DOES NOT set Authorization — that
// would leak a no-op header for the v3 path.
//
// v4 / Unknown: sets `Authorization: Bearer <token>`. DOES NOT add a
// query param — keeps audit-log URLs clean of credentials for the
// v4 operators.
//
// req or token nil/empty → no-op (defensive — production never hits
// this path because New() errors out on empty token earlier; tests
// occasionally construct a bare request, hence the guard).
func ApplyAuth(req *http.Request, token string, format AuthFormat) {
	if req == nil || token == "" {
		return
	}
	switch format {
	case AuthFormatV3:
		// url.Values.Set is map-keyed — preserves siblings like
		// language= and include_image_language= that doOnce builds
		// before this call.
		q := req.URL.Query()
		q.Set("api_key", token)
		req.URL.RawQuery = q.Encode()
	default:
		// AuthFormatV4 and AuthFormatUnknown both take the historical
		// Bearer-header path. Unknown logs at construction (Client.New)
		// so the operator sees the hint; the request itself proceeds
		// so TMDB can surface its own 401 to the test runner.
		req.Header.Set("Authorization", "Bearer "+token)
	}
}
