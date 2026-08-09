// Package icsfeed builds the RFC 5545 iCalendar subscription feed backing
// GET /api/v1/calendar.ics. It owns the HMAC token scheme (domain-separated
// from the session cookie), the revocation epoch check, the upcoming-focused
// window, and the iCalendar rendering. The calendar query itself is reused
// from internal/catalog/app/calendar (no second query is written here).
package icsfeed

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// tokenPurpose tags the signed payload so a token minted for one domain
// (ICS) is structurally distinct from any other signed blob. The verifier
// asserts it — defense-in-depth on top of the separate signing key.
const tokenPurpose = "ics"

// ErrRevoked is the single opaque sentinel returned for EVERY token
// rejection (malformed / bad signature / wrong purpose / epoch mismatch).
// Callers MUST NOT leak which underlying check failed.
var ErrRevoked = errors.New("ics token invalid or revoked")

// Payload is the signed token body. Wire keys are compact single letters.
// Epoch is the app_config.ics_epoch snapshot at mint time.
type Payload struct {
	Purpose string `json:"p"` // always tokenPurpose
	Scope   string `json:"s"` // library|followed|all (normalized)
	Epoch   int64  `json:"e"`
}

// SignToken produces `base64url(json).base64url(hmac)` — the SAME wire
// shape as the session cookie, but the HMAC is keyed by the ICS-domain key
// and the payload carries purpose:"ics". Scope is normalized before signing.
func SignToken(key []byte, scope string, epoch int64) (string, error) {
	body, err := json.Marshal(Payload{
		Purpose: tokenPurpose,
		Scope:   normalizeScope(scope),
		Epoch:   epoch,
	})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// VerifyToken validates the signature (constant-time) and the purpose tag.
// It does NOT check the epoch — that needs a DB read and is done by the
// usecase against the live ics_epoch. Every failure returns ErrRevoked so
// the caller cannot distinguish a tampered token from a wrong-purpose one.
func VerifyToken(key []byte, token string) (Payload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Payload{}, ErrRevoked
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Payload{}, ErrRevoked
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Payload{}, ErrRevoked
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(body)
	if subtle.ConstantTimeCompare(sig, mac.Sum(nil)) != 1 {
		return Payload{}, ErrRevoked
	}
	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		return Payload{}, ErrRevoked
	}
	if p.Purpose != tokenPurpose {
		return Payload{}, ErrRevoked
	}
	p.Scope = normalizeScope(p.Scope)
	return p, nil
}

// normalizeScope collapses anything but library|followed to "all"
// (mirrors the calendar usecase's scope normalization).
func normalizeScope(s string) string {
	switch s {
	case "library", "followed":
		return s
	default:
		return "all"
	}
}
