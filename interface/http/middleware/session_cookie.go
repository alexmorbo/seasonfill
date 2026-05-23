package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// SessionCookieName is the HMAC-signed session cookie.
const SessionCookieName = "seasonfill_session"

// SessionPayload is the cookie body. Exp is unix-second; verifier
// checks `now <= exp`.
type SessionPayload struct {
	Username string `json:"u"`
	Exp      int64  `json:"e"`
}

var (
	ErrSessionMalformed = errors.New("session cookie malformed")
	ErrSessionSignature = errors.New("session cookie signature mismatch")
	ErrSessionExpired   = errors.New("session cookie expired")
)

// SignSession produces `base64url(json).base64url(hmac)`. HMAC over
// the JSON bytes (NOT the base64). secret = apiKey as []byte.
func SignSession(secret []byte, username string, expiresAt time.Time) (string, error) {
	body, err := json.Marshal(SessionPayload{Username: username, Exp: expiresAt.Unix()})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// VerifySession returns the payload on success. Callers MUST NOT leak
// which sentinel triggered the rejection.
func VerifySession(secret []byte, token string, now time.Time) (SessionPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return SessionPayload{}, ErrSessionMalformed
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return SessionPayload{}, ErrSessionMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return SessionPayload{}, ErrSessionMalformed
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	if subtle.ConstantTimeCompare(sig, mac.Sum(nil)) != 1 {
		return SessionPayload{}, ErrSessionSignature
	}
	var p SessionPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return SessionPayload{}, ErrSessionMalformed
	}
	if now.Unix() > p.Exp {
		return SessionPayload{}, ErrSessionExpired
	}
	if p.Username == "" {
		return SessionPayload{}, ErrSessionMalformed
	}
	return p, nil
}
