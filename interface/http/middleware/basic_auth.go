package middleware

import (
	"encoding/base64"
	"strings"
)

// basicAuthPrefix is the case-sensitive scheme token per RFC 7617 §2.
// We accept it case-insensitively (some clients send "basic ").
const basicAuthPrefix = "Basic "

// basicRealmHeader is the value emitted on absent/malformed Authorization
// headers in Basic mode. Hardcoded realm per parent D-decision.
const basicRealmHeader = `Basic realm="Seasonfill"`

// parseBasicHeader extracts username and password from an RFC 7617 Basic
// Authorization header value. Returns ok=false on:
//   - empty input
//   - missing or wrong scheme prefix
//   - empty credential payload after the scheme
//   - base64 decode failure
//   - decoded payload missing a colon separator
//   - empty username (RFC 7617 §2 forbids colon in username, empty allowed
//     only for "no user"; we reject for safety)
//
// Empty password IS allowed (RFC 7617 §2 explicitly permits it) — caller's
// repo lookup will fail constant-time on the empty-string compare.
//
// The function does not short-circuit on bad inputs in a timing-revealing
// way; bcrypt later dominates the timing surface so micro-timing on parse
// is not a real attack vector, but we keep branches uniform.
func parseBasicHeader(header string) (username, password string, ok bool) {
	if header == "" {
		return "", "", false
	}
	if len(header) < len(basicAuthPrefix) {
		return "", "", false
	}
	if !strings.EqualFold(header[:len(basicAuthPrefix)], basicAuthPrefix) {
		return "", "", false
	}
	payload := strings.TrimSpace(header[len(basicAuthPrefix):])
	if payload == "" {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", "", false
	}
	// RFC 7617 §2: exactly one colon separates user from password.
	// Username must not contain colon; password may contain anything
	// after the first colon (so we split on the FIRST colon, not last).
	decoded := string(raw)
	idx := strings.IndexByte(decoded, ':')
	if idx < 0 {
		return "", "", false
	}
	user := decoded[:idx]
	pass := decoded[idx+1:]
	if user == "" {
		return "", "", false
	}
	return user, pass, true
}
